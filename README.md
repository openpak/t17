# Team17

OpenPak's Team17 Server.

| | |
| --- | --- |
| Network | Nintendo Switch |
| Protocol | T17BE: HTTP and one JSON websocket |
| Image | `ghcr.io/openpak/t17` |
| Licence | AGPL-3.0 |

## Titles

| Game | Title ID | Module |
| --- | --- | --- |
| [Moving Out 2](https://openpak.org/titles#t-122) | `010065D018172000` | `cmd/t17d` |

One process serves every title in this table; its handlers are in `cmd/t17d`.

## What it serves

| Host | What | Served by |
| --- | --- | --- |
| `directory.t17service.com` | the addressing: where the game and crossplay tiers live | `t17d` |
| `xplay.t17service.com` | `POST /prod/auth/get-token`, from the console's own BAAS id_token | `t17d` |
| `game.t17service.com` | `/socket`: one websocket carrying every other operation | `t17d` |

## Run

```sh
go build ./...
go test ./...
```

## Docs

- [Protocol facts](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy runbook](docs/deploy.md)
- [Notes](docs/notes.md)
- [Ports](docs/ports.md)

## Licence

AGPL-3.0. See [LICENSE](LICENSE).
