package main

// Rewards sessions for Moving Out 2.
//
// A T17BE session is the game's own lobby: /sessions/create makes it, accept-invite joins it and
// leave-session or close-session ends membership, all over the authenticated websocket, so the
// server sees every join and leave. A player counts once per session after REWARDS_SESSION_MIN
// (3 minutes) of unbroken time with at least one other player, and only while their websocket is
// connected: a console that dropped off without leaving stops counting at once.
//
// Identity: get-token verifies the console's BAAS id_token, whose subject is the BAAS user id,
// and remembers it against the global player id the session store uses. Session ids carry 48
// random bits, so they are unique over time and are the dedupe key as they are.

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	emit "github.com/openpak/rewards-emit"

	"openpak/t17/redact"
)

// accountBacking is where the global player id -> BAAS user id map survives a restart (Postgres in production).
type accountBacking interface {
	upsert(ctx context.Context, gid, baas string) error
	load(ctx context.Context) (map[string]string, error)
}

// accountStore remembers which BAAS user a global player id was minted for, in memory and, once a backing
// store is attached, durably. Writes that cannot reach the store (not attached yet, or the database
// is down) are kept pending, logged at error level, and written through the next time the store is
// reachable (flush, called on every set and by the reconnect loop).
type accountStore struct {
	mu      sync.Mutex
	by      map[string]string
	pending map[string]string
	back    accountBacking
}

// attach loads the persisted map, makes later sets write through to it, and flushes what was pending.
func (b *accountStore) attach(ctx context.Context, back accountBacking) error {
	m, err := back.load(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if b.by == nil {
		b.by = map[string]string{}
	}
	for k, v := range m {
		if _, mine := b.pending[k]; !mine {
			b.by[k] = v
		}
	}
	b.back = back
	b.mu.Unlock()
	b.flush(ctx)
	return nil
}

func (b *accountStore) set(gid, baas string) {
	b.mu.Lock()
	if b.by == nil {
		b.by = map[string]string{}
	}
	if b.pending == nil {
		b.pending = map[string]string{}
	}
	b.by[gid] = baas
	b.pending[gid] = baas
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b.flush(ctx)
}

// flush writes the pending entries to the store, keeping any that fail.
func (b *accountStore) flush(ctx context.Context) {
	b.mu.Lock()
	back := b.back
	todo := make(map[string]string, len(b.pending))
	for k, v := range b.pending {
		todo[k] = v
	}
	b.mu.Unlock()
	if len(todo) == 0 {
		return
	}
	if back == nil {
		log.Printf("[t17] ERROR account map store not connected: %d entries held in memory only until it is", len(todo))
		return
	}
	for k, v := range todo {
		if err := back.upsert(ctx, k, v); err != nil {
			log.Printf("[t17] ERROR persisting the account map failed (%d entries pending, will retry): %v", len(todo), redact.Err(err))
			return
		}
		b.mu.Lock()
		if b.pending[k] == v {
			delete(b.pending, k)
		}
		b.mu.Unlock()
	}
}

func (b *accountStore) get(gid string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.by[gid]
}

// rewardRooms is every open session with the connected members it holds.
func (s *server) rewardRooms() []emit.Room {
	s.sessions.mu.Lock()
	rooms := make([]emit.Room, 0, len(s.sessions.byID))
	for id, sess := range s.sessions.byID {
		rooms = append(rooms, emit.Room{ID: "mo2:" + id})
		for _, p := range sess.Players {
			if s.conn(p) == nil {
				continue
			}
			if acct := s.accounts.get(p); acct != "" {
				r := &rooms[len(rooms)-1]
				r.Players = append(r.Players, emit.Identity{Namespace: "switch", Subject: acct})
			}
		}
	}
	s.sessions.mu.Unlock()
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].ID < rooms[j].ID })
	return rooms
}

const sessionMetric = "game_sessions.moving-out-2"

// runSessions saves the clock after every tick and starts the watcher.
func (s *server) runSessions(ctx context.Context, sess *emit.Sessions, every time.Duration) {
	sess.SetOnTick(s.clock.save)
	sess.Watch(ctx, every, s.rewardRooms)
}
