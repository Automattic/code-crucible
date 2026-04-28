# Contributing

Code Crucible is in an early scaffold phase. Contributions should keep the CLI dependency-light, reproducible, and easy to inspect.

## Development Setup

Requirements:

- Go 1.22 or newer
- `make`
- Linux for the current process resource metrics and `--cpu-limit` behavior
- Codex CLI only when testing live generation

Run the standard checks:

```bash
make check
```

Run the end-to-end proof-of-concept smoke test:

```bash
make smoke
```

The smoke test creates ignored artifacts under `examples/go-ranking-poc/.crucible/`.

Clean ignored local artifacts:

```bash
make clean
```

## Pull Request Expectations

- Keep changes scoped to one behavior or concern.
- Add focused tests for new CLI flags, archive formats, evaluator behavior, or scoring changes.
- Update README and `docs/` when user-facing commands or artifact formats change.
- Run `make check` before opening a pull request.
- Run `make smoke` for changes that affect run creation, evaluation, metrics, leaderboard output, or the Go PoC fixture.

## Artifact Hygiene

Do not commit generated local artifacts:

- `.crucible/`
- `.codex`
- `bin/`
- logs, profiles, coverage output, or local env files

Before committing, check:

```bash
git status --short --ignored
git diff --check
```

If you intentionally add a sample config or env file, use a non-sensitive template such as `.env.example`.

## Commit Style

Use short imperative subjects, matching the existing history:

```text
Add evaluator script support
Fix Codex approval flag placement
```
