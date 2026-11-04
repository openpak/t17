package main

// Player storage blobs and pending invites in Postgres (openpak/prds#85). Once the pool is attached
// (connect, store.go) they are read and written there and nothing is kept in memory, so a restart
// loses neither. Before it is attached (the database was down at start) both fall back to the
// in-memory maps and are flushed into Postgres at attach time. Sessions stay live state.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"openpak/t17/redact"
)

const durableSchema = `CREATE TABLE IF NOT EXISTS t17_storage (
	role    text NOT NULL,
	game_id text NOT NULL,
	owner   text NOT NULL,
	slot    text NOT NULL,
	value   text NOT NULL,
	saved_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (role, game_id, owner, slot)
);
CREATE INDEX IF NOT EXISTS t17_storage_owner ON t17_storage (owner);
CREATE TABLE IF NOT EXISTS t17_invites (
	seq        bigserial,
	invitee    text NOT NULL,
	session_id text NOT NULL,
	inviter    text NOT NULL,
	type       text NOT NULL,
	platforms  jsonb,
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (invitee, session_id)
);
CREATE INDEX IF NOT EXISTS t17_invites_inviter ON t17_invites (inviter)`

func dbTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

func slotParts(key string) [4]string {
	var out [4]string
	copy(out[:], strings.SplitN(key, "\x00", 4))
	return out
}

func (st *storageStore) attach(ctx context.Context, pool *pgxpool.Pool) error {
	st.mu.Lock()
	mem := st.values
	st.mu.Unlock()
	for k, v := range mem {
		if err := storagePutDB(ctx, pool, k, v); err != nil {
			return err
		}
	}
	st.mu.Lock()
	st.values = nil
	st.pool = pool
	st.mu.Unlock()
	return nil
}

func storagePutDB(ctx context.Context, pool *pgxpool.Pool, key, value string) error {
	p := slotParts(key)
	_, err := pool.Exec(ctx, `INSERT INTO t17_storage (role, game_id, owner, slot, value) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (role, game_id, owner, slot) DO UPDATE SET value = EXCLUDED.value, saved_at = now()`,
		p[0], p[1], p[2], p[3], value)
	return err
}

func (st *storageStore) dbPool() *pgxpool.Pool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.pool
}

func (st *storageStore) logErr(what string, err error) {
	log.Printf("[t17] ERROR storage %s failed: %s", what, redact.Err(err))
}

func (st *storageStore) dbGet(pool *pgxpool.Pool, key string) (string, bool) {
	p := slotParts(key)
	ctx, cancel := dbTimeout()
	defer cancel()
	var v string
	err := pool.QueryRow(ctx, `SELECT value FROM t17_storage WHERE role=$1 AND game_id=$2 AND owner=$3 AND slot=$4`,
		p[0], p[1], p[2], p[3]).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		st.logErr("read", err)
		return "", false
	}
	return v, true
}

func (st *storageStore) dbPut(pool *pgxpool.Pool, key, value string) {
	ctx, cancel := dbTimeout()
	defer cancel()
	if err := storagePutDB(ctx, pool, key, value); err != nil {
		st.logErr("write", err)
	}
}

func (st *storageStore) dbDelete(pool *pgxpool.Pool, key string) {
	p := slotParts(key)
	ctx, cancel := dbTimeout()
	defer cancel()
	if _, err := pool.Exec(ctx, `DELETE FROM t17_storage WHERE role=$1 AND game_id=$2 AND owner=$3 AND slot=$4`,
		p[0], p[1], p[2], p[3]); err != nil {
		st.logErr("delete", err)
	}
}

// ---- invites

func (st *sessionStore) attach(ctx context.Context, pool *pgxpool.Pool) error {
	st.mu.Lock()
	mem := st.invites
	st.mu.Unlock()
	for _, list := range mem {
		for _, in := range list {
			if err := inviteInsert(ctx, pool, in); err != nil {
				return err
			}
		}
	}
	st.mu.Lock()
	st.invites = nil
	st.pool = pool
	st.mu.Unlock()
	return nil
}

func (st *sessionStore) dbPool() *pgxpool.Pool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.pool
}

func inviteInsert(ctx context.Context, pool *pgxpool.Pool, in invite) error {
	pl, err := json.Marshal(in.Platforms)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `INSERT INTO t17_invites (invitee, session_id, inviter, type, platforms) VALUES ($1,$2,$3,$4,$5::jsonb)
		ON CONFLICT (invitee, session_id) DO NOTHING`, in.Invitee, in.SessionID, in.Inviter, in.Type, string(pl))
	return err
}

func scanInvites(rows pgx.Rows) ([]invite, error) {
	defer rows.Close()
	var out []invite
	for rows.Next() {
		var in invite
		var pl []byte
		if err := rows.Scan(&in.Invitee, &in.SessionID, &in.Inviter, &in.Type, &pl); err != nil {
			return nil, err
		}
		if len(pl) > 0 {
			_ = json.Unmarshal(pl, &in.Platforms)
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

const inviteCols = `invitee, session_id, inviter, type, platforms`

func (st *sessionStore) dbAddInvite(pool *pgxpool.Pool, in invite) {
	ctx, cancel := dbTimeout()
	defer cancel()
	if err := inviteInsert(ctx, pool, in); err != nil {
		log.Printf("[t17] ERROR invite write failed: %s", redact.Err(err))
	}
}

func (st *sessionStore) dbTakeInvite(pool *pgxpool.Pool, invitee, sessionID string) (invite, bool) {
	ctx, cancel := dbTimeout()
	defer cancel()
	rows, err := pool.Query(ctx, `DELETE FROM t17_invites WHERE seq = (
		SELECT seq FROM t17_invites WHERE invitee = $1 AND ($2 = '' OR session_id = $2) ORDER BY seq LIMIT 1)
		RETURNING `+inviteCols, invitee, sessionID)
	if err != nil {
		log.Printf("[t17] ERROR invite take failed: %s", redact.Err(err))
		return invite{}, false
	}
	list, err := scanInvites(rows)
	if err != nil || len(list) == 0 {
		return invite{}, false
	}
	return list[0], true
}

func (st *sessionStore) dbInvitesFor(pool *pgxpool.Pool, invitee string) []invite {
	ctx, cancel := dbTimeout()
	defer cancel()
	rows, err := pool.Query(ctx, `SELECT `+inviteCols+` FROM t17_invites WHERE invitee = $1 ORDER BY seq`, invitee)
	if err != nil {
		log.Printf("[t17] ERROR invite read failed: %s", redact.Err(err))
		return nil
	}
	list, _ := scanInvites(rows)
	return list
}

// eraseDurable deletes a player's storage slots and every invite to or from them; it returns the rows removed.
func eraseDurable(ctx context.Context, pool *pgxpool.Pool, gid string) (int, error) {
	n := 0
	for _, q := range []string{
		`DELETE FROM t17_storage WHERE owner = $1`,
		`DELETE FROM t17_invites WHERE invitee = $1 OR inviter = $1`,
	} {
		tag, err := pool.Exec(ctx, q, gid)
		if err != nil {
			return n, err
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}
