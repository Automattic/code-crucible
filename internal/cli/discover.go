package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/discovery"
)

func runDiscover(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing or receiving .crucible")
	optimize := fs.String("optimize", "", "feature, function, or behavior to optimize")
	taskFile := fs.String("task-file", "", "path to a file containing the optimization task, relative to project directory")
	limit := fs.Int("limit", discovery.DefaultSuggestionLimit, "maximum source path suggestions to include")
	agentName := fs.String("agent", "local", "discovery agent: local or codex")
	codexBin := fs.String("codex-bin", agent.DefaultCodexBinary, "Codex CLI binary")
	model := fs.String("model", "", "Codex model override")
	profile := fs.String("profile", "", "Codex config profile")
	sandbox := fs.String("sandbox", agent.DefaultCodexSandbox, "Codex sandbox mode")
	approval := fs.String("approval", agent.DefaultApprovalPolicy, "Codex approval policy")
	eventJSON := fs.Bool("event-json", true, "ask Codex to emit JSONL events")
	skipGitRepoCheck := fs.Bool("skip-git-repo-check", true, "allow Codex to run when the host project is not a git repository")
	dryRun := fs.Bool("dry-run", false, "print the Codex invocation without running it")
	if err := fs.Parse(flagsAnywhere(args, fs)); err != nil {
		return 2
	}

	request, code := optimizationRequest(*projectDir, strings.TrimSpace(*optimize), strings.TrimSpace(*taskFile), strings.TrimSpace(strings.Join(fs.Args(), " ")), stderr)
	if code != 0 {
		return code
	}
	if *limit < 1 {
		fmt.Fprintf(stderr, "discover failed: --limit must be at least 1\n")
		return 2
	}

	plan, err := discovery.CreatePlan(discovery.PlanOptions{
		ProjectDir: *projectDir,
		Optimize:   request,
		Limit:      *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "discover failed: %v\n", err)
		return 1
	}

	if *agentName == "" {
		*agentName = "local"
	}
	switch *agentName {
	case "local":
		printDiscoveryPlan(stdout, plan)
		return 0
	case "codex":
		return runCodexDiscovery(*projectDir, plan, codexDiscoveryOptions{
			CodexBin:         *codexBin,
			Model:            *model,
			Profile:          *profile,
			Sandbox:          *sandbox,
			Approval:         *approval,
			EventJSON:        *eventJSON,
			SkipGitRepoCheck: *skipGitRepoCheck,
			DryRun:           *dryRun,
		}, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "discover failed: unsupported --agent %q\n", *agentName)
		return 2
	}
}

type codexDiscoveryOptions struct {
	CodexBin         string
	Model            string
	Profile          string
	Sandbox          string
	Approval         string
	EventJSON        bool
	SkipGitRepoCheck bool
	DryRun           bool
}

func runCodexDiscovery(projectDir string, plan *discovery.Plan, opts codexDiscoveryOptions, stdout, stderr io.Writer) int {
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		fmt.Fprintf(stderr, "discover failed: %v\n", err)
		return 1
	}
	planDir := archive.ProjectPath(absProject, plan.PlanDir)
	promptPath := archive.ProjectPath(absProject, plan.PromptPath)
	outputLastMessage := filepath.Join(planDir, "agent-plan.md")
	agentPlanPath := discovery.AgentPlanPath(planDir)
	codexOpts := agent.CodexOptions{
		Binary:            opts.CodexBin,
		ProjectDir:        absProject,
		RunDir:            planDir,
		PromptPath:        promptPath,
		Model:             opts.Model,
		Profile:           opts.Profile,
		Sandbox:           opts.Sandbox,
		ApprovalPolicy:    opts.Approval,
		OutputLastMessage: outputLastMessage,
		JSONEvents:        opts.EventJSON,
		SkipGitRepoCheck:  opts.SkipGitRepoCheck,
	}
	command := agent.BuildCodexExecCommand(codexOpts)
	if opts.DryRun {
		printDiscoveryPlan(stdout, plan)
		fmt.Fprintf(stdout, "\nCodex command:\n%s\n\n", agent.FormatCommand(command))
		fmt.Fprintf(stdout, "Prompt stdin: %s\n", promptPath)
		fmt.Fprintf(stdout, "Project root: %s\n", absProject)
		fmt.Fprintf(stdout, "Discovery archive: %s\n", planDir)
		fmt.Fprintf(stdout, "Agent plan: %s\n", outputLastMessage)
		fmt.Fprintf(stdout, "Structured plan: %s\n", agentPlanPath)
		return 0
	}
	result, err := agent.RunCodex(context.Background(), codexOpts, stdout, stderr)
	if err != nil {
		if result != nil {
			fmt.Fprintf(stderr, "discover failed: Codex exited with status %d\n", result.ExitCode)
		} else {
			fmt.Fprintf(stderr, "discover failed: %v\n", err)
		}
		return 1
	}
	printDiscoveryPlan(stdout, plan)
	fmt.Fprintf(stdout, "Codex discovery complete\n")
	fmt.Fprintf(stdout, "Agent plan: %s\n", result.FinalPath)
	if parsed, structuredPath, err := loadOrExtractAgentPlan(agentPlanPath, result.FinalPath); err != nil {
		fmt.Fprintf(stderr, "discover warning: structured agent plan was not captured: %v\n", err)
	} else {
		fmt.Fprintf(stdout, "Structured plan: %s\n", structuredPath)
		printStructuredAgentPlanSummary(stdout, parsed)
	}
	fmt.Fprintf(stdout, "Invocation: %s\n", result.InvocationPath)
	return 0
}

func loadOrExtractAgentPlan(agentPlanPath, finalPath string) (*discovery.AgentPlan, string, error) {
	plan, err := discovery.LoadAgentPlan(agentPlanPath)
	if err == nil {
		return plan, agentPlanPath, nil
	}
	loadErr := err
	plan, err = discovery.ExtractAgentPlanFile(finalPath, agentPlanPath)
	if err != nil {
		if os.IsNotExist(loadErr) {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("read %s: %v; extract from %s: %w", agentPlanPath, loadErr, finalPath, err)
	}
	return plan, agentPlanPath, nil
}

func printStructuredAgentPlanSummary(stdout io.Writer, plan *discovery.AgentPlan) {
	if plan == nil {
		return
	}
	if strings.TrimSpace(plan.SourcePath) != "" {
		fmt.Fprintf(stdout, "Recommended source path: %s", plan.SourcePath)
		if strings.TrimSpace(plan.SourcePathConfidence) != "" {
			fmt.Fprintf(stdout, " (%s confidence)", plan.SourcePathConfidence)
		}
		fmt.Fprintln(stdout)
	}
	if len(plan.ClarifyingQuestions) > 0 {
		fmt.Fprintf(stdout, "Clarifying questions: %d\n", len(plan.ClarifyingQuestions))
	}
	if strings.TrimSpace(plan.SuggestedNextCommand) != "" {
		fmt.Fprintf(stdout, "Suggested next command: %s\n", plan.SuggestedNextCommand)
	}
}

func printDiscoveryPlan(stdout io.Writer, plan *discovery.Plan) {
	fmt.Fprintf(stdout, "Created discovery plan %s\n", plan.ID)
	fmt.Fprintf(stdout, "Discovery directory: %s\n", plan.PlanDir)
	fmt.Fprintf(stdout, "Plan: %s\n", plan.PlanPath)
	fmt.Fprintf(stdout, "Agent prompt: %s\n", plan.PromptPath)
	if len(plan.Suggestions) == 0 {
		fmt.Fprintf(stdout, "Suggested source paths: none\n")
		return
	}
	fmt.Fprintf(stdout, "Suggested source paths:\n")
	for _, suggestion := range plan.Suggestions {
		fmt.Fprintf(stdout, "- %s (score %.1f): %s\n", suggestion.Path, suggestion.Score, suggestion.Reason)
	}
}
