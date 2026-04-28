## Summary

-

## Validation

- [ ] `make check`
- [ ] `make smoke` when run/evaluate/leaderboard behavior changed
- [ ] `make regression-tournament` when reports, indexes, or tournament automation changed
- [ ] `make smoke-gateway-network-docker` or `make smoke-gateway-network-podman` when sandbox routing changed

## Artifact Hygiene

- [ ] I checked `git status --short --ignored`
- [ ] I did not commit private `.crucible/`, `.codex`, credentials, local logs, or generated run archives
- [ ] Any included examples are scrubbed and intentionally committed
