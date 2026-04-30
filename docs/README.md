# Code Crucible Docs

The root [README](../README.md) explains what Code Crucible is and why it
exists. This directory holds the developer, operator, archive-format, and design
references.

## Start Here

| Document | Purpose |
|---|---|
| [Getting started](getting-started.md) | Requirements, install from source, first tournament, and provider setup. |
| [CLI reference](cli-reference.md) | Common command flows for discovery, run creation, generation, evaluation, reports, and promotion. |
| [Architecture](architecture.md) | Project mode, archive layout, provider model, scoring, reports, and package responsibilities. |
| [Evaluation](evaluation.md) | Evaluator contract, semantic checks, repeated measurements, sandboxing, resource metrics, and external policy enforcement. |
| [Candidate format](candidate-format.md) | Required candidate files, adoption rules, promotion behavior, evaluation artifacts, and score explanation fields. |

## External Calls And Sandboxes

| Document | Purpose |
|---|---|
| [External fixtures](external-fixtures.md) | HTTP fixture schema, gateway environment, replay behavior, and trace files. |
| [Container raw socket routing](container-raw-socket-routing.md) | Docker/Podman gateway-network design for clients that ignore proxy variables. |

Code Crucible currently uses Docker and Podman as evaluator runtimes. The
repository does not ship application Dockerfiles or Compose files; container
runtime behavior is configured through CLI flags and Make targets.

## User Interfaces

| Document | Purpose |
|---|---|
| [TUI](tui.md) | Bubble Tea selection, dashboard behavior, forms, history, and deferred UI work. |

## Project Maintenance

| Document | Purpose |
|---|---|
| [Contributing](../.github/CONTRIBUTING.md) | Local development workflow, validation commands, artifact hygiene, and commit style. |
| [Security](../.github/SECURITY.md) | Reporting guidance, sensitive archive handling, and execution caveats. |
| [Release checklist](release-checklist.md) | Pre-release validation and publication checks. |
| [Roadmap](roadmap.md) | Active priorities and deferred ideas. |
| [Implementation history](implementation-history.md) | Major shipped foundations and why the current system has its shape. |

## Examples

| Path | Purpose |
|---|---|
| [examples/go-ranking-poc](../examples/go-ranking-poc) | Small Go ranking function used by `make smoke`. |
| [examples/gateway-network-poc](../examples/gateway-network-poc) | Fixture-backed Docker/Podman gateway-network proof of concept. |
| [examples/agent-providers](../examples/agent-providers) | Config snippets for command-based generation providers. |
