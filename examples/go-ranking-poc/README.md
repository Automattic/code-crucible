# Go Ranking PoC

This fixture is a small project for testing Code Crucible end to end.

It contains a deliberately inefficient implementation of:

```go
func TopN(scores []int, n int) []int
```

The evaluator copies each candidate implementation into a temporary Go module, runs golden tests, runs a benchmark, writes `metrics.json` and `verdict.json`, and lets Code Crucible update the leaderboard.

## Manual Baseline Test

```bash
go test ./...
go test -bench=. -benchmem ./...
```

## Code Crucible Flow

From the repository root:

```bash
go build -o bin/crucible ./cmd/crucible

./bin/crucible run \
  --project examples/go-ranking-poc \
  --optimize "$(cat examples/go-ranking-poc/task.md)" \
  --target-path ranking/rank.go \
  --evaluator-script evaluator.sh \
  --variants 2 \
  --external-mode deny

./bin/crucible evaluate --project examples/go-ranking-poc
./bin/crucible leaderboard --project examples/go-ranking-poc
```

To ask Codex for competitors:

```bash
./bin/crucible generate --project examples/go-ranking-poc --agent codex
./bin/crucible evaluate --project examples/go-ranking-poc
./bin/crucible leaderboard --project examples/go-ranking-poc
```
