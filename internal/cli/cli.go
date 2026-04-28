package cli

import (
	"fmt"
	"io"
)

const version = "0.1.0"

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
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
	case "run":
		return runTournament(args[1:], stdout, stderr)
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
  crucible init [--project DIR] [--name NAME]
  crucible run (--optimize TEXT | --task-file PATH) [--project DIR] [--target-path PATH] [--variants N] [--generate]
  crucible generate [--project DIR] [--run RUN_ID] [--agent codex]
  crucible adopt [--project DIR] [--run RUN_ID]
  crucible evaluate [--project DIR] [--run RUN_ID] [--candidate ID] [--jobs N] [--nice N] [--cpu-limit N] [--sandbox-engine docker|podman --sandbox-image IMAGE]
  crucible next-round [--project DIR] [--run RUN_ID] [--parents N]
  crucible evolve [--project DIR] [--run RUN_ID] [--rounds N] [--parents N]
  crucible leaderboard [--project DIR] [--run RUN_ID] [--json]
  crucible index [--project DIR] [--run RUN_ID] [--json]
  crucible inspect [--project DIR] [--run RUN_ID] [candidate-id]
  crucible version

Core workflow:
  1. Run "crucible init" inside an existing project.
  2. Run "crucible run --optimize ..." or "crucible run --task-file ..." to create a tournament workspace.
  3. Fill in docs/interfaces.md and evaluator/evaluator.sh.
  4. Run "crucible generate --agent codex" to ask Codex for competitors, or use "crucible run --generate" as an explicit shortcut.
  5. Run "crucible evaluate" to execute the run evaluator and update leaderboard results.
  6. Run "crucible next-round" to prepare the next generation prompt from passed candidates.
  7. Run "crucible evolve --rounds N" to automate generate/evaluate/next-round cycles.
  8. Run "crucible index" to rebuild the SQLite summary from filesystem artifacts.

`)
}
