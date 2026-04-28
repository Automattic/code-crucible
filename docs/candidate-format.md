# Candidate Format

Code Crucible stores every competitor as a directory under a round directory.

For the first round:

```text
.crucible/runs/<run-id>/round-0001/
  candidate-0000-baseline/
  candidate-0001/
  candidate-0002/
```

`candidate-0000-baseline` is created from the original project source. Generated competitors must use the `candidate-NNNN` form, starting at `candidate-0001`.

Candidate IDs are unique across the whole run archive. Later rounds continue after the highest existing candidate number instead of restarting at `candidate-0001`.

## Required Files

Every generated candidate must include:

```text
candidate-NNNN/
  candidate.json
  design.md
  src/
```

`src/` contains the drop-in replacement implementation.

`design.md` explains the implementation approach, expected tradeoffs, known risks, and any assumptions made while preserving the documented interface contract.

`candidate.json` contains machine-readable metadata:

```json
{
  "id": "candidate-0001",
  "name": "short descriptive name",
  "round": 1,
  "parent_ids": ["candidate-0000-baseline"],
  "agent": "selected-agent",
  "model": "gpt-5.5",
  "source_path": "src",
  "baseline": false,
  "created_at": "2026-04-27T20:30:00Z"
}
```

Required fields:

- `id`
- `name`
- `round`
- `parent_ids`
- `agent`
- `source_path`
- `baseline`

`created_at` should be an RFC3339 UTC timestamp. If it is omitted, adoption records the current time in the leaderboard entry.

Adopted candidates are recorded in `leaderboard.json` with project-relative `source_path` values when possible. Evaluator scripts receive absolute runtime paths as arguments and should prefer those arguments, plus `CRUCIBLE_PROJECT_DIR`, over parsing archive paths directly from JSON.

## Adoption

`crucible adopt` scans the active round directory from `run.json` and adds valid generated candidates to `leaderboard.json`. `crucible next-round` advances that active directory after prior candidates have passed.

Adoption validates:

- Directory name matches `candidate-NNNN`
- `candidate.json` exists and parses
- `candidate.json.id` matches the directory name
- `design.md` exists
- `src/` exists
- `source_path` resolves to the candidate's `src/` directory
- `round` matches the active round
- Generated candidates are not marked as baseline

Malformed candidates are reported and skipped. Adoption does not silently repair candidate artifacts.

## Evaluation Artifacts

After `crucible evaluate`, each evaluated candidate may also contain:

```text
candidate-NNNN/
  evaluation.stdout.log
  evaluation.stderr.log
  external-trace.json
  metrics.json
  recorded-http-fixtures.json
  resource-metrics.json
  verdict.json
```

`external-trace.json` is written when the candidate uses a gateway-backed mode such as `allowlist`, `mock`, `replay`, or `record`. It stores request counts, unique hosts, bytes sent and received, failures, and per-request trace events observed through the proxy gateway. In `record` mode, proxied HTTP responses are also captured in `recorded-http-fixtures.json` using the same fixture shape as `external/http-fixtures.json`.

`metrics.json` uses the shared `Metrics` shape from `internal/model`. Evaluator scripts should write benchmark and domain metrics such as:

```json
{
  "runtime_mean_ms": 3.2,
  "p95_latency_ms": 4.8,
  "benchmark_ns_per_op": 4800000,
  "benchmark_runs": 5,
  "memory_peak_bytes": 32768,
  "external_call_count": 0
}
```

Code Crucible augments those script-provided values with process-level resource metrics from the evaluator invocation:

- `wall_time_ms`
- `cpu_user_seconds`
- `cpu_system_seconds`
- `cpu_percent`
- `max_rss_bytes`
- `voluntary_context_switches`
- `involuntary_context_switches`
- `io_bytes_read`
- `io_bytes_written`
- `resource_metric_source`

Local evaluator runs use `resource_metric_source: "host-process"`. Docker and Podman evaluator runs use `resource_metric_source: "container-wrapper"` and measure from inside the sandbox.

Container runs can be bounded with `--sandbox-profile strict`, `--memory-limit`, and `--pids-limit`. The selected container sandbox settings are archived in JSON evaluation reports.

Compare resource metrics only within the same sandbox engine. Docker and Podman runs can produce different timings because they may use different runtimes, storage drivers, rootless settings, and cache states.

`verdict.json` must include:

```json
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
```

If `verdict.json` is missing or invalid, Code Crucible marks the candidate as failed.

`leaderboard.json` also stores `external.policy_enforcement` for each evaluated candidate. Its `status` can be:

- `enforced`: the framework enforced the configured policy
- `partial`: the framework enforced part of the policy, but another required piece is still advisory
- `advisory`: the evaluator and generated code were instructed to honor the policy, but the framework did not enforce it
- `failed`: the requested policy could not be enforced safely, so the candidate failed closed

Currently, `deny` mode is enforced only for Docker or Podman evaluation with `--sandbox-network none`. `allowlist`, `mock`, `replay`, and `record` modes can be `partial` when Code Crucible starts the gateway and exports proxy environment variables. Clients that ignore proxy variables can use direct-routed gateway URLs when their base URL is configurable, or container `--external-routing gateway-network` when declared hostnames should be routed through a sidecar gateway. Local gateway-backed evaluations include warnings because local mode cannot block or transparently intercept raw socket traffic. Container gateway-network evaluations archive `external-routing.json` beside candidate metrics.

`p95_latency_ms` should be a true 95th percentile over the evaluator's sampled timings. Use `runtime_mean_ms` for averages and `benchmark_ns_per_op` for the mean `go test -bench` style operation time.

## Score Explanation

After evaluation, `leaderboard.json` stores a `score_explanation` object beside each candidate's `score`. It records the run-relative primary metric and memory references, candidate ratios, penalty components, final score, and fail-closed reason for unscoreable candidates. This makes score decisions available to future rounds, reports, and agents without reverse-engineering the scoring code.
