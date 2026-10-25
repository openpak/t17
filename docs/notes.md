# Notes: t17

## Status: the gate is open, the session protocol is not built

Moving Out 2 (`010065D018172000`) authenticates against this service and opens its websocket.
It is **not playable**: nothing above the socket is implemented yet, so the game connects and
waits. The routes still to build are `/websocket/go-online`, `/player/*`, `/sessions/*`,
`/friends/*`, `/storage/*` and `/live/*` — see [`docs/protocol.md`](protocol.md), which is
the recovered contract and should stay in step with the code.

Beyond that, Moving Out 2 is a **Photon Fusion** title: once the session layer works it hands off
to [`servers/photon`](../../photon). The Fusion AppId is not baked into the game, so it most likely
arrives from `/live/get` — which would mean OpenPak chooses it.

## Why the console's token is ours to verify

`get-token` carries a BAAS `id_token` that **OpenPak signed**, because OpenPak is the BAAS the
console logged into. So this service verifies its own signature (`-jwks-url`, RS256 against the
BAAS JWKS) instead of asking Nintendo to vouch for it — the same reasoning as
[`servers/demonware`](../../demonware). It refuses rather than guessing when the keys cannot be
fetched: that token is what names the player.
