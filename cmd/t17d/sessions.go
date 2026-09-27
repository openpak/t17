package main

import (
	"log"
	"sync"
)

// Sessions and invites, held in memory, plus the pushes that tell the other player about them.
//
// A session only exists for the client once the server remembers it: an invite has to be recorded
// against the invitee, pushed to them so their UI notices, returned by their /sessions/list-invites,
// and resolved by /sessions/accept-invite to the SAME session with both players in it. Deriving a
// session per connection, which is what this did before, cannot carry an invite between two consoles.
//
// ponytail: one process, one map, no persistence. Sessions die with the server, which is right for a
// lobby — nobody is in one across a restart. Persist only if a title turns out to need it.
type sessionStore struct {
	mu      sync.Mutex
	byID    map[string]*session
	invites map[string][]invite // keyed by invitee
}

type session struct {
	ID         string
	Owner      string
	Type       string
	MaxPlayers int
	CanInvite  bool
	Closed     bool
	Chunk      any
	Platforms  any
	Players    []string // global player ids, owner first
}

type invite struct {
	SessionID string
	Inviter   string
	Invitee   string
	Type      string
	Platforms any
}

func (st *sessionStore) put(s *session) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.byID == nil {
		st.byID = map[string]*session{}
	}
	st.byID[s.ID] = s
}

func (st *sessionStore) get(id string) *session {
	st.mu.Lock()
	defer st.mu.Unlock()

	return st.byID[id]
}

// join adds a player and reports the members who were already there, so they can be told.
func (st *sessionStore) join(id, player string) (*session, []string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.byID[id]
	if s == nil {
		return nil, nil
	}
	existing := append([]string(nil), s.Players...)
	for _, p := range s.Players {
		if p == player {
			return s, existing
		}
	}
	s.Players = append(s.Players, player)

	return s, existing
}

// leave removes a player and reports who remains.
func (st *sessionStore) leave(id, player string) (*session, []string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.byID[id]
	if s == nil {
		return nil, nil
	}
	kept := s.Players[:0]
	for _, p := range s.Players {
		if p != player {
			kept = append(kept, p)
		}
	}
	s.Players = kept
	if len(s.Players) == 0 {
		delete(st.byID, id)
	}

	return s, append([]string(nil), s.Players...)
}

func (st *sessionStore) addInvite(in invite) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.invites == nil {
		st.invites = map[string][]invite{}
	}
	for _, existing := range st.invites[in.Invitee] {
		if existing.SessionID == in.SessionID {
			return // already invited to this one
		}
	}
	st.invites[in.Invitee] = append(st.invites[in.Invitee], in)
}

func (st *sessionStore) takeInvite(invitee, sessionID string) (invite, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	list := st.invites[invitee]
	for i, in := range list {
		if in.SessionID == sessionID || sessionID == "" {
			st.invites[invitee] = append(list[:i:i], list[i+1:]...)

			return in, true
		}
	}

	return invite{}, false
}

func (st *sessionStore) invitesFor(invitee string) []invite {
	st.mu.Lock()
	defer st.mu.Unlock()

	return append([]invite(nil), st.invites[invitee]...)
}

// The session-system message types, from the dispatcher's jump table at 0x6821a0: it subtracts 2000
// and allows twelve cases, the same numbers the request builders use.
const (
	msgSessionUpdated      = 2000
	msgSessionClosed       = 2001
	msgPlayerLeft          = 2004
	msgPlayerJoined        = 2005
	msgInviteReceived      = 2007
	msgInviteSent          = 2008
	msgInviteWithdrawn     = 2009
	msgInviteAccepted      = 2010
	msgInviteRejected      = 2011
	sessionSystemID        = "2"
	messageProtocolVersion = "1"
)

// push sends one server message to a player, if they are connected. The envelope is what
// __ParseJSONObject and its message arm (0x694180) read: `action` at the frame root, then `body`
// holding messageSystemId (a string), messageType (a number), from, messageProtocolVersion, and the
// payload under `message`. A body without an inner `message` is refused with
// "No inner Message Body for Message".
func (s *server) push(to, from string, messageType int, payload map[string]any) {
	c := s.conn(to)
	if c == nil {
		return // not online; their list-invites will carry it when they next ask
	}
	frame := map[string]any{
		"action": "message",
		"body": map[string]any{
			"messageSystemId":        sessionSystemID,
			"messageType":            messageType,
			"messageProtocolVersion": messageProtocolVersion,
			"from":                   from,
			"message":                payload,
		},
	}
	if err := c.sendJSON(frame); err != nil {
		log.Printf("[t17] push %d to %s failed: %v", messageType, to, err)

		return
	}
	log.Printf("[t17] push %d to %s from %s", messageType, to, from)
}

// pushAll sends to everyone in the list except `except`.
func (s *server) pushAll(players []string, except, from string, messageType int, payload map[string]any) {
	for _, p := range players {
		if p != except {
			s.push(p, from, messageType, payload)
		}
	}
}
