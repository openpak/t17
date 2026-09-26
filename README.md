# t17 — OpenPak's Team17 backend (T17BE)

The service a Team17 Switch title reaches when a console is redirected to OpenPak. One process
serves all three tiers a T17BE title asks for, dispatching on hostname:

| Host | What it answers |
| --- | --- |
| `directory.t17service.com` | the addressing — where the game and crossplay tiers live |
| `xplay.t17service.com` | `POST /prod/auth/get-token`, from the console's own BAAS id_token |
| `game.t17service.com` | `/socket` — one websocket carrying every other operation |

Plain HTTP on **22630** inside the pod network (health on **22631**); Traefik terminates the
guest's TLS for the three names, the same arrangement as the Among Us front and
[`servers/battlenet`](../battlenet).

## Status: the gate is open, the session protocol is not built

Moving Out 2 (`010065D018172000`) authenticates against this service and opens its websocket.
It is **not playable**: nothing above the socket is implemented yet, so the game connects and
waits. The routes still to build are `/websocket/go-online`, `/player/*`, `/sessions/*`,
`/friends/*`, `/storage/*` and `/live/*` — see [`docs/protocol.md`](docs/protocol.md), which is
the recovered contract and should stay in step with the code.

Beyond that, Moving Out 2 is a **Photon Fusion** title: once the session layer works it hands off
to [`servers/photon`](../photon). The Fusion AppId is not baked into the game, so it most likely
arrives from `/live/get` — which would mean OpenPak chooses it.

## Why the console's token is ours to verify

`get-token` carries a BAAS `id_token` that **OpenPak signed**, because OpenPak is the BAAS the
console logged into. So this service verifies its own signature (`-jwks-url`, RS256 against the
BAAS JWKS) instead of asking Nintendo to vouch for it — the same reasoning as
[`servers/demonware`](../demonware). It refuses rather than guessing when the keys cannot be
fetched: that token is what names the player.

## Two things that will cost you an evening

- **`displayName` must contain `#`.** T17BE splits it to derive a short name and *fails the whole
  init* when the separator is missing, through a path with no error string. The symptom is
  authentication succeeding and then silence — no socket, no diagnostic.
- **There are two JSON contracts.** The native SDK's field list describes the string the game's own
  C# builds for it, not this service's HTTP reply. Match the reply to the managed class; matching
  it to the native list is what makes `blockList` look like a string when it must be an array.

## Build and test

```sh
go build ./...
go test ./...
```

A `v*.*.*` tag publishes `ghcr.io/openpak/t17:latest` and deploys, like every other OpenPak
service.
