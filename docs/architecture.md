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

The initial implementation creates the archive and prompt package. Execution, proxy enforcement, and live agent adapters are planned next.

## Project Mode

Code Crucible is meant to run from inside an existing project:

```bash
cd existing-project
crucible init
crucible run --optimize "make this feature faster"
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
leaderboard.json
```

Future rounds will add `round-0002`, `round-0003`, and so on.

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

The first agent integration is prompt-based. Code Crucible writes:

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

Future providers will execute agent CLIs directly through a common provider interface.

## Evaluation

`evaluator/evaluator.sh` is generated for every run.

If `--evaluator` is provided, the scaffold wraps that command and records minimal metrics. If no evaluator is provided, it writes a failing verdict with instructions to add deterministic correctness checks and benchmarks.

The planned evaluator layer will add:

- Containerized execution
- Stable environment variables
- Resource limits
- Structured metrics collection
- External trace collection
- Fail-closed verdict handling

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
