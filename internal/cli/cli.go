package cli

import (
	"fmt"
	"io"
)

const version = "0.1.0"

func Run(args []string, stdout, stderr io.Writer) int {
	return RunWithIO(args, nil, stdout, stderr)
}

func RunWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		if stdin != nil {
			return runInteractive(stdin, stdout, stderr)
		}
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHelp(stdout)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "crucible %s\n", version)
		return 0
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "provider", "providers":
		return runProvider(args[1:], stdout, stderr)
	case "evaluator", "evaluators":
		return runEvaluator(args[1:], stdout, stderr)
	case "tui":
		return runTUI(args[1:], stdin, stdout, stderr)
	case "run":
		return runTournament(args[1:], stdout, stderr)
	case "discover":
		return runDiscover(args[1:], stdout, stderr)
	case "generate":
		return runGenerate(args[1:], stdout, stderr)
	case "adopt":
		return runAdopt(args[1:], stdout, stderr)
	case "evaluate":
		return runEvaluate(args[1:], stdout, stderr)
	case "next-round":
		return runNextRound(args[1:], stdout, stderr)
	case "evolve":
		return runEvolve(args[1:], stdout, stderr)
	case "leaderboard":
		return runLeaderboard(args[1:], stdout, stderr)
	case "index":
		return runIndex(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	case "query":
		return runQuery(args[1:], stdout, stderr)
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `Code Crucible

Usage:
  crucible
  crucible tui [--project-dir DIR] [--run RUN_ID]
  crucible init [--project-dir DIR] [--name NAME] [--default-agent AGENT]
  crucible provider template PROVIDER [--json]
  crucible provider list
  crucible discover "OPTIMIZATION REQUEST" [--project-dir DIR] [--agent local|codex]
  crucible evaluator generate [--project-dir DIR] [--run RUN_ID] [--agent AGENT] [--dry-run]
  crucible run "OPTIMIZATION REQUEST" [--project-dir DIR] [--source-path PATH] [--agent AGENT] [--agent-plan PATH] [--variants N] [--generate]
  crucible run (--optimize TEXT | --task-file PATH) [--project-dir DIR] [--source-path PATH] [--agent AGENT] [--agent-plan PATH] [--variants N] [--generate]
  crucible generate [--project-dir DIR] [--run RUN_ID] [--agent AGENT]
  crucible adopt [--project-dir DIR] [--run RUN_ID]
  crucible evaluate [--project-dir DIR] [--run RUN_ID] [--candidate ID] [--jobs N] [--warmups N] [--repetitions N] [--outliers MODE] [--sample-stat STAT] [--nice N] [--cpu-limit N] [--sandbox-profile PROFILE] [--sandbox-engine docker|podman --sandbox-image IMAGE] [--external-routing gateway-network] [--require-passed]
  crucible next-round [--project-dir DIR] [--run RUN_ID] [--parents N]
  crucible evolve [--project-dir DIR] [--run RUN_ID] [--rounds N] [--parents N] [--agent AGENT]
  crucible leaderboard [--project-dir DIR] [--run RUN_ID] [--json]
  crucible index [--project-dir DIR] [--run RUN_ID] [--json]
  crucible query runs [--project-dir DIR] [--limit N] [--json]
  crucible query candidates [--project-dir DIR] [--run RUN_ID] [--status STATUS] [--limit N] [--json]
  crucible report [--project-dir DIR] [--run RUN_ID] [--output PATH] [--json]
  crucible inspect [--project-dir DIR] [--run RUN_ID] [candidate-id]
  crucible version

Core workflow:
  1. Run "crucible" for the guided workflow, or run "crucible run \"make this feature faster\"" directly.
  2. Run "crucible provider template claude --json" to inspect optional non-Codex provider config.
  3. Run "crucible discover \"make this feature faster\" --agent codex" when the source path or evaluator contract needs discovery.
  4. Run "crucible evaluator generate" or supply your own evaluator before trusting tournament scores.
  5. Review or edit docs/interfaces.md and evaluator/evaluator.sh in the run archive.
  6. Run "crucible evaluate --candidate candidate-0000-baseline" to measure a supplied-evaluator baseline or rerun changed evaluator logic.
  7. Run "crucible generate --agent codex" to ask Codex for competitors, or use "crucible run --generate" as an explicit shortcut.
  8. Run "crucible evaluate" to execute the run evaluator and update leaderboard results.
  9. Run "crucible leaderboard" or "crucible report" to inspect results.
  10. Run "crucible next-round" or "crucible evolve --rounds N" for follow-up rounds.

`)
}
