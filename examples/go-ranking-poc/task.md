# Go Ranking PoC Task

Optimize `ranking.TopN`.

The implementation must keep this public interface:

```go
func TopN(scores []int, n int) []int
```

Requirements:

- Return the `n` highest scores in descending order.
- Do not mutate the input slice.
- Preserve duplicate values.
- Support negative scores.
- Return all scores sorted descending when `n` is larger than the input length.
- Return an empty, non-nil slice when `n <= 0`.
- Do not perform external network calls.

Primary optimization goal:

- Reduce benchmark `ns/op` for `BenchmarkTopN`.

Secondary goals:

- Keep allocations low.
- Keep the implementation simple enough to review.
