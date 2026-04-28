# Security Policy

Code Crucible is an early-stage local CLI for generating and evaluating code competitors. Treat run archives and agent transcripts as potentially sensitive project data.

## Supported Versions

No stable release line exists yet. Security fixes currently land on the `trunk` branch.

## Reporting Security Issues

Until a dedicated security contact is published, please report security concerns through a private GitHub security advisory on the repository once the remote is available. If that is not available, contact the maintainer privately before opening a public issue.

## Sensitive Artifact Guidance

Do not commit local `.crucible/` directories from host projects unless you have reviewed the contents. Run archives may include:

- Copies of source code from the evaluated project
- Agent prompts and transcripts
- Evaluator logs and benchmark output
- External policy data, hostnames, fixtures, or replay traces
- Generated competitor implementations

The repository `.gitignore` excludes `.crucible/`, `.codex`, build outputs, logs, env files, and common private key formats by default. Keep project-specific credentials outside evaluator scripts and pass local-only values with environment variables when needed.

## External Execution

Evaluators and generated competitors should still be treated as local code execution unless they are run in an explicit Docker or Podman sandbox. Container evaluation can enforce `deny` mode with `--sandbox-network none`; local evaluation only warns for `deny` because Code Crucible cannot isolate host-network access around arbitrary local evaluator scripts.

Fixture-backed `mock` and `replay` modes provide proxy and trust environment variables and start the archived fixture gateway for evaluator runs. This is deterministic for HTTP clients that honor standard proxy and trust settings, but raw sockets, custom transports, and unsupported protocols still need evaluator-specific handling. Review evaluator scripts before running them against sensitive projects, and prefer `--jobs 1`, `--nice 10`, and explicit `--timeout` values while the execution layer is still maturing.
