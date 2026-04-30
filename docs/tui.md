# TUI Framework Selection

Code Crucible uses Bubble Tea for the terminal UI.

## Decision

Use Charmbracelet's Bubble Tea as the primary TUI framework, with Bubbles for reusable widgets when the interface needs lists, tables, text inputs, viewports, progress displays, file pickers, or help views.

Code Crucible uses Bubble Tea v1.2.x and Bubbles v0.20.x because that pair supports the project's Go 1.22 baseline. Newer Bubbles releases currently pull in a newer Bubble Tea dependency chain and raise the Go directive. The TUI is the default interactive entrypoint for bare `crucible`, `crucible tui` remains an explicit alias with options, and the prompt-based interactive mode remains available through `crucible prompt`.

## Rationale

- Bubble Tea is Go-native and follows a small model/update/view architecture that fits Code Crucible's stateful workflows.
- The project needs dashboard-like screens, forms, candidate lists, tables, progress/status views, and scrollable reports; Bubbles already provides these primitives.
- The architecture can keep command execution outside UI state by extracting reusable controllers first, then calling those controllers from both prompt mode and TUI mode.
- Bubble Tea apps take over terminal input and output, so logging and long-running command output need explicit handling. Code Crucible should route command logs to run archives and surface concise status in the TUI.

## Implementation Notes

- Launch the TUI from bare `crucible`, keep `crucible tui` as an explicit alias, and preserve the line-oriented prompt workflow as `crucible prompt`.
- Use the existing `internal/cli.WorkflowController` from prompt mode and the TUI so both interfaces share command construction and execution.
- The first implementation includes a run dashboard: latest or selected run status, leaderboard rows, selected candidate detail, and common next actions. The dashboard defaults to a passed non-baseline candidate when one exists, and the candidate table is the picker for candidate-specific actions such as inspect. Actions that have TUI forms are shown as key-first prompts; workflows that are not yet implemented in the TUI use short CLI hints instead of long absolute commands.
- Forms cover low-prompt auto run setup, discovery, evaluator generation, generation, adoption, promotion, evaluation, next-round preparation, evolution, reports, index rebuilds, archive queries, and inspection. The new-run form defaults to `crucible run --auto`, so the operator can type only the optimization request and press Enter to create the run, generate and validate an evaluator, record baseline metrics, and refresh the dashboard. Forms use Bubbles text inputs, execute through the shared CLI controller, show a live spinner with elapsed time while actions run, allow cancellation requests for long-running actions, and return to the dashboard after completion instead of forcing a raw output screen.
- Press `h` from the dashboard to open the in-session command history. Each entry lists the selected form options before the generated CLI command, followed by completion status, duration, captured output byte counts, and cancellation artifact pointers when applicable.
- Cancellation is conservative: Code Crucible records a cancellation event and marks affected unevaluated evaluation candidates as `canceled`, but does not overwrite completed `passed` or `failed` results. Canceled generation records valid unadopted candidate artifacts as `canceled` and records partial candidate directories in the same event stream.
- Candidate selection uses the Bubbles table widget, and candidate details use a scrollable viewport.
- Keep tests focused on state transitions and command construction. Avoid terminal snapshot tests until the UI stabilizes.

## Deferred

- Exact screen layout and keybindings.
- A full async app shell with richer background job management, including logs, parallel jobs, and detailed progress displays.
