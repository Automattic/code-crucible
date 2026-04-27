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

The initial implementation creates the archive, prompt package, Codex generation path, candidate adoption path, and local evaluator execution. Proxy enforcement and containerized execution are planned next.

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
run.json
README.md
docs/interfaces.md
evaluator/evaluator.sh
external/policy.json
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

The current implementation records and communicates the policy. A later execution layer will enforce it through container networking, local mocks, proxying, DNS overrides, or protocol-specific adapters.

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

Codex uses the host project as its working root. The prompt instructs it to write only under the current round directory and not to modify host project source outside `.crucible`.

`crucible run --generate` is an explicit shortcut. It creates the run archive first, then calls the same Codex generation path with the newly-created run ID. The separate `run` and `generate` commands remain the safer default workflow when the operator wants to review or edit interface docs, evaluator scripts, or prompts before spending a model run.

After successful generation, Code Crucible adopts valid `candidate-NNNN` directories into `leaderboard.json`. The same adoption step is available manually with `crucible adopt`.

Future providers can use the same run metadata and prompt package through a common provider interface.

## Evaluation

`evaluator/evaluator.sh` is generated for every run.

If `--evaluator` is provided, the scaffold wraps that command and records minimal metrics. If `--evaluator-script` is provided, Code Crucible copies that script into the run archive as `evaluator/evaluator.sh`. If neither is provided, the generated script writes a failing verdict with instructions to add deterministic correctness checks and benchmarks.

`crucible evaluate` executes the run evaluator for each candidate currently listed in `leaderboard.json`. The default CLI behavior is sequential (`--jobs 1`) and lower-priority (`--nice 10`) so tournaments do not monopolize an interactive workstation. CPU affinity can be constrained with `--cpu-limit N` on systems with `taskset`. Additional evaluator environment variables can be passed with repeated `--env KEY=VALUE` flags.

It passes:

```text
evaluator.sh <candidate-dir> <run-dir> <metrics-out> <verdict-out>
```

The evaluator must write `metrics.json` and `verdict.json`. Code Crucible reads both files, adds process-level resource metrics collected around the evaluator invocation, updates `leaderboard.json`, computes a starter score, and marks candidates as `passed` only when correctness, benchmark, and external policy verdicts all pass. Missing or malformed verdicts fail closed.

The planned evaluator layer will add:

- Containerized execution
- Resource limits
- External trace collection

## Data Model

The Go data model currently includes:

- `RunConfig`
- `Candidate`
- `Metrics`
- `ExternalCallTrace`
- `Verdict`
- `CandidateResult`
- `Leaderboard`

The filesystem archive is the source of truth for now. SQLite indexing is planned once the artifact format stabilizes.

The human-readable leaderboard view sorts passed candidates by score, then p95 latency. The JSON output preserves the archived result data for automation.
