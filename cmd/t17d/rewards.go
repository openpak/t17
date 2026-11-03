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
	"sort"
	"time"

	emit "github.com/openpak/rewards-emit"
)

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

func (s *server) watchSessions(ctx context.Context, rw *emit.Client, every time.Duration) {
	if !rw.Enabled() {
		return
	}
	emit.NewSessions(rw, "game_sessions.moving-out-2", 0).Watch(ctx, every, s.rewardRooms)
}
