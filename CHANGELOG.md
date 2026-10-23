# Changelog — t17

> **Current Switch client (2026-10-13): Harbor**, at
> `~/REPOS/Openpak/emulators/harbor`. Harbor is an independent
> OpenPak project based on Eden. Citron, Ryujinx (desktop and Android), and
> the former OpenPak Eden checkouts have been removed. References to those
> forks below record historical evidence only: do not run their commands,
> reinstall them, or select them for current work. Use Harbor's README and
> current handoff for builds and testing; old results do not establish Harbor parity.

## v0.11.5 — 2026-10-10

**The log carries no personal data.** Player ids, display names, session ids, client addresses and
websocket frame text are gone from every log line. Where two lines have to be matched up they carry
a `~xxxxxx` tag (a keyed hash, new for each process start). The HTTP server's own error log has addresses scrubbed. Error text drops the addresses and URLs
that net errors spell out, and request paths and unknown routes keep their shape but tag any segment
that is not a plain route word. The websocket frame dump is off; `T17_TRACE_BODIES=1` turns it back on
for a bench run (never set it in production). Behaviour is unchanged; `TestLogsCarryNoPersonalData`
drives sign-in, two sockets, sessions, invites and relays and fails if a sentinel id, token, name,
address or query reaches the log.

## v0.11.4 — 2026-09-28

**A player's Nintendo account goes in `linkedAccounts`.** Two players are in a session together for
the first time, and the host drew the joiner with no avatar. Read from the host's own trace: it asked
the console's friends service for the profile of account `0000000000000000`. The game takes a session
player's platform account from `linkedAccounts` (`{platformId, platformPlayerId}`) and nowhere else,
and we sent that list empty.

## v0.11.3 — 2026-09-28

**A player's platform goes inside `liveData`.** The invite flow now completes — request, invite,
accept — and the joiner then waited on "joining crew" for ten seconds and left. It was waiting for
the host's join confirmation, which the host sends from its player-joined handler. Read from the
host: the player-joined event did fire, and its first listener threw
`ArgumentNullException: Value cannot be null. Parameter name: key` in
`CrossPlayAnalyticsService.OnPlayerJoined` → `IncreaseTrackingReferenceForPlatform(platform)`, so the
session manager's listener behind it never ran.

The platform was null because the player parser (`0x696370`) reads `platformPlayerId`, `platformId`,
`gameId`, `deviceId` and `chunk` from a `liveData` object inside the player, and we sent them at the
player's own level. Every player object now carries `liveData`.

## v0.11.2 — 2026-09-27

**Session pushes are addressed to `SESN`, not `2`.** Read from the joiner itself this time: its
`RequestXPlaySessionInviteFromPlatformSessionTask` asked for an invite, our `SessionInviteReceived`
push reached its socket 120 ms later, and the task failed on its ten-second timeout anyway. The
client's message arm decides whether a push belongs to one of its own systems by comparing
`messageSystemId` with four names — `FRND`, `SESN`, `CORE`, `SRVC` (`0x691210`) — and hands anything
else to the game as an application message. We sent `"2"`, the session system's number in the request
factory, so **no session push this server ever sent was seen by the session system**.

## v0.11.1 — 2026-09-27

**A person in a session push is a whole player, not an id.** With the relay in place the join got
one step further: the host answered the joiner's announcement with `/sessions/send-invite`, we
pushed `SessionInviteReceived` (2007) to the Switch — and the Switch never accepted it. The session
dispatcher (`0x6821a0`) runs `inviter` through the same player parser a session's `players` use
(`0x696370`) and drops the message on failure ("Cannot parst Inviter JSON"); we sent a bare id.

Fixed wherever that dispatcher reads a person: `inviter` in 2007 and 2009, `invitee` in 2008, 2010
and 2011, the player's fields at the payload root of `SessionPlayerJoined` (2005), and `inviter` in
the `/sessions/list-invites` reply (`0x686650`).

## v0.11.0 — 2026-09-27

**`/messaging/send` is relayed.** With hosting fixed, a Switch that accepted an invite failed with
"unable to join online session". Its log here shows why: it sent `/messaging/send` — system `XPSM`,
type 1, `{"m_t17SenderId", "m_sessionId"}`, addressed to the host — got our empty 200 for a route we
did not have, and gave up 36 seconds later. The host was never told.

The message now goes to whoever `to` names (`globalPlayerId`, `globalPlayerIds`, or everyone in a
`sessionId`, less `exclude`) as an `action: "message"` push carrying the sender's own
`messageSystemId`, `messageType` and `messageProtocolVersion`, with `from` set and `message` left
exactly as sent. The client's parser (`0x694180`) hands a message for a system it does not own to the
game as a string, so it is relayed as one and never interpreted here.

## v0.10.1 — 2026-09-27

**A global player id is a GUID, because the game parses it as one.** Hosting an online session in
Moving Out 2 failed on a console and on Ryujinx alike with "unable to create online session", about a
second after the Fusion runner started. Read out of the running game: the runner starts
(`StartGameResult: Ok:True`), then `GameLobby.InternalAttachGameSessionAsync` calls
`Uid.Parse(T17SessionManager.LocalCrossPlaySessionUser.T17GlobalPlayerID)` — `Guid.Parse` — and
throws `FormatException: Guid should contain 32 digits with 4 dashes`. The game logs
`AttachGameSessionAsync() failed: AttachmentResult=Failure`, shuts its runner down and leaves the
session it had just created.

We handed out the 16-digit Nintendo account id as the global id. It is now that id left-padded with
zeros to 32 digits, undashed so the token still cuts at its first dash. `platformPlayerId` stays the
account id, `/player/get-platform-accounts` maps one to the other, and the display-name tag is still
taken from the account id so nobody's name changes.

## v0.10.0 — 2026-09-27

**Player storage is real, and an unwritten slot is not a 404.** `/storage/put` remembers a value and
`/storage/get` gives it back, keyed by dataRole + gameId + globalPlayerId + slot. The blob is the
game's own (base64 of its LitJSON); we hold it verbatim and never interpret it.

The 404 an empty slot used to answer is what cost Moving Out 2 its T17 connection on **every first
launch**: measured in its own trace, the game read the 404 and disposed of the socket 100 ms later,
putting up "An error has occurred connecting to T17 services." The launch after — whose T17BE init is
already past the storage step — worked, which is the "only ever works the second time" everyone kept
hitting. `OnDownloadRecentlyMetPlayersCallback` does treat 404 as "nothing stored", which is why the
404 looked right, but the native `T17BE_StorageSystem` boot fetch does not agree.

An unwritten slot now answers 200 with base64 of `{}` — a well-formed empty object for the game's own
parser. The build before last answered 200 with `value: ""`, which is empty rather than well-formed and
sent the game down its has-data path with nothing in it; a valid empty object is neither that nor a 404.

## v0.9.1 — 2026-09-27

**Session ids are unique per session, not per player.** The client names its Photon room after the
session id, so `s-<ownerId>` meant every attempt rejoined the previous attempt's room — the Photon
server logged `EnterRoom "s-aaaaaaaaaaaa0001" actor=2 occupants=[2]`, the client landed in a dead
Fusion simulation whose host had already left, ran for a few seconds and gave up with "unable to
create online session".

Measured alongside this: the Photon chain itself already works. The client authenticates, joins the
room named after the T17 session, is handed to the game server, enters as an actor and exchanges
Fusion events with our `FusionHost` — so T17 session → Photon Fusion room is wired end to end.

## v0.9.0 — 2026-09-27

**Real sessions, real invites, and server pushes.** Sessions were derived per connection and invite
lists were always empty, so an invite had nowhere to land: the host created a session, waited ~13 s
for a confirmation that could never arrive, and left. Now the server remembers sessions and invites
and tells the other player about them.

- `sessions.go` holds sessions and invites in memory; `conn.go` registers each live websocket against
  its player so a push can reach it from whichever connection caused it.
- `/sessions/send-invite` records the invite and pushes **message 2007** (SessionInviteReceived:
  sessionId, inviter, type, supportedPlatforms) to the invitee, plus 2008 to the inviter.
- `/sessions/accept-invite` puts the player in the stored session, pushes **2005** (PlayerJoined) to
  everyone already there and **2010** (InviteAccepted) to the inviter, and answers with the session
  carrying **both** players.
- `/sessions/list-invites` answers from the store; `/sessions/update` pushes **2000** so the other
  members learn the chunk the host filled in — which is where the Photon region and session name
  live; leave/close/reject/withdraw push 2004/2001/2011/2009.

The message numbers come from the session dispatcher's jump table (`0x6821a0`: it subtracts 2000 and
allows twelve cases, the same ids the request builders use). The push envelope is what `0x694180`
reads: `action: "message"` at the frame root, then `body` holding `messageSystemId` (a **string**),
`messageType` (a number), `from`, `messageProtocolVersion`, and the payload under `message` — a body
without an inner `message` is refused outright.

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
