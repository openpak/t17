# T17BE — the protocol, as recovered

Read out of **Moving Out 2** (`010065D018172000`, update v327680 = 1.3.315) on 2026-09-25, partly
from captured traffic and partly by disassembly. Addresses are that build's. The research trail,
including the tooling that made the C# readable, is in `nx-baas/docs/t17-backend.md`.

`T17BE` is a native C++ SDK statically linked into the game (`D:/work/…/core/source/T17BE_*.cpp`)
with a C# layer in front of it (`Team17.CrossPlay.Core`, `Team17.CrossPlay.Auth`,
`Team17.Nintendo.WebSocket`). The websocket is `NintendoSDK_WebSocket`, a static library — not a
firmware service, so an emulator needs nothing extra for it.

## 1. The directory

```
GET directory.<host>/
  Api-Title: ProjectJourney   Api-Platform: nxswitch   Api-Tier: dev   Api-Version: 1

200 {"game":{"url":"https://game.<host>/"},"xplayurl":"https://xplay.<host>/prod/",
     "AllowXPlay":"1","ga":{"ga":"0"}}
```

Team17's own service answers this with an **empty** `game.url` (`http:///`) for this title and
tier, which is why the retail game fails against the live backend. Because every later request
goes to a host named here, answering this one is enough to point a title at OpenPak — no need to
redirect the AWS API Gateway it otherwise names.

## 2. get-token

```
POST <xplayurl>auth/get-token
{"coreVersion":"1.0.6.0.0","platformId":"nintendo","deviceId":"nintendo-switch",
 "gameId":"ProjectJourney","authType":"nintendo-switch","displayName":"…",
 "nintendoSwitchAccessToken":"<JWT>"}
```

The JWT is the console's BAAS id_token — **one OpenPak signed** (`kid: openpak-baas-key-1`, `sub`
= the BAAS user id), so this service verifies its own token rather than asking Nintendo.

**There are two JSON contracts, and this reply answers the managed one.** `BE_PreInitialise` is a
P/Invoke stub handing native a single string, and `GetT17AuthJson` **rebuilds** that string from
the C# object — so the native SDK never sees this HTTP body. The reply must match the response
class in `Team17.CrossPlay.Auth`: twelve fields, with the types it declares.

| field | type | | field | type |
|---|---|---|---|---|
| `websocketUrl` | string | | `role` | string (client/server/service) |
| `token` | string | | `blockList` | **array** |
| `exp` | int32, unix seconds | | `version` | string |
| `ttl` | int32, seconds | | `deviceId` | string |
| `displayName` | string | | `platformId` | string |
| `globalPlayerId` | string | | `killSwitchActive` | bool |

The deserialiser is `JsonUtility.FromJsonOverwrite` (`WebRequest::UpdateWaitForResponse`,
`0x4c0fefc`), which **ignores unknown keys and defaults missing ones but throws on a type
mismatch** — and that throw kills the auth step with no diagnostic. So an extra key is harmless
and a wrong type is fatal. `blockList` is an array *here* even though the native side parses it as
a stringified JSON chunk; that chunk is the one C# rebuilds.

**`displayName` must contain `#`.** T17BE splits `displayNameFull` at `'#'` to derive the short
name, and the split helper (`0x6975a0`) returns **false** when the separator is absent, failing
`PreInitialise` via "Problem splitting the displayNameFull into short!!" — a path with no error
string of its own. A name without a tag can never work, whatever else is correct.

**The ttl has a hard ceiling of 10800 seconds (3 hours).** `T17BE_Transport::Initialise` checks
`((authExpirySeconds-1)>>4) < 0x2a3`; over that it logs "Cannot initialise Base Class --> Bailing!!"
and returns false. The failure is silent from outside and its shape is distinctive: a failed
`Initialise` leaves the transport's active byte at `+8` clear, and `Update` (`0x691db0`) returns
immediately while that byte is zero, so the client **holds the websocket open and never sends a
single request**. A separate reconnection-time check (`((x-1)>>3) < 0x2a3`, ≤ 5400 s) only warns and
forces the default.

### The native contract, for reference

`T17BE_Transport::PreInitialise` (`0x68f2c0`) parses the string C# hands it and looks up keys in a
fixed order, twice — a validation pass that names what is missing, then a fill pass that does not:

```
websocketUrl token exp ttl displayName globalPlayerId role blockList deviceId platformId
… then, fill pass only: version
```

`version` has no "Backend Missing required" string, so omitting it fails silently. `exp`/`ttl` are
read as 32-bit numbers; `version` is copied as a string; `blockList` is handed to the same JSON
parser as the whole blob, so there it is a **string** containing JSON.

## 3. The websocket

`wss://game.<host>/socket?token=<token>` — the token rides as a query parameter. The client offers
`Sec-WebSocket-Extensions: permessage-deflate`; declining it is legal and it then must not
compress.

Client → server:

```json
{"action": "/websocket/go-online",
 "headers": {"request-id": "…", "Authorization": "Bearer <token>"},
 "body": { … }}
```

Server → client: a `messages` array — `__ParseJSONBlob` looks for that key — and every message
needs a `headers` object (`__ParseJSONObject --> No Header for Response - REQUIRED FOR
RESPONSES!!`). `statusCode` inside the headers is what `InternalResponse_GoOnline` and its
siblings gate on.

```json
{"messages": [{"headers": {"request-id": "…", "action": "…", "statusCode": 200}, "body": { … }}]}
```

### Route keys

```
/player/get-account  /player/get-platform-accounts  /player/update-display-name
/sessions/create|update|close|leave|kick-player|grant-ownership
/sessions/send-invite|accept-invite|reject-invite|withdraw-invite|list-invites
/friends/send-friend-invite|accept-friend-invite|reject-friend-invite|withdraw-friend-invite
/friends/remove-friend|get-friend-invites|get-friends-and-status
/friends/subscribe-to-friend-status-updates|unsubscribe-to-friend-status-updates
/friends/block-player|unblock-player|get-blocked-players
/storage/get|put|delete|get-file-url        /live/get|put|patch
/websocket/go-online|ping|start-reconnection|complete-reconnection
/service/send-report-sns|trigger-kill-switch     auth/renew-token
```

Reconnection: `start-reconnection` yields a `reconnectionToken`, then `complete-reconnection`.
Re-auth replaces the `token`.

## Open

The socket opens and then **the client never reads it.** Measured 2026-09-26 against the real
console: the socket opened at 04:46:02, this service pinged at 04:46:22 and again at 04:46:42, both
writes succeeded, and the client neither ponged nor closed. RFC 6455 requires a pong, and
`NintendoSDK_WebSocket` is conformant, so its receive path was never started.

**That means implementing the routes is premature.** Answers would go unread. The gap is in the
client's transport initialisation, not in the protocol above the socket.

Where to look next. The boot sequence that should run is
`Get Block List → Go Online → Get Essentials → READY` (`T17BE_System::__BootUp`, strings at
`0x68f038`+). `OnNewWebsocketCreated` (`0x6932b0`) only acts when the transport state — the word at
`+0x94` — is **3**, setting it to 4 on success and 6 on failure; that is the *reconnect* path
("recreate on AppSide"), so the first-connect path differs and its trigger is unidentified. The C#
side reaches its own "connected" state 2 (`NintendoSwitch::MoveNext`, `0x4c065d0`) after
`ConnectAsync` returns and a virtual check passes — it does not dispose the socket, so it believes
the connection is up. Something between that and native's read loop does not fire.

Worth checking first: whether native ever registers its receive callback
(`BE_Transport_WebSocket_OnReceive`, `Core::BE_Transport_WebSocket_RegisterSendViaWebsocketCallback`
and the `RegisterCB_*` family), and whether `T17BE_Transport::Initialise` — which bails with
"Missing Auth Token" and warns on "Bad Reconnection Time" — completed at all. Its init parameters
are logged one per line by the SDK (`Initialise PARAM --> authToken/globalID/deviceID/
displayNameFull --> %s`), which is the shape of an SDK that expects an operator to have a log sink;
`pLogFilename` is one of those parameters, and nothing appears under Ryujinx's save data, so the
game passes no filename.

Also unverified: whether `/live/get` carries the Photon **Fusion AppId**. No AppId is baked into
the build — `PhotonAppSettings` holds none and the C# has `CustomPhotonAppSettings` — so if it
arrives from this backend, OpenPak chooses it and `servers/photon` can accept whatever we hand out.

## The websocket message contract (read out of the client's parsers, 2026-09-26)

Every address below is a module vaddr in Moving Out 2 1.3.315's `main`. This replaces everything
this document previously inferred about the reply shape; the earlier version was wrong in three
separate ways and each one alone was enough for the client to silently discard our answers.

A frame from us must be an object with a `messages` array (`__ParseJSONBlob`, `0x6930f0`). Each
element is dispatched by `__ParseJSONObject` (`0x693af0`), which reads the element's **`action`** and
accepts **exactly three literals**:

| `action` | handled by | meaning |
|---|---|---|
| `response` | `0x693cf0` | the answer to a request the client sent |
| `message` | `0x694180` | a server push |
| `multipart` | — | a chunked payload |

Anything else — including the request's own path, which is what we used to echo — logs
`T17BE_Transport :: __ParseJSON --> Unknown Action Type --> [%s] --> Ignoring` and is dropped.

For `action: "response"` (`0x693cf0`):

- `headers` — an object, and it must hold **`request-id` as a JSON number**. Read with the integer
  getter; a string is a miss and the whole element is discarded. This is how the reply is matched to
  the pending request, so a dropped one is indistinguishable from no answer at all.
- `statusCode` — a JSON number, **at the element root, not inside `headers`**.
- `body` — optional. Absent, the client zeroes its own body struct.

The status is then interpreted: `200` is the success path, `400` becomes its internal error 8, `403`
becomes 7, `404` becomes 5, and **`>= 500` or `429` fails the request outright**. A separate
`statusCode != 200` check in `__ParseJSONBlob` marks the whole blob's request failed.

For `action: "message"` (`0x694180`) the element carries `messageSystemId`, `messageType`,
`messageProtocolVersion`, `from` and `body` (or `message`); a missing body logs
`No Body - REQUIRED FOR MESSAGES!!!` and a `from` on the block list is ignored.

**Do not send anything unprompted.** An empty `messages` array reaches the branch that marks a
pending request complete.

## Why the socket sits in silence — measured, and still open

**Measured.** The client writes its upgrade request and then never reads a byte: `ss -tnp` on the
console's host showed `Recv-Q` stuck at 264 bytes for ten minutes, and Ryujinx's `Read` drains the
socket whenever it is called, so it is not being called. Until that changes, nothing this server
sends can matter — which is why the ttl ceiling, the `blockList` type, the subprotocol echo and the
greeting all changed nothing.

**The leading candidate, not yet confirmed.** Ryujinx's `SslManagedSocketConnection.Pending()`
returned a hardcoded `0`, and `nn::ssl::Connection::Pending` is one of the two ways a non-blocking
client asks whether there is anything to read (`Peek` is the other, and it is stubbed to
`WouldBlock` on purpose for Nintendo's curl). A client gated on it would never read. Against that
reading: Eden implements the same call as `SSL_pending()`, which reports **plaintext already
decrypted and not yet read** — also zero in this situation — so if the client were gated on strict
`Pending` semantics it would stall on hardware too, and it does not.

The fork now answers `Pending` truthfully (1 when the socket has data or plaintext may be buffered)
and, more usefully, traces every `nn::ssl` command on a traced host, including the `WouldBlock`
reads and `DoHandshakeGetServerCert` that were previously invisible. One run says definitively which
call the client is waiting on. Do not treat `Pending` as the answer until that log exists.

## The complete route surface (recovered 2026-09-26, not inferred)

All 707 functions in T17BE's native library were decompiled (`scratch/mo2/decomp/all`, Ghidra
headless via `pyghidra`). The library spans module vaddr `0x66a3f8`-`0x6977c8` across eight source
files — `T17BE_{Transport,System,MainSystem,FriendSystem,SessionSystem,StorageSystem,LiveDataSystem,UID}.cpp`
— plus its own websocket at `0x69e000`. Every function below names itself in its log strings, so
these are the game's names, not ours. **40 routes, 32 request builders, 20 response handlers.**

Request fields are the keys the builder writes; response fields are the keys the handler reads.

| Route | Builder | Request fields | Response fields |
|---|---|---|---|
| `/websocket/go-online` | `Transport::InternalRequest_GoOnline` `0x6952f0` | `chunk` | — status only |
| `/websocket/ping` | `Transport::InternalResponse_Ping` `0x692740` | `description` | — |
| `/websocket/start-reconnection` | `Transport::InternalRequest_StartReconnection` `0x692840` | — | `reconnectionToken` (required) |
| `/websocket/complete-reconnection` | `Transport::InternalRequest_CompleteReconnection` `0x692920` | `reconnectionToken` | — |
| `/auth/renew-token` | `Transport::__TryReAuth` `0x692140` | — | `AuthToken` (required) |
| `/player/get-account` | `MainSystem::InternalRequest_GetPlayerAccount` `0x67cd30` | `platformId` | `globalPlayerId`, `displayName` |
| `/player/update-display-name` | `MainSystem::InternalRequest_UpdateDisplayName` `0x67cc00` | `newDisplayName` | `newDisplayName` (must be a *full* name) |
| `/player/get-platform-accounts` | `MainSystem::InternalRequest_BatchGetPlayerGlobalIDList` `0x67d6d0` | `platformId`, `platformPlayerIds` | `globalPlayerId`, `platformPlayerId` |
| `/service/send-report-sns` | `MainSystem::InternalRequest_SendReportSNS` `0x67d520` | `subject`, `message` | — |
| `/service/trigger-kill-switch` | `MainSystem::InternalRequest_TriggerKillSwitch` `0x67d3c0` | `gameId`, `platformId`, `killState` | — |
| `/friends/get-friends-and-status` | `FriendSystem::InternalRequest_GetFriendsAndStatus` `0x66e5c0` | — | `friends` (body required) |
| `/friends/get-friend-invites` | `FriendSystem::InternalRequest_GetFriendInvites` `0x66f2a0` | `incomingPaginate`, `outgoingPaginate` | `invites[]` of `globalPlayerId`, `displayName` |
| `/friends/get-blocked-players` | `FriendSystem::InternalRequest_GetBlockedPlayers` `0x66e7b0` | `paginate` | body required |
| `/friends/subscribe-to-friend-status-updates` | same builder | — | `blockList` |
| `/friends/block-player`, `/friends/unblock-player` | same builder | — | `globalPlayerID` (required) |
| `/friends/send-friend-invite` | — | — | `globalPlayerId`, `displayName` |
| `/friends/withdraw-friend-invite` | `FriendSystem::InternalRequest_WithdrawInvite` `0x66f190` | `inviteeGlobalPlayerId` | — |
| `/friends/{accept,reject}-friend-invite`, `/friends/remove-friend`, `/friends/unsubscribe-to-friend-status-updates` | — | — | status only |
| `/sessions/create` | `SessionSystem::InternalRequest_SessionsCreate` `0x68aa10` | `game`, `group`, `type`, `chunk`, `maxPlayers`, `membersCanInviteOthers`, `closedToNewPlayers`, `supportedPlatforms` | `session` with `players`, `owner`, `maxPlayers`, `membersCanInviteOthers`, `closedToNewPlayers`, `chunk`, `game`, `group`, `supportedPlatforms` |
| `/sessions/update` | `…SessionsUpdate` `0x6897d0` | `sessionId` + the create fields | as create |
| `/sessions/accept-invite` | `…SessionsAcceptInvite` `0x68a270` | `sessionId`, `inviterId` | `session` details (required) |
| `/sessions/list-invites` | `…SessionsListInvites` `0x68a110` | — | `invites[]` of `sessionId`, `inviter`, `invitee`, `supportedPlatforms` |
| `/sessions/send-invite`, `/withdraw-invite` | `…SessionsSendInvite` `0x689d90`, `…WithdrawInvite` `0x689f60` | `sessionId`, `inviteeId` | — |
| `/sessions/reject-invite` | `…SessionsRejectInvite` `0x68a7b0` | `sessionId`, `inviterId` | — |
| `/sessions/kick-player`, `/grant-ownership` | `0x689bf0`, `0x68a560` | `sessionId`, `globalPlayerId` | — |
| `/sessions/leave`, `/sessions/close` | `0x68a420`, `0x689ab0` | `sessionId` | — |
| `/live/get` | `LiveDataSystem::InternalRequest_Get` `0x6788b0` | `dataKey`, `globalPlayerId` | body required ("Missing Response Body") |
| `/live/put`, `/live/patch` | `0x678fb0`, `0x6790e0` | `chunk` | — |
| `/storage/get`, `/put`, `/delete` | `0x68d430`, `0x68d720`, `0x68da60` | `dataRole` (`client`/`server`/`service`), `platformId`, `gameId`, `globalPlayerId`, `slot`, `value` on put | — |
| `/storage/get-file-url` | `0x68dd30` | the storage fields plus `method` (`GET`/`PUT`/`DELETE`) | — |

**Required fields are enforced and each one announces itself.** `InternalResponse_SessionsCreate`
has a separate log line for an empty session type, a bad `maxPlayers`, a bad `sessionId` and a bad
`sessionOwner`. `InternalResponse_GetFriendsAndStatus` and `_GetBlockedPlayers` log
`Missing pResponseBody`; `LiveDataSystem::InternalResponse_Get` logs `Missing Response Body`;
`_BlockPlayer`/`_UnblockPlayer` log `Missing globalPlayerID in parameter 1`. A display name that
comes back without a `#` fails the full-name split — `_GetFriendInvites` rejects it per direction
("Incoming --> Bad Full Name").

**Server pushes** use `action: "message"` and go to `MainSystem::InternalRequest_MessageSend`'s
counterpart: `messageProtocolVersion`, `messageSystemId`, `messageType`, `message`, and a target of
`to` / `globalPlayerId` / `globalPlayerIds` / `sessionId`, with `exclude` for broadcast.

### The core's own state machine (`0x68eff0`)

```
0 -> 1 -> sends /websocket/go-online -> 2
2  waits on core+0xb1 (set by the go-online 200), then core+0xa0 == 1 ? 3 : 5
3  initialises Friend (/friends/get-friend-invites) and Session (/sessions/list-invites) -> 4
4  waits on core+0x1be and core+0x25a (their ready flags) -> 5
5  ONLINE: the queued-request flush in 0x6925d0 runs only in this state
```

`Core::Update` (`0x690fc0`) returns immediately unless `core+0` is set, and
`Transport::Update` (`0x691db0`) needs both its active byte at `+8` **and** a websocket pointer at
`+0x10` — which `Initialise` takes as an argument and stores via `0x68ecc0`.
