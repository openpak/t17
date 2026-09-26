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
	id := strings.TrimPrefix(c.token, "t17-")
	if i := strings.LastIndexByte(id, '-'); i > 0 {
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

// session answers /sessions/create, /update and /accept-invite. InternalResponse_SessionsCreate
// checks four things by name and logs a distinct line for each: an empty session type, a bad
// maxPlayers, a bad sessionId and a bad sessionOwner. The rest is echoed from the request so the
// client sees the session it asked for.
//
// ponytail: the id is derived from the owner, so a player rejoining gets the same session and no
// state is kept. Real session tracking is the next piece of work, and it needs two consoles to test.
func (c *client) session(in map[string]any) map[string]any {
	owner := c.playerID()
	sid, _ := in["sessionId"].(string)
	if sid == "" {
		sid = "s-" + owner
	}
	kind, _ := in["type"].(string)
	if kind == "" {
		kind = "private"
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
		platforms = []any{"nintendo"}
	}

	return map[string]any{
		"sessionId":              sid,
		"type":                   kind,
		"owner":                  owner,
		"maxPlayers":             int(maxPlayers),
		"players":                []any{map[string]any{"globalPlayerId": owner, "displayName": c.displayName()}},
		"membersCanInviteOthers": in["membersCanInviteOthers"],
		"closedToNewPlayers":     in["closedToNewPlayers"],
		"chunk":                  chunk,
		"supportedPlatforms":     platforms,
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
