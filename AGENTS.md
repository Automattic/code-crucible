# Agent Notes

Code Crucible is a Go CLI project.

## Commands

- Format: `gofmt -w ./cmd ./internal`
- Test: `go test ./...`
- Vet: `go vet ./...`
- Build: `go build -o bin/crucible ./cmd/crucible`
- Full check: `make check`
- Smoke Codex command construction: `bin/crucible generate --project /tmp/crucible-sample --dry-run`

## Project Intent

The CLI should be usable from inside another project directory. It creates and manages a `.crucible/` work area containing optimization tournaments, baseline source, interface docs, evaluator scaffolds, external policy data, prompts, metrics, and leaderboards.

Keep the early implementation dependency-light. Prefer standard library code unless a dependency clearly earns its weight.

## Design Priorities

- Reproducible run archives
- Drop-in replacement contracts for competitors
- Deterministic tests and benchmarks
- External call tracking and replayability
- Agent-provider abstraction without coupling to one model or vendor
- Codex CLI is the first concrete provider; keep its invocation archived and reproducible
- Clear filesystem artifacts that are easy to inspect and commit or ignore
