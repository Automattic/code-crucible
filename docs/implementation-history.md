# Implementation History

This document keeps completed implementation context out of the roadmap while
preserving the major milestones that explain Code Crucible's current shape.

## Tournament Workflow

- Added low-prompt `run --auto` setup with source-path discovery, generated
  evaluator validation, baseline metrics, optional generation, and optional
  evaluation.
- Made bare `crucible` open the TUI, with forms for common tournament actions
  and the older prompt workflow preserved as `crucible prompt`.
- Added candidate promotion with dry-run previews, passed-candidate defaults,
  `.crucible/` target protection, and archived promotion reports.
- Added next-round and evolve workflows so tournaments can use passed
  candidates as parents for follow-up generation.

## Provider Abstraction

- Added a model-agnostic provider contract with capability metadata for
  discovery, generation, evolution, JSON output, and Git requirements.
- Kept Codex as the first built-in generation provider while moving shared
  prompt, archive, and tournament code away from Codex-specific assumptions.
- Added configurable command providers, provider templates, dry-run invocation
  inspection, archived provider stdout/stderr/final responses, and project-level
  default-agent settings.

## Evaluation And Scoring

- Added evaluator generation and baseline validation before marking a run
  evaluator-ready.
- Added semantic contract checks, Go signature prechecks, repeated evaluator
  sampling, outlier handling, configurable sample statistics, and score
  explanations in `leaderboard.json`.
- Added resource metrics for local evaluator processes and Docker/Podman
  container wrappers.
- Added cache-resistant evaluator guidance so generated tests measure the real
  optimization target unless cache performance is explicitly in scope.

## External Calls And Sandboxes

- Added external policy modes for `deny`, `allowlist`, `mock`, `replay`, and
  `record`.
- Added fixture-backed HTTP gateway support, proxy environment wiring,
  direct-routed URLs, external trace capture, and record/replay fixture
  archives.
- Added Docker/Podman sandbox profiles, resource limits, and container-only
  `gateway-network` routing for declared raw HTTP/HTTPS hosts.
- Documented local-mode limits for network enforcement and raw socket routing.

## Reporting And Automation

- Added static HTML reports, rebuildable SQLite indexes, and query commands for
  automation.
- Added CI regression tournament coverage plus manual Docker/Podman
  gateway-network smoke workflows.
- Added release, contribution, and security docs for pre-release repository
  hygiene.
