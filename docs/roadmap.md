# Roadmap

Code Crucible is still pre-stable. The near-term goal is to make optimization
tournaments boring to run: clear setup, reproducible archives, deterministic
evaluation, useful reports, and conservative promotion back into source trees.

## Active Priorities

1. **Workflow presets** - save project or user defaults for common low-prompt
   tournament settings.
2. **TUI job manager** - show background jobs, logs, cancellation, and richer
   progress without leaving the dashboard.
3. **Provider setup UX** - turn provider templates into a smoother install or
   marketplace-style flow.
4. **Pre-release hardening** - keep command output, docs, smoke tests, and
   archive expectations stable before tagging.

## Lower-Priority Follow-Ups

- Compare reports across multiple runs.
- Add protocol-specific external routing when a real target project needs it.
- Publish archive compatibility guidance once formats settle.

## Recently Completed

- Low-prompt `run --auto` setup.
- Default TUI entrypoint for bare `crucible`.
- Model-agnostic provider abstraction with Codex and command providers.
- Candidate promotion with dry-run previews and archived reports.
- Semantic checks, repeated evaluation sampling, and score explanations.
- Fixture-backed external policy modes and Docker/Podman `gateway-network`
  routing.
- Static HTML reports, rebuildable SQLite indexes, and query commands.

See [implementation-history.md](implementation-history.md) for more detail on
completed foundations.
