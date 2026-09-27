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
	// status overrides the 200 a route answers with. The client maps statuses to its own error
	// codes -- 400 to 8, 403 to 7, 404 to 5 -- and some handlers treat one of those as a normal
	// outcome rather than a failure.
	status int
}

func empty(map[string]any, *client) map[string]any { return map[string]any{} }

var routes = map[string]route{
	// InternalResponse_GoOnline (0x692aa4) checks nothing but the status: on 200 it sets the
	// transport's online byte and the core's go-online ack, and returns.
	"/websocket/go-online": {body: empty},
	"/websocket/ping":      {body: empty},

	// InternalResponse_GetPlayerAccount reads globalPlayerId and displayName. The display name is a
	// *full* name and T17BE splits it at '#' -- a name without one fails the split helper.
	"/player/get-account": {body: func(in map[string]any, c *client) map[string]any {
		return map[string]any{"globalPlayerId": c.playerID(), "displayName": c.displayName()}
	}},
	"/player/update-display-name": {body: func(in map[string]any, c *client) map[string]any {
		name, _ := in["newDisplayName"].(string)
		return map[string]any{"newDisplayName": name}
	}},
	// InternalResponse_BatchGetPlayerGlobalIDList reads `accounts`, each entry a globalPlayerId and
	// platformPlayerId. This is how the game turns the console's friend list into T17BE players, so
	// answering with an empty list leaves every friend unresolvable and uninvitable. A Switch
	// player's globalPlayerId is made from their NSA id (globalID), so every id asked for has one.
	"/player/get-platform-accounts": {body: func(in map[string]any, c *client) map[string]any {
		ids, _ := in["platformPlayerIds"].([]any)
		accounts := make([]any, 0, len(ids))
		for _, id := range ids {
			s, ok := id.(string)
			if !ok || s == "" {
				continue
			}
			accounts = append(accounts, map[string]any{"globalPlayerId": globalID(s), "platformPlayerId": s})
		}

		return map[string]any{"accounts": accounts}
	}},

	// InternalResponse_GetFriendsAndStatus and InternalResponse_GetBlockedPlayers both log
	// "Missing pResponseBody" when the body is absent, so the empty collection has to be spelled out.
	"/friends/get-friends-and-status": {body: func(map[string]any, *client) map[string]any {
		return map[string]any{"friends": []any{}}
	}},
	"/friends/get-blocked-players": {body: func(map[string]any, *client) map[string]any {
		return map[string]any{"blockList": []any{}}
	}},
	// InternalResponse_GetFriendInvites (0x66c1d0) reads, in this order: `incoming` at the body
	// root, which must be an OBJECT, then `invites` inside it, which must be an ARRAY of
	// {globalPlayerId, displayName} — then the same for `outgoing`. The outer keys are the
	// directions; `invites` is nested inside each. Getting that inverted made both a console and
	// Ryujinx abandon the session four requests in. It also rejects a display name that is not a
	// full name, per direction ("Incoming --> Bad Full Name"), which empty lists avoid.
	"/friends/get-friend-invites": {body: func(map[string]any, *client) map[string]any {
		return map[string]any{
			"incoming": map[string]any{"invites": []any{}},
			"outgoing": map[string]any{"invites": []any{}},
		}
	}},
	"/friends/subscribe-to-friend-status-updates": {body: func(map[string]any, *client) map[string]any {
		return map[string]any{"blockList": []any{}}
	}},
	"/friends/unsubscribe-to-friend-status-updates": {body: empty},
	"/friends/send-friend-invite":                   {body: empty},
	"/friends/accept-friend-invite":                 {body: empty},
	"/friends/reject-friend-invite":                 {body: empty},
	"/friends/withdraw-friend-invite":               {body: empty},
	"/friends/remove-friend":                        {body: empty},
	// InternalResponse_BlockPlayer and _UnblockPlayer log "Missing globalPlayerID in parameter 1".
	"/friends/block-player": {body: func(in map[string]any, c *client) map[string]any {
		return map[string]any{"globalPlayerId": in["globalPlayerId"]}
	}},
	"/friends/unblock-player": {body: func(in map[string]any, c *client) map[string]any {
		return map[string]any{"globalPlayerId": in["globalPlayerId"]}
	}},

	// The invites this player has been sent, from the store. InternalResponse_SessionsListInvites
	// reads `invites`, each carrying sessionId, inviter, invitee and supportedPlatforms.
	"/sessions/list-invites": {body: func(_ map[string]any, c *client) map[string]any {
		return map[string]any{"invites": c.listInvites()}
	}},

	"/sessions/create": {body: func(in map[string]any, c *client) map[string]any {
		return map[string]any{"session": c.createSession(in)}
	}},

	// The game's own messages to other players, carried without being read.
	"/messaging/send": {body: func(in map[string]any, c *client) map[string]any {
		return c.relay(in)
	}},

	// An update is echoed to the other members as message 2000, or they never learn the chunk the
	// host filled in — which is where the Photon region and session name live.
	"/sessions/update": {body: func(in map[string]any, c *client) map[string]any {
		return map[string]any{"session": c.updateSession(in)}
	}},

	// The invite is recorded against the invitee and pushed to them as message 2007
	// (SessionInviteReceived: sessionId, inviter, type, supportedPlatforms). Without the push their
	// UI never notices, and without the record their next list-invites is empty.
	"/sessions/send-invite": {body: func(in map[string]any, c *client) map[string]any {
		return c.sendInvite(in)
	}},

	// Accepting puts the player in the stored session and tells everyone already in it (2005
	// PlayerJoined), plus the inviter (2010 InviteAccepted). The response carries the session with
	// both players, which is what InternalResponse_SessionsAcceptInvite requires.
	"/sessions/accept-invite": {body: func(in map[string]any, c *client) map[string]any {
		return c.acceptInvite(in)
	}},

	"/sessions/leave": {body: func(in map[string]any, c *client) map[string]any {
		return c.leaveSession(in)
	}},
	"/sessions/close": {body: func(in map[string]any, c *client) map[string]any {
		return c.closeSession(in)
	}},
	"/sessions/reject-invite": {body: func(in map[string]any, c *client) map[string]any {
		return c.rejectInvite(in)
	}},
	"/sessions/withdraw-invite": {body: func(in map[string]any, c *client) map[string]any {
		return c.withdrawInvite(in)
	}},
	"/sessions/kick-player":     {body: empty},
	"/sessions/grant-ownership": {body: empty},

	// InternalResponse_Get logs "Missing Response Body --> Failed" without one. The chunk is the
	// game's own opaque blob; we have never seen a populated one, so it stays empty rather than
	// invented.
	"/live/get": {body: func(in map[string]any, c *client) map[string]any {
		return map[string]any{"chunk": "{}", "dataKey": in["dataKey"]}
	}},
	"/live/put":   {body: empty},
	"/live/patch": {body: empty},

	// Real storage: what the game puts is what it gets back (storage.go). An unwritten slot answers
	// 200 with a well-formed empty object rather than 404 -- see emptySlot for why the 404 this used
	// to send cost every first launch its T17 connection.
	"/storage/get":          {body: func(in map[string]any, c *client) map[string]any { return c.storageGet(in) }},
	"/storage/put":          {body: func(in map[string]any, c *client) map[string]any { return c.storagePut(in) }},
	"/storage/delete":       {body: func(in map[string]any, c *client) map[string]any { return c.storageDelete(in) }},
	"/storage/get-file-url": {body: func(map[string]any, *client) map[string]any { return map[string]any{"url": ""} }},

	"/service/send-report-sns":     {body: empty},
	"/service/trigger-kill-switch": {body: empty},
}
