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

// session answers /sessions/create, /update and /accept-invite.
// InternalResponse_SessionsCreate (0x685740) reads the keys in this order -- `session`, `players`,
// **`id`**, `closedToNewPlayers`, `membersCanInviteOthers`, `type`, `owner`, `maxPlayers`,
// `supportedPlatforms` -- and refuses with a named log line for each of: an empty session type, an
// unknown type (only `game` and `group` exist), a bad `maxPlayers`, a bad `id` ("Bad SessionID!"),
// a bad `owner` ("Bad sessionOwner!") and a player it cannot parse.
//
// The id key is `id`, not `sessionId`: sending only `sessionId` is the "Bad SessionID" failure.
// `sessionId` is what the *invite* structures use, so both are sent here and the client reads
// whichever it looks for.
//
// ponytail: the id is derived from the owner, so nothing is stored and a player rejoining lands in
// the same session. Real session tracking is the next piece of work and it needs two consoles.
func (c *client) session(in map[string]any) map[string]any {
	owner := c.playerID()
	id, _ := in["sessionId"].(string)
	if id == "" {
		id, _ = in["id"].(string)
	}
	if id == "" {
		id = "s-" + owner
	}
	kind, _ := in["type"].(string)
	if kind != "game" && kind != "group" {
		kind = "group" // the only two the client accepts; anything else is "Unknown SessionType"
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

	return map[string]any{
		"id":                     id,
		"sessionId":              id,
		"type":                   kind,
		"owner":                  owner,
		"maxPlayers":             int(maxPlayers),
		"players":                []any{c.player()},
		"membersCanInviteOthers": in["membersCanInviteOthers"],
		"closedToNewPlayers":     in["closedToNewPlayers"],
		"chunk":                  chunk,
		"supportedPlatforms":     platforms,
	}
}

// player is one entry of a session's `players`. The parser (0x696370) reads globalPlayerId,
// displayName, platformId, platformPlayerId, deviceId, gameId, connected, chunk, liveData and
// linkedAccounts; a player it cannot parse fails the whole session response. Our globalPlayerId is
// the console's own NSA id, so platformPlayerId is the same value.
func (c *client) player() map[string]any {
	id := c.playerID()

	return map[string]any{
		"globalPlayerId":   id,
		"platformPlayerId": id,
		"displayName":      c.displayName(),
		"platformId":       "nintendo",
		"deviceId":         "nintendo-switch",
		"gameId":           "ProjectJourney",
		"connected":        true,
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
