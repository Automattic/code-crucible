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

The current implementation creates discovery plan archives, run archives, prompt packages, Codex discovery and generation paths, candidate adoption paths, local evaluator execution, container evaluator execution, fixture-backed mock gateway startup, packaged gateway binaries for container sandboxes, HTTP/HTTPS proxy env routing, container gateway-network routing for declared raw socket hosts, next-round prompt preparation, automated multi-round execution loops, leaderboard scoring, resource metric archival, a rebuildable SQLite index, and static HTML run reports. Richer query and reporting workflows remain active work.

## Project Mode

Code Crucible is meant to run from inside an existing project:

```bash
cd existing-project
crucible
# or
crucible discover "make this feature faster"
# or
crucible run "make this feature faster"
# or
crucible run --task-file crucible-task.md
```

The bare `crucible` command starts a guided workflow. It confirms the project directory, initializes `.crucible/` when needed, creates a discovery plan for new optimization requests, optionally runs discovery through a configured agent, records clarifying answers, creates runs, and offers common follow-up actions for existing run data. Interactive run creation can opt into advanced evaluator/external/round/exploration options, run actions prompt for a run selector instead of silently assuming the latest run, standalone discovery can run locally or through a discovery-capable agent, generated candidates can be adopted, next rounds can be prepared, archive queries can be run, reports can choose output/JSON options, index rebuilds can scope to one run or print JSON, generate can review or edit interface docs, evaluator scaffolds, generation prompts, and discovery handoffs through `$VISUAL` or `$EDITOR`, generate can opt into advanced model/output/dry-run and Codex profile/sandbox/approval options, evaluate can opt into candidate/resource/sandbox options, generate and evolve actions can select a generation agent, and agent settings can update the project `default_agent`. The explicit `crucible tui` command opens a Bubble Tea dashboard for reviewing the latest or selected run status, leaderboard rows, selected candidate details, and basic forms for creating runs, discovery, generation, evaluation, reports, and archive queries. When a structured agent discovery handoff is available, the wizard can use the recommended source path and copies the handoff into the run's `docs/` directory. The project receives a `.crucible/` directory automatically when the first run or discovery plan is created. This keeps optimization artifacts close to the code being evaluated without requiring the host project to adopt Code Crucible as a dependency. `crucible init` remains available for explicit preflight setup or a custom project name.

## Discovery Archive

`crucible discover "..."` creates a reviewable archive under:

```text
.crucible/discoveries/<discovery-id>/
```

Each discovery archive contains the original request, a local plan with heuristic source-path suggestions, and a prompt that can be sent to any discovery-capable agent. Agent discovery writes artifacts under the discovery archive's `agents/` directory, stores the final agent response as `agent-plan.md`, and extracts the machine-readable handoff to `agent-plan.json` when the response includes the requested JSON block.

Archive metadata stores project-local paths where possible, such as `.crucible/runs/<run-id>/...`, instead of absolute host paths. Runtime commands resolve those archive paths against the current project directory so the same archive can be inspected, indexed, or moved without hard-coding a private workstation path.

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
  evaluator/semantic-checks.json
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

If `--source-path` is provided, Code Crucible copies that file or directory into:

```text
round-0001/candidate-0000-baseline/src/
```

If no source path is provided, the baseline directory contains a placeholder and the agent prompt instructs the selected agent to discover the involved code.

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

The current implementation records and communicates the policy. Container evaluation enforces `deny` mode by running with `--sandbox-network none`; deny-mode container evaluations fail closed if a different sandbox network is requested. For `allowlist`, `mock`, `replay`, and `record`, evaluators receive gateway and proxy environment variables. Local evaluation starts the archived gateway on loopback before the evaluator runs, and container evaluation starts the gateway inside the resource wrapper. When `--external-routing gateway-network` is used with Docker or Podman, Code Crucible creates a per-candidate evaluator network, starts the gateway as a sidecar on HTTP and HTTPS ports, maps declared hosts to that sidecar, and archives `external-routing.json`. `allowlist` mode denies proxied and gateway-routed requests to hosts outside the configured allowlist. `record` mode captures gateway-routed HTTP responses for future replay and writes candidate-scoped trace summaries. Local mode still cannot block unrelated host-network access, and modes other than `deny`, `allowlist`, `mock`, `replay`, and `record` remain evaluator-advisory until protocol-specific adapters are implemented.

For `allowlist`, `mock`, `replay`, and `record` modes, each run archives an HTTP fixture file at `external/http-fixtures.json`. If `--external-fixtures` is provided, the file is validated and copied into the run archive; otherwise an empty fixture template is created. The generated `external/mock-gateway.go` is the source gateway artifact. It is started automatically for local and sandboxed `allowlist`, `mock`, `replay`, and `record` evaluations. Container evaluation builds an archived Linux `external/mock-gateway-<goos>-<goarch>` binary when the gateway is needed, then runs that binary inside the sandbox or as the `gateway-network` sidecar so the sandbox image does not need Go for gateway startup. In `allowlist` mode it forwards only allowlisted gateway traffic to live upstream hosts. In `mock` and `replay` modes it serves standard HTTP proxy requests, direct-routed gateway URLs, raw HTTP host routing, direct TLS host routing with the archived mock CA, and HTTPS CONNECT replay. In `record` mode it forwards live gateway traffic, writes `external-trace.json`, and records HTTP fixtures beside the evaluated candidate. Raw socket routing is only available for Docker/Podman sandboxes through the explicit isolated evaluator network and gateway sidecar; local mode remains advisory and warns when transparent interception would be required. See [container raw socket routing](container-raw-socket-routing.md) for the network design.

## Agent Integration

Agent integration is routed through a small provider contract. A provider declares whether it supports discovery, generation, evolution, JSON output, and whether it requires a Git repository. Built-in providers currently include `codex`, which supports discovery, generation, and evolution through Codex CLI, and `local`, which supports heuristic discovery without invoking a model. Project config can also define `kind: command` providers with an argv-style `command` list and capability metadata.

Each project can store a default provider in `.crucible/config.json` as `default_agent`. New work areas default to `codex`, and `crucible init --default-agent codex` can set it explicitly. Agent-invoking commands accept `--agent`; generation and evolution resolve from the command flag, then the run archive, then the project default. Discovery defaults to `local` for non-interactive `crucible discover`; the interactive new-run wizard uses the project default when it supports discovery and otherwise falls back to `codex` for structured agent handoffs.

The first live model-backed integration is Codex CLI. Code Crucible still writes a prompt package:

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

`codex-<timestamp>-invocation.json` records the selected provider, model/profile overrides, command, prompt path, stdout/stderr paths, final response path, exit status, and environment policy fields for reproducibility.

Command providers are invoked with the prompt on stdin and receive environment variables for the provider name, project directory, run directory, prompt path, final-response path, and model override. Their stdout, stderr, final response, and invocation metadata are archived under the same `agents/` directory.

Codex uses the host project as its working root. The prompt instructs it to write generated competitor artifacts only under the current round directory, use the run `tmp/` scratch area for optional verification work, avoid destructive cleanup commands, and not modify host project source outside `.crucible`.

`crucible run --generate` is an explicit shortcut. It creates the run archive first, then calls the selected generation provider with the newly-created run ID. The separate `run` and `generate` commands remain the safer default workflow when the operator wants to review or edit interface docs, evaluator scripts, or prompts before spending a model run.

After successful generation, Code Crucible adopts valid `candidate-NNNN` directories into `leaderboard.json`. The same adoption step is available manually with `crucible adopt`.

After evaluation produces passed candidates, `crucible next-round` selects the top passed parents by score, updates the active round in `run.json`, and writes the next generation prompt with historical metrics and parent IDs. The next `crucible generate` invocation uses that active prompt and round directory.

`crucible evolve --rounds N` automates the loop by running generation, evaluating adopted candidates, and preparing the next round between cycles. It uses the same generation and evaluation options as the individual commands so the filesystem artifacts remain inspectable at every step.

Future providers can use the same run metadata and prompt package through a common provider interface.

## Evaluation

`evaluator/evaluator.sh` is generated for every run.

If `--evaluator` is provided, the scaffold wraps that command and records minimal metrics. If `--evaluator-script` is provided, Code Crucible copies that script into the run archive as `evaluator/evaluator.sh`. If neither is provided, the generated script writes a failing verdict with instructions to add deterministic correctness checks and benchmarks.

`crucible evaluate` executes the run evaluator for each candidate currently listed in `leaderboard.json`. The default CLI behavior is sequential (`--jobs 1`) and lower-priority (`--nice 10`) so tournaments do not monopolize an interactive workstation. CPU affinity can be constrained with `--cpu-limit N` on systems with `taskset`. Additional evaluator environment variables can be passed with repeated `--env KEY=VALUE` flags.

Runs can include `evaluator/semantic-checks.json` for behavior-level prechecks. Each check command runs once in the baseline `src/` directory and once in the candidate `src/` directory before the benchmark evaluator starts. The framework compares exit code and stdout by default, can optionally compare stderr, and archives candidate-scoped results in `semantic-contract-results.json`. A semantic mismatch fails the candidate without running the benchmark evaluator.

Evaluator execution is local by default. Passing `--sandbox-engine docker` or `--sandbox-engine podman` with `--sandbox-image IMAGE` wraps each evaluator invocation in `docker run` or `podman run`. Container mode bind-mounts the run archive read/write, bind-mounts the host project read-only, defaults to `--network none`, maps `--cpu-limit` to a container CPU quota, and records the sandbox settings in JSON evaluation reports. The `strict` profile adds default `--memory-limit 1g` and `--pids-limit 256` caps while keeping network disabled; the `networked` profile applies the same default caps with `--network bridge`. Explicit `--memory-limit`, `--pids-limit`, and `--sandbox-network` flags can tune those defaults, except `strict` always requires `none` networking. `--external-routing gateway-network` requires Docker or Podman with a non-strict profile and replaces the evaluator container network with a per-candidate bridge network that includes the gateway sidecar.

For `allowlist`, `mock`, `replay`, and `record` runs, evaluator environments include `CRUCIBLE_HTTP_FIXTURES`, `CRUCIBLE_MOCK_GATEWAY_SOURCE`, `CRUCIBLE_MOCK_GATEWAY_ADDR`, `CRUCIBLE_MOCK_GATEWAY_URL`, standard proxy variables such as `HTTP_PROXY` and `HTTPS_PROXY`, trace variables such as `CRUCIBLE_EXTERNAL_TRACE`, and mode-specific values such as `CRUCIBLE_ALLOWED_HOSTS`, `CRUCIBLE_RECORD_FIXTURES`, or test CA variables. In container mode, `evaluator/resource-wrapper.sh` starts that gateway before invoking `evaluator.sh`. The current gateway is generated Go source, so the sandbox image must include `go` until Code Crucible ships a packaged gateway binary.

Each evaluator run writes `resource-metrics.json` beside the candidate's `metrics.json` and `verdict.json`. Local mode records host child-process metrics. Container mode runs `evaluator/resource-wrapper.sh` inside the sandbox so wall time, user CPU time, system CPU time, and CPU percent come from the isolated evaluator process rather than the host Docker or Podman client. `metrics.resource_metric_source` identifies the source used for the merged resource metrics.

Tournament results should be compared within a single sandbox engine. Docker and Podman runs are both measured inside their containers, but their timings are not interchangeable because runtime, storage, rootless configuration, and cache behavior can differ.

It passes:

```text
evaluator.sh <candidate-dir> <run-dir> <metrics-out> <verdict-out>
```

Those positional paths are absolute runtime paths. Evaluator environments also include `CRUCIBLE_PROJECT_DIR` and `CRUCIBLE_RUN_DIR` so scripts do not need to parse path fields from `run.json`.

The evaluator must write `metrics.json` and `verdict.json`. Code Crucible reads both files, merges resource metrics, updates `leaderboard.json`, computes a relative log-scaled score, and marks candidates as `passed` only when correctness, benchmark, and external policy verdicts all pass. Missing or malformed verdicts fail closed.

The planned evaluator layer will add:

- Additional protocol-specific adapters beyond HTTP and HTTPS gateway routing

## Data Model

The Go data model currently includes:

- `RunConfig`
- `Candidate`
- `Metrics`
- `ExternalCallTrace`
- `Verdict`
- `CandidateResult`
- `Leaderboard`

The filesystem archive remains the source of truth. `crucible index` rebuilds `.crucible/index.sqlite` from `run.json` and `leaderboard.json`, replacing stale rows with summaries of runs and candidates. The database currently contains `runs`, `candidates`, and `index_meta` tables, plus indexes that support leaderboard-style report queries. It records the schema version in `index_meta`; if the stored schema is missing or stale, Code Crucible deletes the derivative database and rebuilds it from the archive. It is local metadata, so rebuild it from the archive instead of editing it by hand.

The human-readable leaderboard view sorts passed candidates by score, then p95 latency. Scores are relative to the best passed candidate in the run, with each latency doubling costing points so large performance gaps remain visible. The table shows score-driving metrics, including baseline-relative speedup, memory usage, memory usage relative to the baseline, and evaluator CPU time; evaluator CPU time is labeled separately because it includes harness overhead and is not the same as per-operation cost. External call counts are shown as `Ext Calls` only when at least one candidate reports external communication. The JSON output preserves the archived result data for automation and includes `score_explanation` penalty components for each evaluated candidate.

`crucible query runs` and `crucible query candidates` expose stable indexed summaries for automation. The run query returns candidate counts, passed/failed/pending counts, active rounds, policy mode, and best passed candidate data. The candidate query supports run, status, and limit filters and returns score-driving metrics from the indexed candidate rows.

`crucible report` renders a self-contained HTML report for one run. The default output is `reports/leaderboard.html` inside the run archive, and the report is generated from `run.json` plus `leaderboard.json` so it remains reproducible and does not require the derivative SQLite index.
