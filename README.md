# Code Crucible

Code Crucible is a model-agnostic CLI framework for generating, evaluating, benchmarking, and evolving competing implementations of selected project code.

It is designed to run inside an existing project directory. You describe what should be optimized, Code Crucible creates a tournament work area, extracts or documents the baseline code, captures the required drop-in interfaces, prepares evaluator and external-call policy scaffolds, and builds prompt packages for the selected coding agent.

Status: early scaffold. The CLI can initialize projects, create reproducible run archives, and invoke Codex CLI as the first concrete agent provider. Container execution, proxy enforcement, and automated evaluation loops are still under active development.

## Why

AI coding tools are good at producing one implementation. Code Crucible treats code as a competitive artifact:

```text
Generate -> Execute -> Benchmark -> Score -> Archive -> Evolve -> Repeat
```

The goal is not just "does it work", but which implementation works best under measured constraints such as latency, memory, CPU, I/O, external calls, and cost.

## Current Features

- Local git-friendly Go CLI
- `crucible init` for adding a `.crucible/` work area to an existing project
- `crucible run --optimize ...` for creating a tournament archive
- Baseline competitor extraction from an optional `--target-path`
- Interface discovery document scaffold
- External policy scaffold for `deny`, `allowlist`, `mock`, `replay`, and `record`
- Evaluator shell scaffold
- Agent generation prompt scaffold
- Codex CLI generation adapter through `codex exec`
- Candidate adoption from generated `candidate-NNNN` artifacts into `leaderboard.json`
- Local evaluator execution through `crucible evaluate`
- File-backed leaderboard and candidate metadata
- Core Go interfaces and types for agents, evaluation, scoring, metrics, and archive data

## Install From Source

```bash
git clone https://github.com/gaarai/code-crucible.git
cd code-crucible
go build -o bin/crucible ./cmd/crucible
```

## Quick Start

Initialize Code Crucible inside an existing project:

```bash
cd /path/to/your/project
crucible init
```

Create a tournament run for a feature, function, handler, or module you want to optimize:

```bash
crucible run \
  --optimize "reduce p95 latency of the search ranking function" \
  --target-path internal/search/rank.go \
  --variants 5 \
  --rounds 3 \
  --exploration 0.35 \
  --external-mode deny
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

View the current standings:

```bash
crucible leaderboard
```

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

Preview the exact Codex invocation first:

```bash
crucible generate --agent codex --dry-run
```

When you already trust the generated scaffold for a task, create the run and invoke Codex in one command:

```bash
crucible run \
  --optimize "reduce p95 latency of the search ranking function" \
  --target-path internal/search/rank.go \
  --variants 5 \
  --generate
```

## Running Without a Known Target Path

If you do not know where the relevant code lives yet, omit `--target-path`:

```bash
crucible run --optimize "reduce checkout API external calls"
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
codex exec --cd <project> --sandbox workspace-write --ask-for-approval never --json --output-last-message <run>/agents/codex-final.md -
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
  --optimize "reduce allocation pressure in the parser" \
  --target-path internal/parser \
  --variants 4 \
  --generate \
  --model gpt-5.5
```

The generated prompt explicitly tells Codex to avoid modifying host project source outside `.crucible`. Sandbox enforcement currently allows workspace writes; stricter write isolation is planned with container execution.

## Evaluation

Every run includes `evaluator/evaluator.sh`. `crucible evaluate` executes that script once per candidate currently listed in `leaderboard.json`.

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
  verdict.json
```

If the evaluator fails or omits `verdict.json`, Code Crucible marks the candidate as failed. If `metrics.json` is missing or invalid, the candidate can still receive a failed verdict with a warning.

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
  --optimize "reduce API cost in enrichment pipeline" \
  --external-mode allowlist \
  --allow-hosts api.example.com,auth.example.com
```

Proxy and container enforcement are not implemented yet. The current scaffold records the policy and requires generated competitors and evaluators to respect it.

## Project Work Area

`crucible init` creates:

```text
.crucible/
  agents/
  competitors/
  evaluators/
  fixtures/http/
  interfaces/
  runs/
  tasks/
  config.json
  README.md
```

The work area is intended to be local project metadata. Completed run directories should be reproducible archives containing source, prompts, metrics, external traces, and verdicts.

## Architecture

Core packages:

- `cmd/crucible`: CLI entrypoint
- `internal/cli`: command parsing and user-facing commands
- `internal/project`: `.crucible/` initialization and config
- `internal/run`: tournament run creation and candidate adoption
- `internal/discovery`: target path inspection and interface doc generation
- `internal/agent`: agent prompt construction and Codex CLI provider
- `internal/evaluator`: evaluator scaffold generation
- `internal/archive`: JSON archive helpers and baseline copying
- `internal/model`: shared data model
- `internal/scoring`: starter scoring logic

See [docs/architecture.md](docs/architecture.md) for the current design.

## Roadmap

- Add Docker or Podman sandbox execution
- Add proxy or mock gateway enforcement
- Add SQLite index alongside filesystem artifacts
- Add replay fixture format and mock handler generator
- Add multi-round evolution strategy
- Add richer leaderboard views and HTML reports
- Add CI integration for regression tournaments

## License

No license has been selected yet.
