package main

import (
	"hash/crc32"
	"strings"
	"sync"
)

// client is one websocket's worth of identity. The token carries the player id (mintToken builds
// "t17-<globalPlayerId>-<hash>"), and the display name is remembered from get-token because T17BE
// requires a *full* name -- it splits displayNameFull at '#' and fails when there is no separator.
type client struct {
	s     *server
	token string
}

func (c *client) playerID() string {
	// mintToken builds "t17-<globalPlayerId>-<base64url hash>". Split at the FIRST dash: the id is
	// hex and has none, while the hash is base64url and frequently does — cutting at the last dash
	// left part of the hash glued to the id, which then became the session owner, the player entry
	// and the display name, and the game left every session it had just created.
	id := strings.TrimPrefix(c.token, "t17-")
	if i := strings.IndexByte(id, '-'); i > 0 {
		id = id[:i]
	}

	return id
}

func (c *client) displayName() string {
	id := c.playerID()
	if name := c.s.nameFor(id); name != "" {
		return name
	}

	// Fall back to the same shape get-token mints, so the '#' is never missing.
	return id + "#" + fourDigits(id)
}

// createSession records a new session owned by this player and returns its wire form.
// InternalResponse_SessionsCreate (0x685740) reads `session` → `players` → **`id`** →
// `closedToNewPlayers` → `membersCanInviteOthers` → `type` → `owner` → `maxPlayers` →
// `supportedPlatforms`, and refuses with a named line for each of: an empty type, an unknown type
// (only `game` and `group`), a bad maxPlayers, a bad `id` ("Bad SessionID!"), a bad `owner`
// ("Bad sessionOwner!") and a player it cannot parse. `sessionId` goes out too because the invite
// structures use that name.
func (c *client) createSession(in map[string]any) map[string]any {
	owner := c.playerID()
	kind, _ := in["type"].(string)
	if kind != "game" && kind != "group" {
		kind = "group"
	}
	maxPlayers, ok := in["maxPlayers"].(float64)
	if !ok || maxPlayers < 1 {
		maxPlayers = 4
	}
	chunk, hasChunk := in["chunk"]
	if !hasChunk {
		chunk = "{}"
	}
	platforms, hasPlatforms := in["supportedPlatforms"]
	if !hasPlatforms {
		platforms = []any{"all"}
	}
	canInvite, _ := in["membersCanInviteOthers"].(bool)
	closed, _ := in["closedToNewPlayers"].(bool)

	s := &session{
		ID: "s-" + owner, Owner: owner, Type: kind, MaxPlayers: int(maxPlayers),
		CanInvite: canInvite, Closed: closed, Chunk: chunk, Platforms: platforms,
		Players: []string{owner},
	}
	c.s.sessions.put(s)

	return c.wire(s)
}

// wire is the JSON shape of a stored session.
func (c *client) wire(s *session) map[string]any {
	players := make([]any, 0, len(s.Players))
	for _, id := range s.Players {
		players = append(players, c.playerOf(id))
	}

	return map[string]any{
		"id":                     s.ID,
		"sessionId":              s.ID,
		"type":                   s.Type,
		"owner":                  s.Owner,
		"maxPlayers":             s.MaxPlayers,
		"players":                players,
		"membersCanInviteOthers": s.CanInvite,
		"closedToNewPlayers":     s.Closed,
		"chunk":                  s.Chunk,
		"supportedPlatforms":     s.Platforms,
	}
}

// playerOf is one entry of a session's `players`. The parser (0x696370) reads globalPlayerId,
// displayName, platformId, platformPlayerId, deviceId, gameId, connected, chunk, liveData and
// linkedAccounts, and a player it cannot parse fails the whole session response. A Switch player's
// globalPlayerId is their NSA id, so platformPlayerId is the same value.
func (c *client) playerOf(id string) map[string]any {
	name := c.s.nameFor(id)
	if name == "" {
		name = id + "#" + fourDigits(id)
	}

	return map[string]any{
		"globalPlayerId":   id,
		"platformPlayerId": id,
		"displayName":      name,
		"platformId":       "nintendo",
		"deviceId":         "nintendo-switch",
		"gameId":           "ProjectJourney",
		"connected":        c.s.conn(id) != nil,
		"chunk":            "{}",
		"linkedAccounts":   []any{},
	}
}

func fourDigits(id string) string {
	const digits = "0123456789"
	n := crc32.ChecksumIEEE([]byte(id)) % 10000
	return string([]byte{digits[n/1000], digits[n/100%10], digits[n/10%10], digits[n%10]})
}

// names remembers the display name minted for each player at get-token.
type names struct {
	mu sync.RWMutex
	m  map[string]string
}

func (n *names) set(id, name string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.m == nil {
		n.m = map[string]string{}
	}
	n.m[id] = name
}

func (n *names) get(id string) string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return n.m[id]
}
