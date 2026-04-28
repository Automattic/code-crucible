# Architecture

Code Crucible is organized around local, reproducible tournament archives.

## Workflow

```text
optimization request
  -> project discovery
  -> baseline extraction
  -> interface documentation
  -> evaluator scaffold
  -> external policy scaffold
  -> agent prompt package
  -> competitor generation
  -> evaluation
  -> scoring
  -> next generation prompt
```

The current implementation creates the archive, prompt package, Codex generation path, candidate adoption path, local evaluator execution, container evaluator execution, fixture-backed mock gateway startup, HTTP/HTTPS proxy env routing, next-round prompt preparation, automated multi-round execution loops, leaderboard scoring, resource metric archival, and a rebuildable SQLite index. Richer reporting is the next major archive-layer gap.

## Project Mode

Code Crucible is meant to run from inside an existing project:

```bash
cd existing-project
crucible init
crucible run --optimize "make this feature faster"
# or
crucible run --task-file crucible-task.md
```

The project receives a `.crucible/` directory. This keeps optimization artifacts close to the code being evaluated without requiring the host project to adopt Code Crucible as a dependency.

## Run Archive

Each run is stored under:

```text
.crucible/runs/<timestamp>-<slug>/
```

The current layout is:

```text
.crucible/index.sqlite
.crucible/runs/<timestamp>-<slug>/
  run.json
  README.md
  docs/interfaces.md
  evaluator/evaluator.sh
  external/policy.json
  external/http-fixtures.json
  external/mock-gateway.go
  external/mock-ca.pem
  external/mock-ca-key.pem
  prompts/generation-round-0001.md
  round-0001/
    candidate-0000-baseline/
      candidate.json
      design.md
      src/
    candidate-0001/
      candidate.json
      design.md
      src/
  leaderboard.json
```

Future rounds will add `round-0002`, `round-0003`, and so on.

`crucible next-round` creates those follow-up directories and writes `prompts/generation-round-NNNN.md`. Candidate IDs remain run-global, so a second round normally starts after the highest existing candidate number rather than restarting at `candidate-0001`.

Generated candidates are described in [candidate-format.md](candidate-format.md).

## Baseline Candidate

The first competitor is always the original project implementation.

If `--target-path` is provided, Code Crucible copies that file or directory into:

```text
round-0001/candidate-0000-baseline/src/
```

If no target path is provided, the baseline directory contains a placeholder and the agent prompt instructs the selected agent to discover the involved code.

## Interface Contract

`docs/interfaces.md` is the contract that all competitors must satisfy. It should document:

- Inputs
- Outputs
- Error behavior
- Side effects
- External communications
- Correctness checks
- Benchmark metrics
- Disqualification rules

The evaluator should be written against this contract instead of incidental implementation details.

## External Policy

The external policy is recorded in `external/policy.json`.

Supported modes:

- `deny`
- `allowlist`
- `mock`
- `replay`
- `record`

The current implementation records and communicates the policy. Container evaluation enforces `deny` mode by running with `--sandbox-network none`; deny-mode container evaluations fail closed if a different sandbox network is requested. For `mock` and `replay`, sandboxed evaluators receive fixture gateway, proxy, and test CA environment variables, and the container wrapper starts the archived gateway on loopback before the evaluator runs. Local mode and modes other than `deny`, `mock`, and `replay` remain evaluator-advisory until local mocks, DNS overrides, or protocol-specific adapters are implemented.

For `mock`, `replay`, and `record` modes, each run archives an HTTP fixture file at `external/http-fixtures.json`. If `--external-fixtures` is provided, the file is validated and copied into the run archive; otherwise an empty fixture template is created. The generated `external/mock-gateway.go` is the first gateway artifact. It is started automatically for sandboxed fixture-backed evaluations and can serve standard HTTP proxy requests and HTTPS CONNECT replay. Protocol-specific routing remains planned work for clients that ignore proxy environment variables.

## Agent Integration

The first live agent integration is Codex CLI. Code Crucible still writes a prompt package:

```text
prompts/generation-round-0001.md
```

That prompt contains:

- Optimization request
- Variant count
- Exploration setting
- External policy
- Interface contract path
- Baseline and historical metric context
- Output requirements for competitors

`crucible generate --agent codex` invokes Codex non-interactively with the prompt on stdin:

```text
codex --ask-for-approval never exec --cd <project> --sandbox workspace-write --json --output-last-message <run>/agents/codex-final.md -
```

Codex artifacts are archived under:

```text
agents/
  codex-<timestamp>-events.jsonl
  codex-<timestamp>-stderr.log
  codex-<timestamp>-invocation.json
  codex-final.md
```

Codex uses the host project as its working root. The prompt instructs it to write generated competitor artifacts only under the current round directory, use the run `tmp/` scratch area for optional verification work, avoid destructive cleanup commands, and not modify host project source outside `.crucible`.

`crucible run --generate` is an explicit shortcut. It creates the run archive first, then calls the same Codex generation path with the newly-created run ID. The separate `run` and `generate` commands remain the safer default workflow when the operator wants to review or edit interface docs, evaluator scripts, or prompts before spending a model run.

After successful generation, Code Crucible adopts valid `candidate-NNNN` directories into `leaderboard.json`. The same adoption step is available manually with `crucible adopt`.

After evaluation produces passed candidates, `crucible next-round` selects the top passed parents by score, updates the active round in `run.json`, and writes the next generation prompt with historical metrics and parent IDs. The next `crucible generate` invocation uses that active prompt and round directory.

`crucible evolve --rounds N` automates the loop by running generation, evaluating adopted candidates, and preparing the next round between cycles. It uses the same generation and evaluation options as the individual commands so the filesystem artifacts remain inspectable at every step.

Future providers can use the same run metadata and prompt package through a common provider interface.

## Evaluation

`evaluator/evaluator.sh` is generated for every run.

If `--evaluator` is provided, the scaffold wraps that command and records minimal metrics. If `--evaluator-script` is provided, Code Crucible copies that script into the run archive as `evaluator/evaluator.sh`. If neither is provided, the generated script writes a failing verdict with instructions to add deterministic correctness checks and benchmarks.

`crucible evaluate` executes the run evaluator for each candidate currently listed in `leaderboard.json`. The default CLI behavior is sequential (`--jobs 1`) and lower-priority (`--nice 10`) so tournaments do not monopolize an interactive workstation. CPU affinity can be constrained with `--cpu-limit N` on systems with `taskset`. Additional evaluator environment variables can be passed with repeated `--env KEY=VALUE` flags.

Evaluator execution is local by default. Passing `--sandbox-engine docker` or `--sandbox-engine podman` with `--sandbox-image IMAGE` wraps each evaluator invocation in `docker run` or `podman run`. Container mode bind-mounts the run archive read/write, bind-mounts the host project read-only, defaults to `--network none`, maps `--cpu-limit` to a container CPU quota, and records the sandbox settings in JSON evaluation reports.

For fixture-backed `mock` and `replay` runs, evaluator environments include `CRUCIBLE_HTTP_FIXTURES`, `CRUCIBLE_MOCK_GATEWAY_SOURCE`, `CRUCIBLE_MOCK_GATEWAY_ADDR`, `CRUCIBLE_MOCK_GATEWAY_URL`, `CRUCIBLE_MOCK_CA_CERT`, `CRUCIBLE_MOCK_CA_KEY`, standard proxy variables such as `HTTP_PROXY` and `HTTPS_PROXY`, and common trust variables such as `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`, and `NODE_EXTRA_CA_CERTS`. In container mode, `evaluator/resource-wrapper.sh` starts that gateway before invoking `evaluator.sh`. The current gateway is generated Go source, so the sandbox image must include `go` until Code Crucible ships a packaged gateway binary.

Each evaluator run writes `resource-metrics.json` beside the candidate's `metrics.json` and `verdict.json`. Local mode records host child-process metrics. Container mode runs `evaluator/resource-wrapper.sh` inside the sandbox so wall time, user CPU time, system CPU time, and CPU percent come from the isolated evaluator process rather than the host Docker or Podman client. `metrics.resource_metric_source` identifies the source used for the merged resource metrics.

Tournament results should be compared within a single sandbox engine. Docker and Podman runs are both measured inside their containers, but their timings are not interchangeable because runtime, storage, rootless configuration, and cache behavior can differ.

It passes:

```text
evaluator.sh <candidate-dir> <run-dir> <metrics-out> <verdict-out>
```

The evaluator must write `metrics.json` and `verdict.json`. Code Crucible reads both files, merges resource metrics, updates `leaderboard.json`, computes a relative log-scaled score, and marks candidates as `passed` only when correctness, benchmark, and external policy verdicts all pass. Missing or malformed verdicts fail closed.

The planned evaluator layer will add:

- Lower-level routing for clients that ignore proxy environment variables
- Allowlist enforcement
- External trace collection
- Richer sandbox profiles

## Data Model

The Go data model currently includes:

- `RunConfig`
- `Candidate`
- `Metrics`
- `ExternalCallTrace`
- `Verdict`
- `CandidateResult`
- `Leaderboard`

The filesystem archive remains the source of truth. `crucible index` rebuilds `.crucible/index.sqlite` from `run.json` and `leaderboard.json`, replacing stale rows with summaries of runs and candidates. The database currently contains `runs`, `candidates`, and `index_meta` tables, plus indexes that support leaderboard-style report queries. It is derivative local metadata; if it is missing, stale, or corrupted, rebuild it from the archive instead of editing it by hand.

The human-readable leaderboard view sorts passed candidates by score, then p95 latency. Scores are relative to the best passed candidate in the run, with each latency doubling costing points so large performance gaps remain visible. The table shows score-driving metrics, including baseline-relative speedup, memory usage, memory usage relative to the baseline, and evaluator CPU time; evaluator CPU time is labeled separately because it includes harness overhead and is not the same as per-operation cost. External call counts are shown as `Ext Calls` only when at least one candidate reports external communication. The JSON output preserves the archived result data for automation and includes `score_explanation` penalty components for each evaluated candidate.
