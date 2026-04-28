# TUI Framework Selection

Code Crucible will use Bubble Tea for the planned terminal UI.

## Decision

Use Charmbracelet's Bubble Tea as the primary TUI framework, with Bubbles for reusable widgets when the interface needs lists, tables, text inputs, viewports, progress displays, file pickers, or help views.

Do not add the dependency until the first TUI implementation slice starts. The current prompt-based interactive mode should remain dependency-light and continue to work without Bubble Tea. The first TUI entrypoint should be explicit, such as `crucible tui`, rather than replacing the bare `crucible` workflow.

## Rationale

- Bubble Tea is Go-native and follows a small model/update/view architecture that fits Code Crucible's stateful workflows.
- The project needs dashboard-like screens, forms, candidate lists, tables, progress/status views, and scrollable reports; Bubbles already provides these primitives.
- The architecture can keep command execution outside UI state by extracting reusable controllers first, then calling those controllers from both prompt mode and TUI mode.
- Bubble Tea apps take over terminal input and output, so logging and long-running command output need explicit handling. Code Crucible should route command logs to run archives and surface concise status in the TUI.

## Implementation Notes

- Add a new `crucible tui` command instead of replacing bare `crucible`.
- Use the existing `internal/cli.WorkflowController` from prompt mode and the future TUI so both interfaces share command construction and execution.
- Start with a read-mostly run dashboard: latest run status, leaderboard, candidate detail, and common next actions.
- Add forms after the dashboard for run creation, discovery, generation, evaluation, reports, and queries.
- Keep tests focused on state transitions and command construction. Avoid terminal snapshot tests until the UI stabilizes.

## Deferred

- Exact screen layout and keybindings.
- Whether the first TUI slice should import only Bubble Tea or include Bubbles/Lip Gloss immediately.
