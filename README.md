# Code Crucible

**A tournament runner for code optimization work.**

AI agents are good at producing one implementation. Code Crucible turns that
into a measured competition: capture the baseline, generate drop-in
competitors, test them with deterministic evaluators, archive the evidence, and
rank what actually wins.

The core loop is intentionally small:

```text
baseline -> competitors -> evaluator -> metrics -> leaderboard -> next round
```

Code Crucible runs inside the project you want to improve. It creates a local
`.crucible/` work area with prompts, interface contracts, evaluator scripts,
candidate source, external-call policy data, metrics, reports, and promotion
records. The archive stays inspectable, reproducible, and easy to commit or
ignore.

## Why This Matters

| Audience | What Gets Better |
|---|---|
| Engineers | Optimization proposals come with runnable source, verdicts, metrics, and archived logs instead of a loose chat transcript. |
| Teams reviewing AI output | Candidates compete against the same baseline and evaluator, so reviews can focus on evidence and tradeoffs. |
| Performance-minded maintainers | Leaderboards track latency, benchmark cost, memory, resource usage, and external-call behavior in one place. |
| Tool builders | Agent providers are pluggable; Codex is the first built-in generation provider, not a hard-coded assumption. |
| Risk-sensitive projects | Evaluators can run locally or in Docker/Podman sandboxes with explicit external-call policies and replayable fixtures. |

## What It Does

- Opens a TUI dashboard from bare `crucible` for starting tournaments,
  reviewing candidates, applying winners, continuing rounds, exporting reports,
  and checking command history.
- Creates reproducible tournament archives under `.crucible/runs/`.
- Discovers likely source paths and can ask an agent for a structured handoff
  before a run starts.
- Captures baseline source and interface docs so generated competitors have a
  clear drop-in contract.
- Generates or accepts deterministic evaluator scripts, then validates generated
  evaluators against the baseline.
- Runs local evaluators, repeated measurements, semantic prechecks, and optional
  Docker/Podman sandboxed evaluation.
- Tracks external-call policy modes: `deny`, `allowlist`, `mock`, `replay`, and
  `record`.
- Archives metrics, verdicts, traces, provider invocations, reports, promotion
  records, and a rebuildable SQLite index.
- Promotes the best passing candidate back into the original source path only
  when you choose to apply it.

## Try It Locally

Build the CLI from source:

```bash
git clone https://github.com/Automattic/code-crucible.git
cd code-crucible
go build -o bin/crucible ./cmd/crucible
```

Run it from a project you want to optimize:

```bash
cd /path/to/your/project
/path/to/code-crucible/bin/crucible
```

Or start with the included proof-of-concept fixture:

```bash
make smoke
```

That smoke test creates a tournament around the sample ranking function,
evaluates the baseline, and prints a leaderboard.

For a direct CLI run inside another project:

```bash
crucible run "reduce p95 latency of the search ranking function" --auto
```

Add `--generate --evaluate` when you want the same command to request
competitors and score them after setup.

## Documentation

| Document | Start Here For |
|---|---|
| [docs/README.md](docs/README.md) | Complete map of project docs |
| [docs/getting-started.md](docs/getting-started.md) | Install, requirements, first local tournament, and provider setup |
| [docs/cli-reference.md](docs/cli-reference.md) | Command examples and workflow reference |
| [docs/architecture.md](docs/architecture.md) | Archive model, package layout, and design principles |
| [docs/evaluation.md](docs/evaluation.md) | Evaluators, scoring, sandboxing, resource metrics, and external policies |
| [docs/candidate-format.md](docs/candidate-format.md) | Candidate directory contract and leaderboard artifacts |
| [docs/external-fixtures.md](docs/external-fixtures.md) | Fixture-backed HTTP gateway and replay format |
| [docs/tui.md](docs/tui.md) | TUI design notes and current implementation |
| [.github/CONTRIBUTING.md](.github/CONTRIBUTING.md) | Development workflow and pull request expectations |
| [.github/SECURITY.md](.github/SECURITY.md) | Security policy and sensitive artifact guidance |
| [docs/roadmap.md](docs/roadmap.md) | Current priorities and deferred work |

## Status

Code Crucible is an early Go CLI. The main tournament loop works end to end:
archive creation, agent generation, evaluator validation, local and container
evaluation, fixture-backed external policies, leaderboards, reports, indexing,
promotion, and TUI workflows. The interface is still evolving, so archive
formats and command names may change before a stable release.

## License

Code Crucible is licensed under the GNU General Public License version 2. See
[LICENSE](LICENSE) for details.
