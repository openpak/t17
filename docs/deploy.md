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

`T17_DATABASE_URL` (Postgres, e.g. `postgres://t17:…@openpak-postgres:5432/t17?sslmode=disable`) keeps the id map and the
rewards session clock across restarts (tables are created on start; pool of 2). Unset, or if the database is
unreachable, the service logs `ERROR` and runs on memory, retrying the connection every 30 seconds and
writing pending entries through when it returns.

## Account erasure

`DELETE /internal/accounts/{account_id}/data` with `Authorization: Bearer $ACCOUNT_ERASE_KEY`, served on the health listener
(`http://openpak-t17:22631`, container network only). Unset key: 503. The account's `switch` link subjects come from the account
core (`ACCOUNT_GRPC_ADDR`, `ACCOUNT_INTERNAL_KEY`), so call it before the core finalises the deletion. Erased: the `t17_accounts`
row and the in-memory display name, storage slots, session membership, invites and connection of that player. Kept: nothing per
player (the session clock stores no ids). Idempotent; answers `{"service":"t17","rows":N}`.
