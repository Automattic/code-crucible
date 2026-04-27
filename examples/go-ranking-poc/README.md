# Go Ranking PoC

This fixture is a small project for testing Code Crucible end to end.

It contains a deliberately inefficient implementation of:

```go
func TopN(scores []int, n int) []int
```

The evaluator copies each candidate implementation into a temporary Go module, runs golden tests, runs the benchmark five times with `GOMAXPROCS=1`, writes mean and true p95 benchmark metrics to `metrics.json`, writes `verdict.json`, and lets Code Crucible update the leaderboard.

## Manual Baseline Test

```bash
go test ./...
GOMAXPROCS=1 go test -cpu=1 -bench=. -benchmem -count=5 ./...
```

## Code Crucible Flow

From the repository root:

```bash
go build -o bin/crucible ./cmd/crucible

./bin/crucible run \
  --project examples/go-ranking-poc \
  --task-file task.md \
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
