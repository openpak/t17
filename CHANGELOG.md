# Changelog — t17

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
