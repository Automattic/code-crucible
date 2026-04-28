# TUI Framework Selection

Code Crucible uses Bubble Tea for the terminal UI.

## Decision

Use Charmbracelet's Bubble Tea as the primary TUI framework, with Bubbles for reusable widgets when the interface needs lists, tables, text inputs, viewports, progress displays, file pickers, or help views.

Code Crucible uses Bubble Tea v1.2.x for the first TUI slice because it supports the project's Go 1.22 baseline. Newer Bubble Tea releases currently require a newer Go toolchain. The prompt-based interactive mode remains available through bare `crucible`, and the TUI entrypoint is explicit: `crucible tui`.

## Rationale

- Bubble Tea is Go-native and follows a small model/update/view architecture that fits Code Crucible's stateful workflows.
- The project needs dashboard-like screens, forms, candidate lists, tables, progress/status views, and scrollable reports; Bubbles already provides these primitives.
- The architecture can keep command execution outside UI state by extracting reusable controllers first, then calling those controllers from both prompt mode and TUI mode.
- Bubble Tea apps take over terminal input and output, so logging and long-running command output need explicit handling. Code Crucible should route command logs to run archives and surface concise status in the TUI.

## Implementation Notes

- Keep `crucible tui` as the explicit entrypoint instead of replacing bare `crucible`.
- Use the existing `internal/cli.WorkflowController` from prompt mode and the future TUI so both interfaces share command construction and execution.
- The first implementation includes a run dashboard: latest or selected run status, leaderboard rows, selected candidate detail, and common next action commands.
- Basic forms now cover run creation, discovery, generation, evaluation, reports, and archive queries. They execute through the shared CLI controller and show captured output after completion.
- Keep tests focused on state transitions and command construction. Avoid terminal snapshot tests until the UI stabilizes.

## Deferred

- Exact screen layout and keybindings.
- Whether the forms should add Bubbles widgets immediately or continue with plain Bubble Tea state until the interaction model stabilizes.
