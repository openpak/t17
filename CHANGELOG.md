# Changelog — t17

## v0.8.0 — 2026-09-27

**An empty storage slot answers 404, not 200 with an empty value.**
`OnDownloadRecentlyMetPlayersCallback` (`0x4b73440`, in `Team17.CrossPlay.RecentlyMetPlayers`)
branches on the request's error code: **5** is "nothing stored" and is handled by carrying on with an
empty list, while **0** means the body holds real data and hands it straight to the list parser. The
response parser turns statusCode 404 into error 5 — so answering an empty slot with a 200 and
`value: ""` sends the game down the has-data path with nothing in it. Both platforms were abandoning
the online session immediately after that response.

Routes can now carry a status other than 200, since the client maps 400 to its error 8, 403 to 7 and
404 to 5, and some handlers treat one of those as an ordinary outcome.

## v0.7.2 — 2026-09-27

**The player id was being parsed out of the token wrongly.** `mintToken` builds
`t17-<globalPlayerId>-<base64url hash>`, and the hash routinely contains a dash, so splitting at the
last dash left part of it glued to the id: a session came back with
`"owner":"aaaaaaaaaaaa0002-YIJI930d"` and a display name of
`"aaaaaaaaaaaa0002-YIJI930d#5020"` instead of `<user-b>#8667`. The game created the session, saw an owner
that was not the player, and sent `/sessions/leave` immediately — "unable to create online session"
on the console and "you have been disconnected from the session" on Ryujinx. Split at the first dash:
the id is hex and contains none.

## v0.7.1 — 2026-09-26

**Fixes a v0.7.0 regression: the friend-invite directions are the outer keys.**
`InternalResponse_GetFriendInvites` (`0x66c1d0`) reads `incoming` at the body root as an object,
then `invites` inside it as an array, then the same for `outgoing`. v0.7.0 had it inverted — an
`invites` object holding the two directions — and both a real console and Ryujinx abandoned the
session four requests in, where v0.6.0 had carried them past six.

## v0.7.0 — 2026-09-26

The client now runs the whole online flow — go-online, friend invites, session invites, storage,
friends, platform accounts — and stops at `/sessions/create`. Three shapes were wrong, all three
read out of the handlers:

- **A session's id key is `id`, not `sessionId`.** `InternalResponse_SessionsCreate` (`0x685740`)
  reads `session` → `players` → `id` → `closedToNewPlayers` → `membersCanInviteOthers` → `type` →
  `owner` → `maxPlayers` → `supportedPlatforms`, and a missing `id` is its "Bad SessionID!" path.
  Both keys are sent now, since the invite structures use `sessionId`. The type must be `game` or
  `group` — anything else is "Unknown SessionType".
- **`players` entries are full players.** The parser (`0x696370`) wants `globalPlayerId`,
  `displayName`, `platformId`, `platformPlayerId`, `deviceId`, `gameId`, `connected`, `chunk`,
  `linkedAccounts`; one it cannot parse fails the entire session response.
- **`invites` is an object of `incoming` and `outgoing`**, not an array
  (`InternalResponse_GetFriendInvites`, `0x66c1d0`).
- **`/player/get-platform-accounts` now resolves ids** instead of answering an empty list. This is
  how the game turns the console's friend list into T17BE players, so an empty answer leaves every
  friend unresolvable and uninvitable. A Switch player's `globalPlayerId` is their NSA id — ours is,
  from get-token — so the mapping is the identity one.

## v0.6.0 — 2026-09-26

**A socket reply must not be wrapped in `messages`.** The websocket receive path (`f_6933f0`, the
entry `BE_Transport_WebSocket_OnReceive` calls) parses the frame and hands the **root** to
`__ParseJSONObject`, which reads `action` there. The `messages` array belongs to `__ParseJSONBlob`,
which serves the HTTP path. Our wrapped reply left the root with no `action`, so every answer was
dropped with `no ACTION is message --> Ignoring` — silently.

That was the whole remaining blocker: `BE_IsReady` is `ldrb w0, [x0, #0xb1]; ret`, the byte only
`InternalResponse_GoOnline` sets, so the C# polled it for 60 s and hung up. Which is exactly what
the 2026-09-26 20:32 run did, 67 seconds after going online.

## v0.5.0 — 2026-09-26

**The whole route surface, implemented from the decompiled client rather than discovered one game
launch at a time.** All 707 functions in T17BE's native library were decompiled; every route, its
request fields and its response fields are in `docs/protocol.md` with the addresses, named by the
binary itself — 40 routes, 32 request builders, 20 response handlers.

- `cmd/t17d/routes.go` answers all 40, with the fields each handler refuses to work without:
  `session` (type, sessionId, owner, maxPlayers) for `/sessions/create`, `friends` /`blockList` /
  `invites` for the collection routes, a `chunk` for `/live/get`, `globalPlayerId` + a full
  `displayName` (with its `#`) for `/player/get-account`.
- An unknown action now gets a 200 with an empty body and a log line, instead of silence.

## v0.4.0 — 2026-09-26

The reply shape is now read out of the client's own parsers instead of inferred, and it was wrong
in three ways at once — each enough on its own for every answer to be silently discarded:

- **`action` must be the literal `"response"`.** `__ParseJSONObject` (`0x693af0`) switches on it and
  accepts only `response`, `message` or `multipart`; we echoed the request's path, which logs
  "Unknown Action Type --> Ignoring".
- **`statusCode` belongs at the element root**, as a number, not inside `headers`.
- **`request-id` must stay a JSON number** inside `headers`. It is read with an integer getter, and
  decoding the incoming headers into `map[string]string` also meant a numeric request-id failed to
  decode and the entire frame was ignored.

Also removed two experiments that would break a session that works: the unprompted
`{"messages":[]}` greeting (an empty array reaches the branch that marks a pending request
complete) and the close frame after three unanswered pings. The idle ping stays.

None of this was why the socket sits silent: the client never reads what we send, measured through a
stuck `Recv-Q`. A hardcoded `0` from Ryujinx's `nn::ssl` `Pending` is the leading candidate for that
and is now fixed in the fork, but it is **not confirmed** — Eden implements the same call as
`SSL_pending()`, which would also be zero here. The fork now traces every `nn::ssl` command so one
run settles it. See `docs/protocol.md`.

## v0.3.1 — 2026-09-26

- Log the client's upgrade request (`Sec-WebSocket-Version`/`-Protocol`/`-Extensions`), which we
  had never read back, and **echo the first offered subprotocol**. RFC 6455 §4.1 lets a client
  that offered subprotocols and got none back treat the connection as failed — while leaving it
  open, which is indistinguishable from our symptom.
- The ttl cap in v0.3.0 was **not** the blocker: with `ttl` 3600 the 2026-09-26 05:11 UTC run
  behaved identically. Recorded so it is not re-tried.

## v0.3.0 — 2026-09-26

- **The auth expiry must be at most 10800 seconds.** `T17BE_Transport::Initialise` checks
  `((authExpirySeconds-1)>>4) < 0x2a3`, and on failure logs "Cannot initialise Base Class -->
  Bailing!!" and returns false. A failed Initialise leaves the transport's "active" byte (`+8`)
  clear, and its `Update` (`0x691db0`) returns immediately while that byte is zero — so the core
  pumps no requests and the client holds an open websocket without ever sending one. Our `ttl` was
  12 hours, an arbitrary choice, four times over the limit. Now one hour, which also stays under
  the reconnection-time check's 5400 s.

## v0.2.0 — 2026-09-26

- socket: greet a new connection with an empty `messages` envelope, the smallest well-formed thing
  a server can say — `__ParseJSONBlob` looks for that key. If the transport waits to be greeted
  before it finalises the connection, this is what unblocks it.
- socket: after three unanswered pings, send a **close frame**. RFC 6455 requires a live reader to
  echo a close, so this settles read-versus-not-reading without assuming the SDK answers pings —
  which it may not, and which the earlier "no pong" reading leaned on too heavily.

## v0.1.1 — 2026-09-26

- `-jwks-host` / `T17_JWKS_HOST`: nx-baas routes by hostname and answers the BAAS surface only for
  its own names, so fetching the keys from the container address needs the BAAS name on the Host
  header or the request lands in its catch-all 404.

## v0.1.0 — 2026-09-26

- First release. Serves the three T17BE tiers from one process, dispatching on hostname:
  the directory, `auth/get-token`, and the `/socket` websocket.
- `get-token` verifies the console's BAAS `id_token` itself (RS256 against the BAAS JWKS) and
  answers the twelve fields of the managed response class, with the types it declares.
- `displayName` is given a stable `#NNNN` tag when it has none — without the separator T17BE fails
  its whole init through a path that logs nothing.
- The websocket accepts the upgrade, logs every frame, pings an idle connection to tell a live
  client reader from a dead one, and answers any `action` with a `messages`/`headers` envelope.
- Extracted from the capture spike that lived in `nx-baas/t17.go`; the recovered protocol is
  `docs/protocol.md`.
