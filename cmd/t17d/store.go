package main

// Durable state in Postgres (T17_DATABASE_URL): the KU -> BAAS user id map (t17_accounts) and the
// rewards session clock (t17_session_clock), so a restart loses neither. The pool is capped at
// two connections. The clock is stored as text: its keys contain NUL bytes, which jsonb refuses.
//
// If the database is unreachable kleid keeps running on memory, logs at ERROR level, keeps the
// unsaved writes pending and retries every 30 seconds (the clock on every tick); the pool itself
// reconnects when the server returns.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	emit "github.com/openpak/rewards-emit"

	"openpak/t17/redact"
)

type pgStore struct{ pool *pgxpool.Pool }

const schema = `CREATE TABLE IF NOT EXISTS t17_accounts (
	gid           text PRIMARY KEY,
	baas_user_id text NOT NULL,
	last_seen    timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS t17_session_clock (
	metric   text PRIMARY KEY,
	state    text NOT NULL,
	saved_at timestamptz NOT NULL DEFAULT now()
);
` + durableSchema

func openStore(ctx context.Context, url string) (*pgStore, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, err
	}
	return &pgStore{pool}, nil
}

func (p *pgStore) upsert(ctx context.Context, gid, baas string) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO t17_accounts (gid, baas_user_id, last_seen) VALUES ($1,$2,now())
		ON CONFLICT (gid) DO UPDATE SET baas_user_id = EXCLUDED.baas_user_id, last_seen = now()`, gid, baas)
	return err
}

func (p *pgStore) load(ctx context.Context) (map[string]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT gid, baas_user_id FROM t17_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var gid, baas string
		if err := rows.Scan(&gid, &baas); err != nil {
			return nil, err
		}
		out[gid] = baas
	}
	return out, rows.Err()
}

func (p *pgStore) saveClock(ctx context.Context, metric string, raw []byte) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO t17_session_clock (metric, state, saved_at) VALUES ($1,$2,now())
		ON CONFLICT (metric) DO UPDATE SET state = EXCLUDED.state, saved_at = now()`, metric, string(raw))
	return err
}

func (p *pgStore) loadClock(ctx context.Context, metric string) ([]byte, error) {
	var raw []byte
	err := p.pool.QueryRow(ctx, `SELECT state FROM t17_session_clock WHERE metric = $1`, metric).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return raw, err
}

// clockBacking stores the session clock as JSON.
type clockBacking interface {
	saveClock(ctx context.Context, metric string, raw []byte) error
	loadClock(ctx context.Context, metric string) ([]byte, error)
}

// clockSaver writes the clock when it changed. A failed write leaves it unsaved, so the next tick
// retries; the failure is logged at error level at most once a minute.
type clockSaver struct {
	mu       sync.Mutex
	back     clockBacking
	metric   string
	last     []byte
	lastWarn time.Time
}

func (c *clockSaver) save(st emit.State) {
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if string(raw) == string(c.last) {
		return
	}
	if c.metric == "" {
		c.metric = sessionMetric
	}
	if c.back == nil {
		c.warn("session clock store not connected: clock held in memory only")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.back.saveClock(ctx, c.metric, raw); err != nil {
		c.warn("saving the session clock failed (will retry): " + redact.Err(err))
		return
	}
	c.last = raw
}

func (c *clockSaver) warn(msg string) {
	if time.Since(c.lastWarn) > time.Minute {
		c.lastWarn = time.Now()
		log.Printf("[t17] ERROR %s", msg)
	}
}

func (c *clockSaver) attach(back clockBacking) {
	c.mu.Lock()
	c.back = back
	c.mu.Unlock()
}

// connect wires one reachable store in: loads the id map, restores the clock (only before the first
// tick, so only on the first connection) and makes later writes go through.
func (s *server) connect(ctx context.Context, db interface {
	accountBacking
	clockBacking
}, sess *emit.Sessions, restore bool) error {
	if err := s.accounts.attach(ctx, db); err != nil {
		return err
	}
	if restore {
		raw, err := db.loadClock(ctx, sessionMetric)
		if err != nil {
			return err
		}
		if raw != nil {
			var st emit.State
			if json.Unmarshal(raw, &st) == nil {
				sess.Restore(st)
				s.clock.mu.Lock()
				s.clock.last = raw
				s.clock.mu.Unlock()
			}
		}
	}
	if p, ok := db.(*pgStore); ok {
		if err := s.storage.attach(ctx, p.pool); err != nil {
			return err
		}
		if err := s.sessions.attach(ctx, p.pool); err != nil {
			return err
		}
	}
	s.clock.attach(db)
	return nil
}

// startStore connects to Postgres when url is set. It must run before the first tick. When the
// database is down at start it logs at error level and keeps retrying in the background.
func (s *server) startStore(ctx context.Context, url string, sess *emit.Sessions) {
	if url == "" {
		return
	}
	try := func(restore bool) error {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		db, err := openStore(cctx, url)
		if err != nil {
			return err
		}
		if err := s.connect(cctx, db, sess, restore); err != nil {
			db.pool.Close()
			return err
		}
		return nil
	}
	if err := try(true); err == nil {
		log.Printf("[t17] state persisted in Postgres (account map, session clock)")
		return
	} else {
		log.Printf("[t17] ERROR Postgres unavailable, running on memory and retrying every 30s: %v", redact.Err(err))
	}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// The clock is not restored on a late connection: ticks have already run.
				if err := try(false); err == nil {
					log.Printf("[t17] Postgres is back: pending writes flushed")
					return
				}
			}
		}
	}()
}
