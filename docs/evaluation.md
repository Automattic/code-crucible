# Evaluation

Every run includes an evaluator script at `evaluator/evaluator.sh`.
`crucible evaluate` executes that script for candidates listed in
`leaderboard.json`, reads the produced metrics and verdict, merges resource
metrics, updates candidate status, and recomputes scores.

## Evaluator Contract

The evaluator receives four positional arguments:

```text
evaluator.sh <candidate-dir> <run-dir> <metrics-out> <verdict-out>
```

It must write:

- `metrics.json`
- `verdict.json`

`verdict.json` must include:

```json
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
```

If the evaluator exits unsuccessfully, omits `verdict.json`, or writes malformed
JSON, Code Crucible fails the candidate closed. Missing or invalid metrics are
reported in the candidate output and produce an unscoreable failed result.

Evaluator environments include `CRUCIBLE_PROJECT_DIR` and `CRUCIBLE_RUN_DIR` so
scripts can avoid parsing archive paths directly from JSON.

## Generated Evaluators

When a run has no evaluator, ask the selected provider to draft one:

```bash
crucible evaluator generate
```

The command writes `prompts/evaluator-generation.md`, asks the provider to
replace `evaluator/evaluator.sh`, optionally records notes at
`evaluator/evaluator.md`, validates the result against the baseline, and stores
the validation report at `evaluator/validation.json`. A passing validation marks
the run evaluator-ready and moves waiting candidates from `needs-evaluator` to
`pending`.

## Semantic Checks

For behavior-level prechecks, add `evaluator/semantic-checks.json`. Each check
runs once in the baseline `src/` directory and once in the candidate `src/`
directory before the benchmark evaluator starts.

```json
{
  "version": 1,
  "checks": [
    {
      "name": "golden fixture",
      "command": "go test ./...",
      "timeout_ms": 30000,
      "compare_exit_code": true,
      "compare_stdout": true
    }
  ]
}
```

Failing semantic checks stop evaluation before benchmarking and archive
candidate-scoped results in `semantic-contract-results.json`.

## Metrics

Evaluator scripts should emit domain-specific metrics such as:

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

`p95_latency_ms` should be a true 95th percentile over sampled timings. Use
`runtime_mean_ms` for averages and `benchmark_ns_per_op` for Go benchmark-style
mean operation time.

Code Crucible augments evaluator metrics with process-level resource data:

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

Local evaluator runs use `resource_metric_source: "host-process"`.
Docker/Podman runs use `resource_metric_source: "container-wrapper"`.

## Sampling

Use repeated evaluation when one evaluator run is too noisy:

```bash
crucible evaluate \
  --warmups 1 \
  --repetitions 5 \
  --outliers trim-min-max \
  --sample-stat median
```

Warmups are discarded. Measured repetitions are aggregated into `metrics.json`,
and per-sample details are archived in `evaluation-samples.json`.

Aggregated metrics include runtime mean/min/median/max/stddev, standard error,
approximate 95% confidence half-width, selected sample statistic, and measured
repetition count. `--outliers trim-min-max` removes one low and one high
measured sample when at least three measured samples exist.

## Benchmark Hygiene

Evaluators should measure the real optimization target rather than a repeated
fixture shortcut. Unless cache performance is explicitly part of the task, use
varied deterministic inputs or fresh process/context isolation so in-process
memoization cannot skip the work being optimized.

When caching is intentionally in scope, report cache-cold and cache-warm metrics
separately and document which metric drives the score.

## Local Execution

Evaluation defaults to sequential local execution with lower process priority:

```bash
crucible evaluate --jobs 1 --nice 10
```

Use `--jobs` only when you explicitly want parallel candidate evaluation. Use
`--cpu-limit` for CPU affinity on systems with `taskset`, and pass additional
evaluator environment with repeated `--env KEY=VALUE`.

## Container Sandboxes

For containerized evaluation, pass Docker or Podman plus an image that contains
`bash` and the required project toolchain:

```bash
crucible evaluate \
  --sandbox-engine podman \
  --sandbox-image golang:1.22 \
  --sandbox-profile strict \
  --cpu-limit 2
```

Container sandboxes bind-mount the run archive read/write and the host project
read-only, default to network isolation, and pass `--cpu-limit` through as a
container CPU quota. The archived resource wrapper runs inside the sandbox so
CPU and wall-time resource metrics describe the evaluator process rather than
the host Docker or Podman client.

Sandbox profiles:

| Profile | Behavior |
|---|---|
| `default` | Local execution unless `--sandbox-engine docker|podman` is set; container runs default to `--sandbox-network none`. |
| `strict` | Docker/Podman only; defaults to `--sandbox-network none`, `--memory-limit 1g`, and `--pids-limit 256`. |
| `networked` | Docker/Podman only; defaults to `--sandbox-network bridge`, `--memory-limit 1g`, and `--pids-limit 256`. |

Use explicit `--sandbox-network`, `--memory-limit`, and `--pids-limit` flags
when a profile default needs tuning. The `strict` profile always requires
`--sandbox-network none`.

Keep all candidates in a tournament on the same sandbox engine. Docker and
Podman timings should not be compared as equivalent results because runtimes,
storage drivers, rootless behavior, cache state, and wrapper overhead can
differ.

## External Call Policies

Code Crucible treats external communication as part of evaluation.

Supported policy modes:

- `deny`: no external network access should be required
- `allowlist`: only configured hosts are allowed
- `mock`: deterministic local mock handlers serve responses
- `replay`: recorded fixtures serve deterministic responses
- `record`: live responses may be captured for future replay

Example:

```bash
crucible run \
  "reduce API cost in enrichment pipeline" \
  --external-mode allowlist \
  --allow-hosts api.example.com,auth.example.com
```

For `deny` mode, container evaluation enforces network isolation with
`--sandbox-network none`. Local deny-mode runs are advisory because the
framework cannot isolate host-network access around arbitrary local evaluator
scripts.

For `allowlist`, Code Crucible starts the archived gateway as an HTTP/HTTPS
proxy and denies proxied requests to hosts outside `--allow-hosts`. This is
partial enforcement unless container `--external-routing gateway-network` is
used for declared hosts.

Fixture-backed modes archive HTTP fixtures in `external/http-fixtures.json`.
Provide fixtures with `--external-fixtures`, or omit it to create an empty
template. During `mock`, `replay`, and `record`, Code Crucible exports gateway
and proxy environment variables and starts the archived gateway before running
the evaluator.

For container evaluation, Code Crucible builds an archived Linux gateway binary
on the host when needed so sandbox images do not need Go just to start the
gateway. Record mode captures gateway-routed HTTP responses into each
candidate's `recorded-http-fixtures.json` and writes `external-trace.json`.

See [external-fixtures.md](external-fixtures.md) for fixture format details.

## Gateway Network Routing

For clients that ignore proxy environment variables, container evaluation can
route declared external hostnames through a gateway sidecar:

```bash
crucible evaluate \
  --sandbox-engine podman \
  --sandbox-image golang:1.22 \
  --external-routing gateway-network
```

`gateway-network` is valid only for Docker or Podman with a non-strict sandbox
profile. Code Crucible creates a per-candidate bridge network, starts the
archived mock gateway as a sidecar, maps declared hosts from `--allow-hosts` and
HTTP fixtures into the evaluator container, captures gateway traces, and
archives `external-routing.json`. If the network, sidecar, privileged gateway
ports, or host mappings cannot be established, the candidate fails closed.

See [container-raw-socket-routing.md](container-raw-socket-routing.md) for the
network design.

## Scoring

The human-readable leaderboard ranks passed candidates by score, then p95
latency. It shows score-driving metrics such as p95 latency, `ns/op`, speedup
versus baseline, memory, memory usage relative to baseline, and evaluator CPU
time. If any candidate reports external communication, the table also shows
`Ext Calls`.

`leaderboard.json` preserves the archived result data for automation and
includes a machine-readable `score_explanation` object for each evaluated
candidate.
