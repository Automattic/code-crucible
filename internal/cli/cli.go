package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/gaarai/code-crucible/internal/archive"
	"github.com/gaarai/code-crucible/internal/project"
	"github.com/gaarai/code-crucible/internal/run"
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
	case "leaderboard":
		return runLeaderboard(args[1:], stdout, stderr)
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := fs.String("project", ".", "project directory to initialize")
	name := fs.String("name", "", "project name")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := project.Init(*projectDir, *name)
	if err != nil {
		fmt.Fprintf(stderr, "init failed: %v\n", err)
		return 1
	}

	abs, _ := filepath.Abs(*projectDir)
	fmt.Fprintf(stdout, "Initialized Code Crucible work area for %s\n", cfg.ProjectName)
	fmt.Fprintf(stdout, "Work area: %s\n", project.WorkDir(abs))
	return 0
}

func runTournament(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := fs.String("project", ".", "project directory containing or receiving .crucible")
	optimize := fs.String("optimize", "", "feature, function, or behavior to optimize")
	targetPath := fs.String("target-path", "", "optional file or directory to use as the initial baseline source")
	agent := fs.String("agent", "", "agent provider name")
	variants := fs.Int("variants", 3, "number of new competitors to request per round")
	rounds := fs.Int("rounds", 1, "number of tournament rounds to prepare")
	exploration := fs.Float64("exploration", 0.35, "0..1 balance between iterative improvement and creative alternatives")
	evaluator := fs.String("evaluator", "", "deterministic evaluator command to run from each candidate src directory")
	externalMode := fs.String("external-mode", "deny", "external call mode: deny, allowlist, mock, replay, record")
	fixtures := fs.String("external-fixtures", "", "fixtures path for mock or replay mode")
	allowHosts := fs.String("allow-hosts", "", "comma-separated host allowlist")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	created, err := run.Create(run.Options{
		ProjectDir:   *projectDir,
		Optimize:     *optimize,
		TargetPath:   *targetPath,
		Agent:        *agent,
		Variants:     *variants,
		Rounds:       *rounds,
		Exploration:  *exploration,
		Evaluator:    *evaluator,
		ExternalMode: *externalMode,
		Fixtures:     *fixtures,
		AllowHosts:   splitCSV(*allowHosts),
	})
	if err != nil {
		fmt.Fprintf(stderr, "run setup failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Created run %s\n", created.ID)
	fmt.Fprintf(stdout, "Run directory: %s\n", created.RunDir)
	fmt.Fprintf(stdout, "Interface docs: %s\n", created.InterfaceDocPath)
	fmt.Fprintf(stdout, "Generation prompt: %s\n", created.PromptPath)
	fmt.Fprintf(stdout, "Baseline source: %s\n", created.BaselineSourceDir)
	return 0
}

func runLeaderboard(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("leaderboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := fs.String("project", ".", "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	jsonOut := fs.Bool("json", false, "print raw leaderboard JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path, err := archive.LeaderboardPath(*projectDir, *runID)
	if err != nil {
		fmt.Fprintf(stderr, "leaderboard failed: %v\n", err)
		return 1
	}
	board, err := archive.LoadLeaderboard(path)
	if err != nil {
		fmt.Fprintf(stderr, "leaderboard failed: %v\n", err)
		return 1
	}

	if *jsonOut {
		data, err := json.MarshalIndent(board, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "leaderboard failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}

	fmt.Fprintf(stdout, "Run: %s\n", board.RunID)
	fmt.Fprintf(stdout, "Optimize: %s\n\n", board.Optimize)
	fmt.Fprintf(stdout, "%-28s %-10s %-10s %-12s %-10s\n", "Candidate", "Status", "Score", "P95 ms", "Calls")
	for _, result := range board.Results {
		fmt.Fprintf(stdout, "%-28s %-10s %-10.2f %-12.2f %-10d\n",
			result.Candidate.ID,
			result.Status,
			result.Score,
			result.Metrics.P95LatencyMS,
			result.Metrics.ExternalCallCount,
		)
	}
	return 0
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := fs.String("project", ".", "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path, err := archive.LeaderboardPath(*projectDir, *runID)
	if err != nil {
		fmt.Fprintf(stderr, "inspect failed: %v\n", err)
		return 1
	}
	board, err := archive.LoadLeaderboard(path)
	if err != nil {
		fmt.Fprintf(stderr, "inspect failed: %v\n", err)
		return 1
	}

	candidateID := ""
	if fs.NArg() > 0 {
		candidateID = fs.Arg(0)
	}

	if candidateID == "" {
		fmt.Fprintf(stdout, "Run: %s\n", board.RunID)
		fmt.Fprintf(stdout, "Candidates: %d\n", len(board.Results))
		fmt.Fprintf(stdout, "Leaderboard: %s\n", path)
		return 0
	}

	for _, result := range board.Results {
		if result.Candidate.ID == candidateID {
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				fmt.Fprintf(stderr, "inspect failed: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "%s\n", data)
			return 0
		}
	}
	fmt.Fprintf(stderr, "candidate %q was not found in %s\n", candidateID, board.RunID)
	return 1
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `Code Crucible

Usage:
  crucible init [--project DIR] [--name NAME]
  crucible run --optimize TEXT [--project DIR] [--target-path PATH] [--variants N]
  crucible leaderboard [--project DIR] [--run RUN_ID] [--json]
  crucible inspect [--project DIR] [--run RUN_ID] [candidate-id]
  crucible version

Core workflow:
  1. Run "crucible init" inside an existing project.
  2. Run "crucible run --optimize ..." to create a tournament workspace.
  3. Fill in docs/interfaces.md and evaluator/evaluator.sh.
  4. Feed prompts/generation-round-0001.md to the selected agent.
  5. Add competitor results to leaderboard.json as evaluation matures.

`)
}
