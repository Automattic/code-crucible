package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/discovery"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
	"github.com/Automattic/code-crucible/internal/run"
)

type interactiveSession struct {
	in     *bufio.Reader
	stdout io.Writer
	stderr io.Writer
}

func runInteractive(stdin io.Reader, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "interactive failed: %v\n", err)
		return 1
	}
	absProject, err := filepath.Abs(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "interactive failed: %v\n", err)
		return 1
	}

	session := interactiveSession{
		in:     bufio.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
	}
	fmt.Fprintln(stdout, "Code Crucible")
	fmt.Fprintln(stdout)

	ok, inputOK := session.confirm(fmt.Sprintf("Use %s as the project directory?", absProject), true)
	if !inputOK {
		return 0
	}
	if !ok {
		answer, inputOK := session.ask(fmt.Sprintf("Project directory [%s]: ", absProject))
		if !inputOK {
			return 0
		}
		if strings.TrimSpace(answer) != "" {
			absProject, err = filepath.Abs(strings.TrimSpace(answer))
			if err != nil {
				fmt.Fprintf(stderr, "interactive failed: %v\n", err)
				return 1
			}
		}
	}

	if _, err := project.Load(absProject); err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "interactive failed: %v\n", err)
			return 1
		}
		ok, inputOK := session.confirm("No .crucible work area found. Create one?", true)
		if !inputOK || !ok {
			return 0
		}
		if _, err := project.Init(absProject, ""); err != nil {
			fmt.Fprintf(stderr, "init failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Initialized Code Crucible work area: %s\n\n", project.WorkDir(absProject))
		if code := session.newRunWizard(absProject); code != 0 {
			return code
		}
	} else {
		fmt.Fprintf(stdout, "Found Code Crucible work area: %s\n\n", project.WorkDir(absProject))
		session.printProjectStatus(absProject)
	}

	return session.menu(absProject)
}

func (s interactiveSession) menu(projectDir string) int {
	for {
		fmt.Fprintln(s.stdout)
		fmt.Fprintln(s.stdout, "Actions:")
		fmt.Fprintln(s.stdout, "  1. New run")
		fmt.Fprintln(s.stdout, "  2. Leaderboard")
		fmt.Fprintln(s.stdout, "  3. Generate competitors")
		fmt.Fprintln(s.stdout, "  4. Evaluate candidates")
		fmt.Fprintln(s.stdout, "  5. Evolve rounds")
		fmt.Fprintln(s.stdout, "  6. HTML report")
		fmt.Fprintln(s.stdout, "  7. Inspect candidate")
		fmt.Fprintln(s.stdout, "  8. Rebuild index")
		fmt.Fprintln(s.stdout, "  q. Quit")
		choice, ok := s.ask("Choose an action [q]: ")
		if !ok {
			return 0
		}
		switch strings.ToLower(strings.TrimSpace(choice)) {
		case "", "q", "quit", "exit":
			return 0
		case "1", "new", "run":
			if code := s.newRunWizard(projectDir); code != 0 {
				return code
			}
		case "2", "leaderboard", "scoreboard", "scores":
			if code := runLeaderboard([]string{"--project-dir", projectDir}, s.stdout, s.stderr); code != 0 {
				return code
			}
		case "3", "generate":
			if code := runGenerate([]string{"--project-dir", projectDir}, s.stdout, s.stderr); code != 0 {
				return code
			}
		case "4", "evaluate":
			if code := runEvaluate([]string{"--project-dir", projectDir}, s.stdout, s.stderr); code != 0 {
				return code
			}
		case "5", "evolve":
			rounds, ok := s.askInt("Rounds", 1)
			if !ok {
				return 0
			}
			parents, ok := s.askInt("Parents", defaultVariantCount)
			if !ok {
				return 0
			}
			if code := runEvolve([]string{"--project-dir", projectDir, "--rounds", strconv.Itoa(rounds), "--parents", strconv.Itoa(parents)}, s.stdout, s.stderr); code != 0 {
				return code
			}
		case "6", "report":
			if code := runReport([]string{"--project-dir", projectDir}, s.stdout, s.stderr); code != 0 {
				return code
			}
		case "7", "inspect":
			candidateID, ok := s.ask("Candidate ID [candidate-0000-baseline]: ")
			if !ok {
				return 0
			}
			candidateID = strings.TrimSpace(candidateID)
			if candidateID == "" {
				candidateID = "candidate-0000-baseline"
			}
			if code := runInspect([]string{"--project-dir", projectDir, candidateID}, s.stdout, s.stderr); code != 0 {
				return code
			}
		case "8", "index":
			if code := runIndex([]string{"--project-dir", projectDir}, s.stdout, s.stderr); code != 0 {
				return code
			}
		default:
			fmt.Fprintf(s.stdout, "Unknown action %q\n", choice)
		}
	}
}

func (s interactiveSession) newRunWizard(projectDir string) int {
	fmt.Fprintln(s.stdout, "New optimization run")
	request, ok := s.askRequired("Optimization request: ")
	if !ok {
		return 0
	}
	variants, ok := s.askInt("Variants", defaultVariantCount)
	if !ok {
		return 0
	}

	plan, err := discovery.CreatePlan(discovery.PlanOptions{
		ProjectDir: projectDir,
		Optimize:   request,
		Limit:      defaultVariantCount,
	})
	if err != nil {
		fmt.Fprintf(s.stderr, "discover failed: %v\n", err)
		return 1
	}
	sourcePath := ""
	if len(plan.Suggestions) > 0 {
		fmt.Fprintln(s.stdout)
		fmt.Fprintf(s.stdout, "Discovery plan: %s\n", plan.PlanPath)
		fmt.Fprintln(s.stdout, "Suggested source paths:")
		for _, suggestion := range plan.Suggestions {
			fmt.Fprintf(s.stdout, "- %s (score %.1f): %s\n", suggestion.Path, suggestion.Score, suggestion.Reason)
		}
		useSuggestion, ok := s.confirm(fmt.Sprintf("Use %s as the baseline source path?", plan.Suggestions[0].Path), false)
		if !ok {
			return 0
		}
		if useSuggestion {
			sourcePath = plan.Suggestions[0].Path
		}
	} else {
		fmt.Fprintf(s.stdout, "Discovery plan: %s\n", plan.PlanPath)
		fmt.Fprintln(s.stdout, "No source path suggestion found. Generation will ask the agent to discover the involved code.")
	}

	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     request,
		SourcePath:   sourcePath,
		Variants:     variants,
		ExternalMode: "deny",
	})
	if err != nil {
		fmt.Fprintf(s.stderr, "run setup failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(s.stdout, "\nCreated run %s\n", created.ID)
	fmt.Fprintf(s.stdout, "Run directory: %s\n", created.RunDir)
	fmt.Fprintf(s.stdout, "Interface docs: %s\n", created.InterfaceDocPath)
	fmt.Fprintf(s.stdout, "Generation prompt: %s\n", created.PromptPath)
	fmt.Fprintf(s.stdout, "Baseline source: %s\n", created.BaselineSourceDir)
	if sourcePath == "" {
		fmt.Fprintln(s.stdout, "Source path: agent discovery pending")
	} else {
		fmt.Fprintf(s.stdout, "Source path: %s\n", sourcePath)
	}
	fmt.Fprintln(s.stdout)
	return runLeaderboard([]string{"--project-dir", projectDir}, s.stdout, s.stderr)
}

func (s interactiveSession) printProjectStatus(projectDir string) {
	runDir, err := archive.LatestRunDir(projectDir)
	if err != nil {
		fmt.Fprintln(s.stdout, "No runs yet.")
		return
	}
	cfg, err := archive.LoadRunConfig(filepath.Join(runDir, "run.json"))
	if err != nil {
		fmt.Fprintf(s.stdout, "Latest run: %s\n", filepath.Base(runDir))
		return
	}
	board, err := archive.LoadLeaderboard(filepath.Join(runDir, "leaderboard.json"))
	if err != nil {
		fmt.Fprintf(s.stdout, "Latest run: %s\n", cfg.ID)
		return
	}
	passed, failed, pending := candidateStatusCounts(board.Results)
	best := bestPassedCandidate(board.Results)
	fmt.Fprintf(s.stdout, "Latest run: %s\n", cfg.ID)
	fmt.Fprintf(s.stdout, "Request: %s\n", oneLine(cfg.Optimize))
	fmt.Fprintf(s.stdout, "Active round: %s\n", filepath.Base(archive.ProjectPath(projectDir, cfg.RoundDir)))
	fmt.Fprintf(s.stdout, "Candidates: %d passed, %d failed, %d pending/generated\n", passed, failed, pending)
	if best != "" {
		fmt.Fprintf(s.stdout, "Best candidate: %s\n", best)
	}
}

func candidateStatusCounts(results []model.CandidateResult) (int, int, int) {
	var passed, failed, pending int
	for _, result := range results {
		switch result.Status {
		case "passed":
			passed++
		case "failed":
			failed++
		default:
			pending++
		}
	}
	return passed, failed, pending
}

func bestPassedCandidate(results []model.CandidateResult) string {
	bestID := ""
	bestScore := 0.0
	for _, result := range results {
		if result.Status != "passed" {
			continue
		}
		if bestID == "" || result.Score > bestScore {
			bestID = result.Candidate.ID
			bestScore = result.Score
		}
	}
	return bestID
}

func (s interactiveSession) askRequired(prompt string) (string, bool) {
	for {
		answer, ok := s.ask(prompt)
		if !ok {
			return "", false
		}
		answer = strings.TrimSpace(answer)
		if answer != "" {
			return answer, true
		}
		fmt.Fprintln(s.stdout, "Please enter a value.")
	}
}

func (s interactiveSession) askInt(label string, fallback int) (int, bool) {
	for {
		answer, ok := s.ask(fmt.Sprintf("%s [%d]: ", label, fallback))
		if !ok {
			return 0, false
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return fallback, true
		}
		value, err := strconv.Atoi(answer)
		if err == nil && value > 0 {
			return value, true
		}
		fmt.Fprintf(s.stdout, "%s must be a positive integer.\n", label)
	}
}

func (s interactiveSession) confirm(prompt string, fallback bool) (bool, bool) {
	suffix := " [Y/n]: "
	if !fallback {
		suffix = " [y/N]: "
	}
	for {
		answer, ok := s.ask(prompt + suffix)
		if !ok {
			return false, false
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		switch answer {
		case "":
			return fallback, true
		case "y", "yes":
			return true, true
		case "n", "no":
			return false, true
		default:
			fmt.Fprintln(s.stdout, "Please answer yes or no.")
		}
	}
}

func (s interactiveSession) ask(prompt string) (string, bool) {
	fmt.Fprint(s.stdout, prompt)
	answer, err := s.in.ReadString('\n')
	if err != nil && len(answer) == 0 {
		if err != io.EOF {
			fmt.Fprintf(s.stderr, "read input failed: %v\n", err)
		}
		return "", false
	}
	return strings.TrimRight(answer, "\r\n"), true
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
