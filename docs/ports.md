# Ports

Claimed from the third-party-network range (`22600–22699`, one block of ten per network), after
Among Us (22600–22609), `servers/demonware` (22610–22619) and `servers/eos` (22620–22629).

| Port | Use |
| --- | --- |
| 22630 | HTTP for all three T17BE hostnames (behind Traefik, which terminates the guest's TLS) |
| 22631 | `/health` |
| 22632–22639 | reserved for this network |

One port serves `directory.`, `xplay.` and `game.` because the tiers are distinguished by
hostname, not by port, and Traefik routes all three names to the same service.
