# Code Crucible

Code Crucible is a model-agnostic CLI framework for generating, evaluating, benchmarking, and evolving competing implementations of selected project code.

It is designed to run inside an existing project directory. You describe what should be optimized, Code Crucible creates a tournament work area, extracts or documents the baseline code, captures the required drop-in interfaces, prepares evaluator and external-call policy scaffolds, and builds prompt packages for the selected coding agent.

Status: early scaffold. The CLI can initialize projects, create reproducible run archives, invoke Codex CLI as the first concrete agent provider, evaluate candidates locally or in Docker/Podman, launch fixture-backed mock gateways for sandboxed evaluators, package gateway binaries for container sandboxes, route standard HTTP and HTTPS proxy traffic to fixtures, prepare and automate follow-up rounds, archive leaderboard metrics, rebuild a SQLite index from filesystem artifacts, and write static HTML run reports. Reporting is still under active development.

## Why

AI coding tools are good at producing one implementation. Code Crucible treats code as a competitive artifact:

```text
Generate -> Execute -> Benchmark -> Score -> Archive -> Evolve -> Repeat
```

The goal is not just "does it work", but which implementation works best under measured constraints such as latency, memory, CPU, I/O, external calls, and cost.

## Current Features

- Local git-friendly Go CLI
- Bare `crucible` interactive workflow for creating runs and acting on existing project data
- `crucible discover` for reviewable source-path, interface, evaluator, and external-policy discovery plans
- Automatic `.crucible/` work area setup when `crucible run` is used in an existing project
- `crucible run "..."`, `--optimize ...`, or `--task-file ...` for creating a tournament archive
- Baseline competitor extraction from an optional `--source-path`
- Interface discovery document scaffold
- External policy scaffold for `deny`, `allowlist`, `mock`, `replay`, and `record`
- Evaluator shell scaffold
- Agent generation prompt scaffold
- Codex CLI generation adapter through `codex exec`
- Candidate adoption from generated `candidate-NNNN` artifacts into `leaderboard.json`
- Local evaluator execution through `crucible evaluate`
- Docker and Podman evaluator sandboxing with in-container resource metrics
- Gateway startup, proxy wiring, direct-routed URLs, allowlist checks, replay, and record capture for external-call policies
- `crucible next-round` for preparing follow-up generation prompts from passed candidates
- `crucible evolve` for chaining generation, evaluation, and next-round preparation
- Ranked human-readable leaderboard output for passed candidates
- Machine-readable score explanations in `leaderboard.json`
- File-backed leaderboard and candidate metadata
- Rebuildable `.crucible/index.sqlite` summary for runs and candidates
- Static HTML run reports through `crucible report`
- Core Go interfaces and types for agents, evaluation, scoring, metrics, and archive data

## Install From Source

```bash
git clone https://github.com/Automattic/code-crucible.git
cd code-crucible
go build -o bin/crucible ./cmd/crucible
```

## Requirements

- Go 1.22 or newer
- Linux for process resource metrics
- `taskset` when using `crucible evaluate --cpu-limit` in local mode
- Codex CLI when using `crucible generate --agent codex`
- Docker or Podman when using containerized evaluator sandboxes
- A pure-Go SQLite driver is included for `crucible index`

Run creation, adoption, inspection, and filesystem archive workflows keep JSON files as the source of truth. The SQLite index is derivative and can be rebuilt at any time.

## Quick Start

For a guided workflow, run Code Crucible without arguments:

```bash
cd /path/to/your/project
crucible
```

The interactive flow confirms the project directory, initializes `.crucible/` when needed, creates a discovery plan, asks for the optimization request and variant count, and can run Codex discovery before creating the run. When Codex returns clarifying questions, the wizard records the answers in the run request. When Codex recommends a source path, the wizard can use it as the baseline and copies the structured discovery handoff into the run's `docs/` directory. If the source path is still not known, the run is created without `--source-path` so the generation prompt asks the agent to discover the involved code. The wizard then shows the current leaderboard and offers actions such as new run, leaderboard, generate, evaluate, evolve, report, inspect, and index rebuild.

Create a tournament run from inside an existing project:

```bash
cd /path/to/your/project
crucible run "reduce p95 latency of the search ranking function"
```

`run` creates `.crucible/` automatically when the project does not have one yet.

If the source path or evaluator boundary is unclear, create a discovery plan first:

```bash
crucible discover "reduce p95 latency of the search ranking function"
```

`discover` writes a local plan under `.crucible/discoveries/<discovery-id>/` with source-path suggestions, an agent discovery prompt, and a review checklist. Use `--agent codex --dry-run` to preview Codex discovery, or remove `--dry-run` to let Codex write `agent-plan.md`; Code Crucible extracts the structured handoff into `agent-plan.json` when the final response includes the requested JSON block.

Use a structured discovery handoff when creating the run to seed interface docs, evaluator scaffold guidance, and the baseline source path:

```bash
crucible run \
  "reduce p95 latency of the search ranking function" \
  --agent-plan .crucible/discoveries/<discovery-id>/agent-plan.json
```

When you already know the source file or directory involved, pass it as the initial baseline source:

```bash
crucible run \
  "reduce p95 latency of the search ranking function" \
  --source-path internal/search/rank.go \
  --variants 5 \
  --rounds 3 \
  --exploration 0.35 \
  --external-mode deny
```

For longer tasks, put the request in a Markdown file:

```bash
crucible run \
  --task-file crucible-task.md \
  --source-path internal/search/rank.go
```

For a full evaluator script instead of a short command, use `--evaluator-script`:

```bash
crucible run \
  "reduce p95 latency of the search ranking function" \
  --source-path internal/search/rank.go \
  --evaluator-script ./crucible-evaluator.sh
```

This creates a run under:

```text
.crucible/runs/<run-id>/
```

Key files in each run:

```text
run.json
README.md
docs/interfaces.md
evaluator/evaluator.sh
external/policy.json
prompts/generation-round-0001.md
round-0001/candidate-0000-baseline/
leaderboard.json
```

Generated competitors must follow the [candidate format](docs/candidate-format.md).

Measure the baseline before asking an agent for competitors:

```bash
crucible evaluate --candidate candidate-0000-baseline
```

View the current standings:

```bash
crucible leaderboard
```

The human-readable leaderboard ranks passed candidates by score, then p95 latency. It includes the score-driving metrics such as p95 latency, `ns/op`, speedup versus the baseline, memory, memory usage relative to the baseline, and evaluator CPU time. If any candidate reports external communication, the table also shows `Ext Calls`; no-network runs omit that column. Numeric columns use adaptive precision so close results remain distinguishable and very wide ranges stay readable. Use `--json` when you need archive order, raw result data, and each candidate's machine-readable `score_explanation`.

Rebuild the project-wide SQLite summary from archived JSON artifacts:

```bash
crucible index
```

The index lives at `.crucible/index.sqlite` and contains run and candidate summary tables suitable for reports, ad hoc queries, and future UI work. It is not authoritative; delete it or rebuild it whenever the filesystem archive changes. `crucible index` tracks the index schema version and resets the derivative database automatically when the stored schema is stale or missing.

Query indexed runs or candidates for reporting and automation:

```bash
crucible query runs --json
crucible query candidates --status passed --limit 10 --json
```

`query` reads `.crucible/index.sqlite`; run `crucible index` first when archive data changes.

Write a static HTML report for the latest run:

```bash
crucible report
```

By default, reports are written to `.crucible/runs/<run-id>/reports/leaderboard.html`. Use `--output report.html` to choose a different path.

Commands that accept `--run` can use a full run ID, a unique run ID prefix, `latest`, or `previous`. Omitting `--run` is the same as `--run latest`.

Inspect a candidate:

```bash
crucible inspect candidate-0000-baseline
```

Ask Codex to generate competitor implementations for the latest run:

```bash
crucible generate --agent codex
```

`generate` automatically adopts valid generated candidates into `leaderboard.json`. If competitors are added by hand or an external agent, adopt them manually:

```bash
crucible adopt
```

Run the evaluator against adopted candidates and update leaderboard metrics, verdicts, status, and score:

```bash
crucible evaluate
```

Evaluation runs one candidate at a time by default and starts evaluator processes with `nice -n 10` so tournaments are less likely to overburden the host machine. Use `--jobs` only when you explicitly want parallel candidate evaluation, use `--cpu-limit` when you want CPU affinity control, and use `--nice 0` to disable priority adjustment. Containerized evaluators can also use `--sandbox-profile`, `--memory-limit`, and `--pids-limit` to keep tournament runs bounded.

```bash
crucible evaluate --jobs 1 --nice 10 --cpu-limit 2 --env GOMAXPROCS=1
```

Prepare the next round after at least one candidate has passed:

```bash
crucible next-round --parents 3
crucible generate --agent codex
```

`next-round` creates the next `round-NNNN/` directory, writes a new generation prompt seeded from the top passed candidates, and updates the run archive so `generate` and `adopt` target that active round.

To automate generation, evaluation, and next-round preparation:

```bash
crucible evolve --rounds 3 --parents 3
```

`evolve` runs the active generation prompt, evaluates adopted candidates, and prepares the next prompt between cycles.

Preview the exact Codex invocation first:

```bash
crucible generate --agent codex --dry-run
```

When you already trust the generated scaffold for a task, create the run and invoke Codex in one command:

```bash
crucible run \
  "reduce p95 latency of the search ranking function" \
  --source-path internal/search/rank.go \
  --variants 5 \
  --generate
```

## Running Without a Known Source Path

If you do not know where the relevant code lives yet, omit `--source-path`:

```bash
crucible run "reduce checkout API external calls"
```

The run archive will include:

- A baseline placeholder
- Interface documentation prompts
- An agent prompt instructing the selected agent to discover involved code
- External communication documentation requirements

This supports the intended workflow where the framework runs inside a project and asks an agent to locate the code involved with the requested optimization target.

## Codex Agent Provider

Code Crucible's first live provider targets Codex CLI.

`crucible generate --agent codex` loads the selected run, reads `prompts/generation-round-0001.md`, and invokes:

```bash
codex --ask-for-approval never exec --cd <project> --sandbox workspace-write --json --output-last-message <run>/agents/codex-final.md -
```

The prompt is sent through stdin. Codex runs with the host project as its working root so it can inspect source code and write competitor artifacts under `.crucible/runs/<run-id>/round-0001/`.

Generation artifacts are stored under:

```text
.crucible/runs/<run-id>/agents/
  codex-<timestamp>-events.jsonl
  codex-<timestamp>-stderr.log
  codex-<timestamp>-invocation.json
  codex-final.md
```

After Codex exits successfully, Code Crucible scans the current round directory for valid `candidate-NNNN` artifacts and adds them to `leaderboard.json` with status `generated`.

Useful options:

```bash
crucible generate \
  --agent codex \
  --model gpt-5.5 \
  --sandbox workspace-write \
  --approval never \
  --event-json=true
```

The same Codex options can be passed through `crucible run --generate`:

```bash
crucible run \
  "reduce allocation pressure in the parser" \
  --source-path internal/parser \
  --variants 4 \
  --generate \
  --model gpt-5.5
```

The generated prompt explicitly tells Codex to avoid modifying host project source outside `.crucible`. Temporary verification work is directed to the run's `.crucible/runs/<run-id>/tmp/` scratch area, and the prompt tells Codex to avoid destructive cleanup commands so blocked cleanup attempts do not pollute generation logs. Codex generation sandboxing is controlled by Codex CLI; evaluator sandboxing is handled separately by `crucible evaluate --sandbox-engine`.

## Evaluation

Every run includes `evaluator/evaluator.sh`. `crucible evaluate` executes that script once per candidate currently listed in `leaderboard.json`.

Runs seeded from a structured discovery handoff can also include `evaluator/contract-checks.json`. The generated evaluator scaffold uses that file to run deterministic artifact and source-shape checks before benchmarking; semantic correctness and performance comparisons still require a real evaluator command or script.

The evaluator script receives:

```text
evaluator.sh <candidate-dir> <run-dir> <metrics-out> <verdict-out>
```

It must write:

- `metrics.json`
- `verdict.json`

Evaluation artifacts are stored beside each candidate:

```text
candidate-NNNN/
  evaluation.stdout.log
  evaluation.stderr.log
  metrics.json
  resource-metrics.json
  verdict.json
```

If the evaluator fails or omits `verdict.json`, Code Crucible marks the candidate as failed. If `metrics.json` is missing or invalid, the candidate can still receive a failed verdict with a warning.

Code Crucible also records resource metrics for each evaluator invocation in `resource-metrics.json` and merges them into `metrics.json` before updating `leaderboard.json`. These include wall time, user CPU time, system CPU time, CPU percent, max RSS, context switches, block I/O counts, and `resource_metric_source`. Evaluator scripts should still emit domain-specific metrics such as benchmark latency, allocations, external calls, and correctness verdicts. When an evaluator reports `p95_latency_ms`, it should be a true 95th percentile value for the sampled benchmark or request timings, not an average or median.

Evaluator execution is local by default. For containerized evaluation, pass `--sandbox-engine docker` or `--sandbox-engine podman` with an image that contains `bash` and the required project toolchain:

```bash
crucible evaluate \
  --sandbox-engine podman \
  --sandbox-image golang:1.25 \
  --sandbox-profile strict \
  --cpu-limit 2
```

Use `--warmups` and `--repetitions` when one evaluator run is too noisy:

```bash
crucible evaluate \
  --warmups 1 \
  --repetitions 5 \
  --outliers trim-min-max \
  --sample-stat median
```

Warmup runs are discarded. Measured repetitions are aggregated into `metrics.json`, with per-sample details archived in `evaluation-samples.json`. Aggregated metrics include runtime mean/min/median/max/stddev, standard error, approximate 95% confidence half-width, the selected sample statistic, and the measured repetition count. `--outliers trim-min-max` removes one low and one high measured sample before aggregation when at least three measured samples exist.

Container sandboxes bind-mount the run archive read/write and the host project read-only at their original absolute paths, run with network isolation by default, and pass `--cpu-limit` through as a container CPU quota. Container runs execute an archived resource wrapper inside the sandbox, so CPU and wall-time resource metrics describe the evaluator process inside the container instead of the host Docker or Podman client.

Evaluator sandbox profiles are:

- `default`: local execution unless `--sandbox-engine docker|podman` is set; container runs default to `--sandbox-network none`.
- `strict`: Docker/Podman only; defaults to `--sandbox-network none`, `--memory-limit 1g`, and `--pids-limit 256`.
- `networked`: Docker/Podman only; defaults to `--sandbox-network bridge`, `--memory-limit 1g`, and `--pids-limit 256`.

Use explicit `--sandbox-network`, `--memory-limit`, and `--pids-limit` flags when a profile default needs to be tuned. The `strict` profile always requires `--sandbox-network none`.

Keep all candidates in a tournament on the same sandbox engine. Docker and Podman timings should not be compared as equivalent results because storage drivers, rootless behavior, cache state, and runtime overhead can differ even when both use the same image and wrapper.

## Proof Of Concept Fixture

The repository includes a Go fixture at [examples/go-ranking-poc](examples/go-ranking-poc).

It provides:

- A deliberately slow `ranking.TopN` implementation
- Golden tests and a benchmark
- A reusable evaluator script
- A task prompt for Codex generation

Run the local baseline loop:

```bash
go build -o bin/crucible ./cmd/crucible

./bin/crucible run \
  --project-dir examples/go-ranking-poc \
  --task-file task.md \
  --source-path ranking/rank.go \
  --evaluator-script evaluator.sh \
  --variants 2 \
  --external-mode deny

./bin/crucible evaluate --project-dir examples/go-ranking-poc
./bin/crucible leaderboard --project-dir examples/go-ranking-poc
```

## External Call Policy

Code Crucible treats external communication as a first-class part of evaluation.

Supported policy modes:

- `deny`: no external network access should be required
- `allowlist`: only configured hosts are allowed
- `mock`: local mock handlers should serve deterministic responses
- `replay`: recorded fixtures should serve deterministic responses
- `record`: live responses may be captured for future replay

Example:

```bash
crucible run \
  "reduce API cost in enrichment pipeline" \
  --external-mode allowlist \
  --allow-hosts api.example.com,auth.example.com
```

For `deny` mode, container evaluation enforces network isolation with `--sandbox-network none`. A deny-mode container evaluation fails closed if a different sandbox network is requested. Local deny-mode runs are marked advisory because the framework cannot prevent host-network access around an arbitrary local evaluator.

For `allowlist` mode, Code Crucible starts the archived gateway as an HTTP/HTTPS proxy and denies proxied requests to hosts outside `--allow-hosts`. This is partial enforcement: clients that ignore proxy environment variables can use direct-routed gateway URLs when their base URL is configurable, but raw sockets and fully transparent routing still require evaluator-specific isolation. Live allowlisted hosts may be unreachable when the container sandbox network is `none`.

Fixture-backed modes archive HTTP fixtures in `external/http-fixtures.json`. Provide an existing fixture file with `--external-fixtures`, or omit it to create an empty template for the run. During `mock`, `replay`, and `record` evaluation, Code Crucible exports gateway and proxy environment variables, then starts the archived gateway before running the evaluator. For container evaluation, Code Crucible builds an archived Linux gateway binary on the host when needed so sandbox images do not need Go just to start the gateway. Record mode captures proxied HTTP responses into each candidate's `recorded-http-fixtures.json` and writes `external-trace.json`; transparent HTTPS tunnels are traced but not replay-captured yet. Container mode can combine this with `--sandbox-network none`; local mode still cannot block unrelated host-network access. See [docs/external-fixtures.md](docs/external-fixtures.md) for the JSON format, environment variables, and current routing limits.

## Project Work Area

`crucible run` creates the work area automatically when needed. `crucible init` is available when you want explicit preflight setup or a custom project name. The work area contains:

```text
.crucible/
  agents/
  competitors/
  discoveries/
  evaluators/
  fixtures/http/
  interfaces/
  runs/
  tasks/
  config.json
  README.md
```

The work area is intended to be local project metadata. Completed run directories should be reproducible archives containing source, prompts, metrics, external traces, and verdicts. `crucible index` adds `index.sqlite` as a rebuildable summary of those archives.

New run archives store paths relative to the host project where possible. Runtime commands resolve those paths back to absolute locations and pass evaluator scripts the absolute candidate directory, run directory, and `CRUCIBLE_PROJECT_DIR`.

Do not commit `.crucible/` run archives from private projects unless you have reviewed them. They may contain source code, prompts, logs, generated competitors, hostnames, fixtures, or project-specific context.

## Architecture

Core packages:

- `cmd/crucible`: CLI entrypoint
- `internal/cli`: command parsing and user-facing commands
- `internal/project`: `.crucible/` initialization and config
- `internal/run`: tournament run creation and candidate adoption
- `internal/discovery`: source path inspection, discovery plans, and interface doc generation
- `internal/agent`: agent prompt construction and Codex CLI provider
- `internal/evaluator`: evaluator scaffold generation
- `internal/archive`: JSON archive helpers and baseline copying
- `internal/indexer`: rebuildable SQLite summary index
- `internal/model`: shared data model
- `internal/scoring`: starter scoring logic

See [docs/architecture.md](docs/architecture.md) for the current design.

## Development

Run the standard validation suite:

```bash
make check
```

Run the end-to-end proof-of-concept smoke test:

```bash
make smoke
```

Run the CI-style regression tournament, which extends the smoke test by rebuilding the index, querying passed candidates, and rendering an HTML report:

```bash
make regression-tournament
```

These targets create ignored artifacts under `examples/go-ranking-poc/.crucible/`.

Remove local build, cache, and smoke-test artifacts with:

```bash
make clean
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidance, [SECURITY.md](SECURITY.md) for security notes, and [docs/release-checklist.md](docs/release-checklist.md) before publishing or tagging.

## Roadmap

Completed cleanup and infrastructure:

- [x] Start fixture gateways for local `mock` and `replay` evaluation, or stop exporting local proxy variables that point to no running gateway
- [x] Decide archive path portability: keep absolute runtime paths in `run.json`, or store relative archive paths and resolve absolutes at execution time
- [x] Refresh stale security and external-policy docs so they match current Docker/Podman and proxy behavior
- [x] Split large implementation files before adding reporting: CLI commands, evaluator sandbox/resource handling, and generated gateway source
- [x] Add SQLite schema-version handling for derivative index rebuilds
- [x] Add a cleanup command or Make target for ignored build, cache, and smoke-test artifacts

Completed feature work:

- [x] Add HTML reports
- [x] Add richer SQLite queries for reports and automation
- [x] Expand sandbox profiles and limits
- [x] Add CI regression tournament jobs
- [x] Add guided `crucible` interactive startup flow
- [x] Add `crucible discover` archives with local source-path suggestions
- [x] Add Codex discovery prompts and structured `agent-plan.json` handoffs
- [x] Use Codex discovery handoffs in the interactive run wizard
- [x] Package the fixture gateway for sandbox images without Go

Base functionality roadmap:

- [x] Convert `agent-plan.json` into stronger generated `docs/interfaces.md` sections and evaluator TODOs/scaffolds
- [x] Add `crucible run --agent-plan` to seed run archives from structured discovery handoffs
- [x] Automatically generate deterministic evaluator checks from discovery handoff data when enough contract detail is available
- [x] Add pre-evaluation source-shape contract checks before benchmarking
- [x] Add pre-evaluation Go function signature checks when discovery names a Go drop-in interface
- [ ] Validate semantic drop-in replacement contracts before benchmarking
- [x] Improve no-source-path generation so the agent extracts and archives the baseline before creating competitors
- [x] Add repeated evaluation controls for warmups, repetitions, and runtime spread metrics
- [x] Add outlier handling, confidence summaries, and configurable statistical score inputs
- [x] Add run selection helpers so commands do not always imply the latest run

Model-agnostic agent roadmap:

- [ ] Define a stable provider contract for discovery, generation, and evolution agents
- [ ] Add a project-level default agent setting in `.crucible/config.json`
- [ ] Add per-run and per-command agent selection flags consistently across `discover`, `run`, `generate`, and `evolve`
- [ ] Archive selected agent name, model, provider command, environment policy, prompt path, stdout/stderr, final response, and exit status for reproducibility
- [ ] Support configurable local command providers in addition to the built-in Codex provider
- [ ] Add provider capability metadata, such as supports-discovery, supports-generation, supports-json-output, and requires-git-repo
- [ ] Add validation and dry-run output for provider command construction
- [ ] Add interactive agent selection and project default-agent management
- [ ] Keep Codex as the first concrete provider while avoiding Codex-specific assumptions in shared prompt, archive, and tournament code

Interactive interface roadmap:

- [ ] Add interactive run selection for all actions that currently default to latest run
- [ ] Add standalone interactive discovery flow, including local-only and Codex discovery modes
- [ ] Add interactive `adopt`
- [ ] Add interactive `next-round`
- [ ] Add interactive `query runs` and `query candidates`
- [ ] Add interactive controls for advanced `run` options: evaluator command/script, external mode, fixtures, allow-hosts, rounds, and exploration
- [ ] Add interactive controls for advanced `generate` options: model, profile, sandbox, approval mode, dry-run, and output path
- [ ] Add interactive controls for advanced `evaluate` options: candidate, jobs, timeout, nice, CPU limit, sandbox engine/image/profile/network, memory limit, and PID limit
- [ ] Add interactive controls for report/index JSON and output-path options
- [ ] Add review/edit prompts for generated interface docs, evaluator scaffold, and discovery handoff before generation

Evaluator and external policy roadmap:

- [x] Enforce `allowlist` mode for proxied HTTP and HTTPS traffic
- [x] Add external trace collection and `record` mode capture
- [x] Add direct-routed gateway URLs for clients that ignore proxy environment variables
- [x] Package the fixture gateway for sandbox images without Go
- [ ] Design and implement transparent routing for raw socket clients with an explicit sandbox/network strategy
- [ ] Decide how raw socket routing should behave for local mode versus Docker/Podman mode
- [ ] Extend external trace capture for transparent routing once raw socket interception exists

TUI roadmap:

- [ ] Select and document a Go TUI framework; current recommendation is Bubble Tea from Charmbracelet
- [ ] Extract interactive workflow actions into reusable controller functions shared by prompt mode and TUI mode
- [ ] Build a TUI run dashboard with latest run status, leaderboard, candidate details, and common next actions
- [ ] Build TUI forms for run creation, discovery, generation, evaluation, reports, and queries
- [ ] Add TUI tests around navigation state and command construction

## License

Code Crucible is licensed under the GNU General Public License version 2. See [LICENSE](LICENSE).
