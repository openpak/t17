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
