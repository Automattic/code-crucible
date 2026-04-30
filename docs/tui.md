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
- The first implementation includes a run dashboard: latest or selected run status, leaderboard rows, selected candidate detail, and common next actions. The dashboard defaults to a passed non-baseline candidate when one exists, and the candidate table is the picker for candidate-specific actions such as review and apply. Top-level actions use mnemonic goal labels: `[S] Start tournament`, `[R] Review selected candidate`, `[A] Apply selected candidate`, `[C] Continue tournament`, `[T] Test & score candidates`, `[G] Create more candidates`, `[E] Export report`, `[H] Command history`, and `[?] Advanced`.
- When no `.crucible/` work area exists, or the work area exists but has no run archives, the TUI opens directly into Start Tournament. Pressing Esc returns to the empty dashboard. If a run directory exists but cannot be loaded, the TUI stays on the dashboard and shows the load error instead of hiding it behind the form.
- Forms cover low-prompt tournament setup, discovery, test-harness generation, candidate creation, generated-candidate import, selected-candidate application, candidate testing/scoring, next-round preparation, tournament continuation, reports, index rebuilds, archive browsing, and candidate review. The new-run form defaults to `crucible run --auto --generate --evaluate`, so the operator can type only the improvement request and press Enter to create the run, generate and validate a test harness, record baseline metrics, create candidates, test and score them, and refresh the dashboard. Lower-frequency actions such as Discovery Only, Create Test Harness, Import Generated Candidates, Create Next Round, Refresh Archive Index, and Browse Archive live behind the Advanced menu. Forms use Bubbles text inputs, execute through the shared CLI controller, show a live spinner with elapsed time while actions run, allow cancellation requests for long-running actions, and return to the dashboard after completion instead of forcing a raw output screen.
- Press `h` from the dashboard to open the in-session command history. Each entry lists the selected form options before the generated CLI command, followed by completion status, duration, captured output byte counts, and cancellation artifact pointers when applicable.
- Cancellation is conservative: Code Crucible records a cancellation event and marks affected unevaluated evaluation candidates as `canceled`, but does not overwrite completed `passed` or `failed` results. Canceled generation records valid unadopted candidate artifacts as `canceled` and records partial candidate directories in the same event stream.
- Candidate selection uses the Bubbles table widget, and candidate details use a scrollable viewport.
- Keep tests focused on state transitions and command construction. Avoid terminal snapshot tests until the UI stabilizes.

## Deferred

- Exact screen layout and keybindings.
- A full async app shell with richer background job management, including logs, parallel jobs, and detailed progress displays.
