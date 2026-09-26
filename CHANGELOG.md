# Changelog — t17

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
