# syntax=docker/dockerfile:1
# Team17's backend (T17BE) for the Switch titles that use it. Plain HTTP inside the pod network —
# Traefik terminates the guest's TLS for directory/xplay/game.t17service.com, the same arrangement
# as the Among Us front and servers/battlenet.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/t17d ./cmd/t17d

FROM gcr.io/distroless/static
COPY --from=build /out/t17d /t17d
USER nonroot:nonroot
EXPOSE 22630 22631
ENTRYPOINT ["/t17d"]
