# Getting Started

Code Crucible is a Go CLI that runs from inside another project directory. It
creates a `.crucible/` work area in that project and keeps tournament artifacts
as plain files.

## Requirements

- Go 1.22 or newer
- Linux for current process resource metrics and `--cpu-limit` behavior
- `taskset` when using `crucible evaluate --cpu-limit` in local mode
- Codex CLI on `PATH` when using the built-in `codex` generation provider
- Docker or Podman when using containerized evaluator sandboxes

The SQLite index uses a pure-Go driver and does not require a system SQLite
package.

## Install From Source

```bash
git clone https://github.com/Automattic/code-crucible.git
cd code-crucible
go build -o bin/crucible ./cmd/crucible
```

From another project:

```bash
cd /path/to/project
/path/to/code-crucible/bin/crucible
```

Bare `crucible` opens the TUI dashboard. In a new project it opens directly to
Start Tournament. Press Esc to return to the empty dashboard.

## First Tournament

The lowest-prompt path asks Code Crucible to discover a likely source path,
generate and validate an evaluator, and record baseline metrics:

```bash
crucible run "reduce p95 latency of the search ranking function" --auto
```

To also request competitors and score them in the same command:

```bash
crucible run "reduce p95 latency of the search ranking function" --auto --generate --evaluate
```

When you already know the source path:

```bash
crucible run \
  "reduce p95 latency of the search ranking function" \
  --source-path internal/search/rank.go \
  --variants 5 \
  --external-mode deny
```

For longer task text:

```bash
crucible run \
  --task-file crucible-task.md \
  --source-path internal/search/rank.go
```

## Included Fixture

The repository includes a small Go proof of concept at
[`examples/go-ranking-poc`](../examples/go-ranking-poc). Run the end-to-end
baseline loop with:

```bash
make smoke
```

That target builds `bin/crucible`, creates a run archive under the example
project, evaluates the baseline candidate, and prints the leaderboard.

## Agent Providers

`crucible init --default-agent codex` stores the default provider in
`.crucible/config.json`. Built-in providers are:

- `codex`: discovery, generation, and evolution through Codex CLI
- `local`: heuristic discovery without invoking a model

Commands that invoke an agent accept `--agent`. `run`, `generate`, and `evolve`
resolve the provider from the command flag, then the run archive, then the
project default. `discover` defaults to `local` unless another provider is
passed explicitly.

Additional command providers can be configured in `.crucible/config.json`.
Inspect the Claude Code example without changing project config:

```bash
crucible provider template claude
crucible provider template claude --json
```

See [`cli-reference.md`](cli-reference.md) for the full provider flow and
[`../examples/agent-providers/claude-code-config.json`](../examples/agent-providers/claude-code-config.json)
for the sample config file.

## Where Artifacts Go

Run archives live under:

```text
.crucible/runs/<run-id>/
```

Important files include:

```text
run.json
README.md
docs/interfaces.md
evaluator/evaluator.sh
evaluator/validation.json
external/policy.json
prompts/generation-round-0001.md
round-0001/candidate-0000-baseline/
leaderboard.json
```

Do not commit `.crucible/` archives from private projects unless you have
reviewed them. They can contain source code, prompts, logs, generated
competitors, hostnames, fixtures, and external traces.
