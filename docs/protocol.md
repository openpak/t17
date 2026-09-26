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
