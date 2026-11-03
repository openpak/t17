# Deploy: t17

Plain HTTP on **22630** inside the pod network (health on **22631**); Traefik terminates the
guest's TLS for the three names, the same arrangement as the Among Us front and
[`platforms/switch/games/battlenet`](../../battlenet).

## Build and test

```sh
go build ./...
go test ./...
```

A `v*.*.*` tag publishes `ghcr.io/openpak/t17:latest` and deploys, like every other OpenPak
service.

## Rewards sessions

Set `REWARDS_EVENTS_URL` (e.g. `http://openpak-rewards:20180`) and `REWARDS_EVENTS_KEY` (or `REWARDS_EVENTS_KEY_FILE`) to report
`game_sessions.moving-out-2` events to the rewards service; unset, the reporter is off. `REWARDS_SESSION_MIN` (default `3m`) is how long a
player must share a room with at least one other player. A room is a T17BE session; only members with a live websocket count, and the player is the BAAS user id from get-token's id_token.
