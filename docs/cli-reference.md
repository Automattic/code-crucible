# CLI Reference

This guide focuses on common workflows. Use `crucible <command> --help` for the
full flag list.

## Interactive Entry Points

```bash
crucible
crucible tui
crucible tui --run previous
crucible prompt
```

Bare `crucible` and `crucible tui` open the same dashboard. `crucible prompt`
keeps the older line-oriented workflow for limited terminals and scripted IO
tests.

Commands that accept `--run` can use a full run ID, a unique prefix, `latest`,
or `previous`. Omitting `--run` is equivalent to `--run latest`.

## Initialize A Project

`crucible run` creates `.crucible/` automatically, but explicit initialization
is useful when setting the project default agent:

```bash
crucible init --default-agent codex
```

The default is stored in `.crucible/config.json` as `default_agent`.

## Discover A Boundary

When the source path or evaluator boundary is unclear:

```bash
crucible discover "reduce p95 latency of the search ranking function"
```

Discovery writes a reviewable archive under
`.crucible/discoveries/<discovery-id>/` with source-path suggestions, an agent
discovery prompt, and a checklist. Use an agent when you want a structured
handoff:

```bash
crucible discover "reduce p95 latency of the search ranking function" --agent codex
```

Seed a run from that handoff:

```bash
crucible run \
  "reduce p95 latency of the search ranking function" \
  --agent-plan .crucible/discoveries/<discovery-id>/agent-plan.json
```

## Create A Run

Known source path:

```bash
crucible run \
  "reduce p95 latency of the search ranking function" \
  --source-path internal/search/rank.go \
  --variants 5 \
  --rounds 3 \
  --exploration 0.35 \
  --external-mode deny
```

Task file:

```bash
crucible run \
  --task-file crucible-task.md \
  --source-path internal/search/rank.go
```

Full evaluator script:

```bash
crucible run \
  "reduce p95 latency of the search ranking function" \
  --source-path internal/search/rank.go \
  --evaluator-script ./crucible-evaluator.sh
```

Low-prompt setup:

```bash
crucible run "reduce p95 latency of the search ranking function" --auto
crucible run "reduce p95 latency of the search ranking function" --auto --generate --evaluate
```

`--auto` creates a local discovery plan, selects the top source-path suggestion,
asks the configured provider to draft and validate an evaluator, records
baseline validation as normal metrics, and prints the leaderboard. Adding
`--generate --evaluate` requests competitors and scores them after setup.

## Generate Or Adopt Candidates

Generate competitors for the latest run:

```bash
crucible generate
```

Preview the provider invocation first:

```bash
crucible generate --dry-run
```

Generate during run creation:

```bash
crucible run \
  "reduce allocation pressure in the parser" \
  --source-path internal/parser \
  --variants 4 \
  --generate
```

If candidates were added by hand or by an external agent:

```bash
crucible adopt
```

Generated competitors must follow the [candidate format](candidate-format.md).

## Agent Providers

Code Crucible includes a `codex` provider and supports configured command
providers.

Codex generation uses the selected run prompt and invokes Codex CLI
non-interactively with the host project as the working root. Artifacts are
archived under the run `agents/` directory:

```text
agents/
  codex-<timestamp>-events.jsonl
  codex-<timestamp>-stderr.log
  codex-<timestamp>-invocation.json
  codex-final.md
```

Useful Codex options:

```bash
crucible generate \
  --agent codex \
  --model gpt-5.5 \
  --sandbox workspace-write \
  --approval never \
  --event-json=true
```

Command providers receive the prompt on stdin and can use these environment
variables:

- `CRUCIBLE_PROVIDER_NAME`
- `CRUCIBLE_PROJECT_DIR`
- `CRUCIBLE_RUN_DIR`
- `CRUCIBLE_PROMPT_PATH`
- `CRUCIBLE_OUTPUT_LAST_MESSAGE`
- `CRUCIBLE_MODEL`

Print a provider template:

```bash
crucible provider template claude
crucible provider template claude --json
```

Merge the resulting fragment into `.crucible/config.json`, then use
`crucible generate --agent <name> --dry-run` to inspect the archived command.

## Evaluate And Score

Measure one candidate:

```bash
crucible evaluate --candidate candidate-0000-baseline
```

Evaluate adopted candidates and update the leaderboard:

```bash
crucible evaluate
```

Evaluation defaults to one candidate at a time and `nice -n 10` so tournaments
do not monopolize an interactive workstation:

```bash
crucible evaluate --jobs 1 --nice 10 --cpu-limit 2 --env GOMAXPROCS=1
```

Use `--require-passed` in smoke tests or CI when the shell command should fail
if any evaluated candidate does not pass.

See [evaluation.md](evaluation.md) for semantic checks, sampling, resource
metrics, and sandbox options.

## Leaderboards, Reports, And Queries

```bash
crucible leaderboard
crucible leaderboard --json
crucible index
crucible query runs --json
crucible query candidates --status passed --limit 10 --json
crucible report
```

`crucible index` rebuilds `.crucible/index.sqlite` from archived JSON files. The
index is derivative and can be deleted or rebuilt at any time.

`crucible report` writes a self-contained HTML report for the latest run at
`.crucible/runs/<run-id>/reports/leaderboard.html` by default.

## Promote A Winner

Preview the copy plan:

```bash
crucible promote --dry-run
```

Apply the best passing non-baseline candidate:

```bash
crucible promote
```

Apply a specific candidate:

```bash
crucible promote candidate-0002
```

Promotion copies from the candidate's archived `src/` directory back to the
original source path recorded in `run.json`. File targets replace the matching
file. Directory targets overlay candidate files without deleting unrelated
project files. Every real promotion writes a report under the run
`promotions/` directory.

## Continue A Tournament

Prepare the next round after at least one candidate has passed:

```bash
crucible next-round --parents 3
crucible generate
```

Automate generation, evaluation, and next-round preparation:

```bash
crucible evolve --rounds 3 --parents 3
```
