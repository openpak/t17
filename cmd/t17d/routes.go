package main

// The T17BE route table, recovered from Moving Out 2 1.3.315's own code rather than guessed. Every
// route below is named by the binary: each request builder logs as
// `T17BE_<System>::InternalRequest_<Name>` and each handler as `InternalResponse_<Name>`, and the
// fields are the keys those functions read and write. docs/protocol.md has the full table with the
// addresses; the short version is that a handler which cannot find a key it needs logs
// "Missing …" and fails the request, so the shapes here are the minimum that satisfies them.
//
// ponytail: static answers for a single player with no friends, no invites and no stored data.
// Enough to bring the client online and keep it there. Session state becomes real work the moment
// two consoles need to see each other.
type route struct {
	// body returns the response body for a request, given the request's own body.
	body func(in map[string]any, c *client) map[string]any
}

func empty(map[string]any, *client) map[string]any { return map[string]any{} }

var routes = map[string]route{
	// InternalResponse_GoOnline (0x692aa4) checks nothing but the status: on 200 it sets the
	// transport's online byte and the core's go-online ack, and returns.
	"/websocket/go-online": {empty},
	"/websocket/ping":      {empty},

	// InternalResponse_GetPlayerAccount reads globalPlayerId and displayName. The display name is a
	// *full* name and T17BE splits it at '#' -- a name without one fails the split helper.
	"/player/get-account": {func(in map[string]any, c *client) map[string]any {
		return map[string]any{"globalPlayerId": c.playerID(), "displayName": c.displayName()}
	}},
	"/player/update-display-name": {func(in map[string]any, c *client) map[string]any {
		name, _ := in["newDisplayName"].(string)
		return map[string]any{"newDisplayName": name}
	}},
	// InternalResponse_BatchGetPlayerGlobalIDList reads `accounts`, each entry a globalPlayerId and
	// platformPlayerId. This is how the game turns the console's friend list into T17BE players, so
	// answering with an empty list leaves every friend unresolvable and uninvitable. A Switch
	// player's globalPlayerId *is* their NSA id -- ours is, from get-token -- so the mapping is
	// the identity one.
	"/player/get-platform-accounts": {func(in map[string]any, c *client) map[string]any {
		ids, _ := in["platformPlayerIds"].([]any)
		accounts := make([]any, 0, len(ids))
		for _, id := range ids {
			s, ok := id.(string)
			if !ok || s == "" {
				continue
			}
			accounts = append(accounts, map[string]any{"globalPlayerId": s, "platformPlayerId": s})
		}

		return map[string]any{"accounts": accounts}
	}},

	// InternalResponse_GetFriendsAndStatus and InternalResponse_GetBlockedPlayers both log
	// "Missing pResponseBody" when the body is absent, so the empty collection has to be spelled out.
	"/friends/get-friends-and-status": {func(map[string]any, *client) map[string]any {
		return map[string]any{"friends": []any{}}
	}},
	"/friends/get-blocked-players": {func(map[string]any, *client) map[string]any {
		return map[string]any{"blockList": []any{}}
	}},
	// InternalResponse_GetFriendInvites reads `invites` as an OBJECT holding `incoming` and
	// `outgoing` arrays, each entry a globalPlayerId and displayName. It rejects a display name that
	// is not a full name, per direction ("Incoming --> Bad Full Name"); empty lists avoid that.
	"/friends/get-friend-invites": {func(map[string]any, *client) map[string]any {
		return map[string]any{"invites": map[string]any{"incoming": []any{}, "outgoing": []any{}}}
	}},
	"/friends/subscribe-to-friend-status-updates": {func(map[string]any, *client) map[string]any {
		return map[string]any{"blockList": []any{}}
	}},
	"/friends/unsubscribe-to-friend-status-updates": {empty},
	"/friends/send-friend-invite":                   {empty},
	"/friends/accept-friend-invite":                 {empty},
	"/friends/reject-friend-invite":                 {empty},
	"/friends/withdraw-friend-invite":               {empty},
	"/friends/remove-friend":                        {empty},
	// InternalResponse_BlockPlayer and _UnblockPlayer log "Missing globalPlayerID in parameter 1".
	"/friends/block-player": {func(in map[string]any, c *client) map[string]any {
		return map[string]any{"globalPlayerId": in["globalPlayerId"]}
	}},
	"/friends/unblock-player": {func(in map[string]any, c *client) map[string]any {
		return map[string]any{"globalPlayerId": in["globalPlayerId"]}
	}},

	"/sessions/list-invites": {func(map[string]any, *client) map[string]any {
		return map[string]any{"invites": []any{}}
	}},
	// InternalResponse_SessionsCreate refuses an empty session type, a bad maxPlayers, a bad
	// sessionId and a bad sessionOwner, each with its own log line, so all four must be real.
	"/sessions/create": {func(in map[string]any, c *client) map[string]any {
		return map[string]any{"session": c.session(in)}
	}},
	"/sessions/update": {func(in map[string]any, c *client) map[string]any {
		return map[string]any{"session": c.session(in)}
	}},
	"/sessions/accept-invite": {func(in map[string]any, c *client) map[string]any {
		return map[string]any{"session": c.session(in)}
	}},
	"/sessions/leave":           {empty},
	"/sessions/close":           {empty},
	"/sessions/kick-player":     {empty},
	"/sessions/send-invite":     {empty},
	"/sessions/withdraw-invite": {empty},
	"/sessions/reject-invite":   {empty},
	"/sessions/grant-ownership": {empty},

	// InternalResponse_Get logs "Missing Response Body --> Failed" without one. The chunk is the
	// game's own opaque blob; we have never seen a populated one, so it stays empty rather than
	// invented.
	"/live/get": {func(in map[string]any, c *client) map[string]any {
		return map[string]any{"chunk": "{}", "dataKey": in["dataKey"]}
	}},
	"/live/put":   {empty},
	"/live/patch": {empty},

	"/storage/get":          {func(map[string]any, *client) map[string]any { return map[string]any{"value": ""} }},
	"/storage/put":          {empty},
	"/storage/delete":       {empty},
	"/storage/get-file-url": {func(map[string]any, *client) map[string]any { return map[string]any{"url": ""} }},

	"/service/send-report-sns":     {empty},
	"/service/trigger-kill-switch": {empty},
}
