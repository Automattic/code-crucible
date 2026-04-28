package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/run"
)

type evaluatorGenerationOptions struct {
	Context           context.Context
	ProjectDir        string
	RunID             string
	AgentName         string
	CodexBin          string
	Model             string
	Profile           string
	Sandbox           string
	Approval          string
	EventJSON         bool
	SkipGitRepoCheck  bool
	OutputLastMessage string
	ValidationTimeout time.Duration
	DryRun            bool
}

func runEvaluator(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printEvaluatorHelp(stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		printEvaluatorHelp(stdout)
		return 0
	case "generate":
		return runEvaluatorGenerate(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown evaluator command %q\n\n", args[0])
		printEvaluatorHelp(stderr)
		return 2
	}
}

func printEvaluatorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  crucible evaluator generate [--project-dir DIR] [--run RUN_ID] [--agent AGENT] [--dry-run]

Options:
  --validation-timeout DURATION  Baseline validation timeout after generation.

`)
}

func runEvaluatorGenerate(args []string, stdout, stderr io.Writer) int {
	return runEvaluatorGenerateWithContext(context.Background(), args, stdout, stderr)
}

func runEvaluatorGenerateWithContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("evaluator generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	agentName := fs.String("agent", "", "agent provider to run; defaults to the run or project default")
	codexBin := fs.String("codex-bin", agent.DefaultCodexBinary, "Codex CLI binary")
	modelName := fs.String("model", "", "agent model override")
	profile := fs.String("profile", "", "Codex config profile")
	sandbox := fs.String("sandbox", agent.DefaultCodexSandbox, "Codex sandbox mode")
	approval := fs.String("approval", agent.DefaultApprovalPolicy, "Codex approval policy")
	eventJSON := fs.Bool("event-json", true, "ask Codex to emit JSONL events")
	skipGitRepoCheck := fs.Bool("skip-git-repo-check", true, "allow Codex to run when the host project is not a git repository")
	outputLastMessage := fs.String("output-last-message", "", "path for provider final response; defaults to a run artifact")
	validationTimeout := fs.Duration("validation-timeout", run.DefaultEvaluatorValidationTimeout, "timeout for generated evaluator baseline validation")
	dryRun := fs.Bool("dry-run", false, "print the provider invocation without running it")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	return evaluatorGenerateWithOptions(evaluatorGenerationOptions{
		Context:           ctx,
		ProjectDir:        *projectDir,
		RunID:             *runID,
		AgentName:         *agentName,
		CodexBin:          *codexBin,
		Model:             *modelName,
		Profile:           *profile,
		Sandbox:           *sandbox,
		Approval:          *approval,
		EventJSON:         *eventJSON,
		SkipGitRepoCheck:  *skipGitRepoCheck,
		OutputLastMessage: *outputLastMessage,
		ValidationTimeout: *validationTimeout,
		DryRun:            *dryRun,
	}, stdout, stderr)
}

func evaluatorGenerateWithOptions(opts evaluatorGenerationOptions, stdout, stderr io.Writer) int {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	configPath, err := archive.RunConfigPath(opts.ProjectDir, opts.RunID)
	if err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	absProject, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	provider, err := resolveGenerationProvider(absProject, cfg, opts.AgentName)
	if err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 2
	}
	projectDir := absProject
	if cfg.ProjectDir != "" {
		projectDir = archive.ProjectPath(absProject, cfg.ProjectDir)
	}
	runDir := cfg.RunDir
	if runDir == "" {
		runDir = filepath.Dir(configPath)
	} else {
		runDir = archive.ProjectPath(projectDir, runDir)
	}
	evaluatorPath := filepath.Join(runDir, "evaluator", "evaluator.sh")
	designPath := filepath.Join(runDir, "evaluator", "evaluator.md")
	promptPath := filepath.Join(runDir, "prompts", "evaluator-generation.md")
	scratchDir := filepath.Join(runDir, "tmp", "evaluator-generation")
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	baselineSourceDir := evaluatorBaselineSourceDir(projectDir, runDir)
	prompt := agent.BuildEvaluatorPrompt(agent.EvaluatorPromptRequest{
		RunConfig:         *cfg,
		InterfaceDocPath:  archive.ProjectRelativePath(projectDir, archive.ProjectPath(projectDir, cfg.InterfaceDocs)),
		RunDir:            archive.ProjectRelativePath(projectDir, runDir),
		EvaluatorPath:     archive.ProjectRelativePath(projectDir, evaluatorPath),
		BaselineSourceDir: archive.ProjectRelativePath(projectDir, baselineSourceDir),
		ScratchDir:        archive.ProjectRelativePath(projectDir, scratchDir),
	})
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	before, _ := os.ReadFile(evaluatorPath)
	if opts.OutputLastMessage == "" {
		opts.OutputLastMessage = filepath.Join(runDir, "agents", provider.Name+"-evaluator-final.md")
	}

	if provider.Kind == "command" {
		return runCommandEvaluatorProvider(provider, projectDir, runDir, promptPath, evaluatorPath, designPath, cfg, before, opts, stdout, stderr)
	}
	if provider.Kind != "codex" {
		fmt.Fprintf(stderr, "evaluator generate failed: provider %q is not implemented for evaluator generation yet\n", provider.Name)
		return 2
	}
	codexOpts := agent.CodexOptions{
		ProviderName:      provider.Name,
		Binary:            opts.CodexBin,
		ProjectDir:        projectDir,
		RunDir:            runDir,
		PromptPath:        promptPath,
		Model:             opts.Model,
		Profile:           opts.Profile,
		Sandbox:           opts.Sandbox,
		ApprovalPolicy:    opts.Approval,
		OutputLastMessage: opts.OutputLastMessage,
		JSONEvents:        opts.EventJSON,
		SkipGitRepoCheck:  opts.SkipGitRepoCheck,
	}
	command := agent.BuildCodexExecCommand(codexOpts)
	if opts.DryRun {
		printEvaluatorDryRun(stdout, provider.Name, command, promptPath, projectDir, runDir, evaluatorPath, opts.OutputLastMessage)
		return 0
	}
	if err := checkProviderExecutable(provider, command); err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Running %s to generate evaluator for run %s\n", provider.Name, cfg.ID)
	fmt.Fprintf(stdout, "Command: %s\n", agent.FormatCommand(command))
	result, err := agent.RunCodex(ctx, codexOpts, stdout, stderr)
	if err != nil {
		printProviderFailure(stderr, "Codex", result)
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	return finishEvaluatorGeneration(projectDir, cfg.ID, evaluatorPath, designPath, before, opts.ValidationTimeout, result.InvocationPath, result.StdoutPath, result.StderrPath, result.FinalPath, stdout, stderr)
}

func runCommandEvaluatorProvider(provider agent.ProviderDefinition, projectDir, runDir, promptPath, evaluatorPath, designPath string, cfg *model.RunConfig, before []byte, opts evaluatorGenerationOptions, stdout, stderr io.Writer) int {
	command := agent.BuildCommandProviderCommand(provider)
	if opts.DryRun {
		printEvaluatorDryRun(stdout, provider.Name, command, promptPath, projectDir, runDir, evaluatorPath, opts.OutputLastMessage)
		return 0
	}
	if err := checkProviderExecutable(provider, command); err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Running %s to generate evaluator for run %s\n", provider.Name, cfg.ID)
	fmt.Fprintf(stdout, "Command: %s\n", agent.FormatCommand(command))
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := agent.RunCommandProvider(ctx, agent.CommandProviderOptions{
		Provider:          provider,
		ProjectDir:        projectDir,
		RunDir:            runDir,
		PromptPath:        promptPath,
		Model:             opts.Model,
		OutputLastMessage: opts.OutputLastMessage,
	}, stdout, stderr)
	if err != nil {
		printProviderFailure(stderr, provider.Name, result)
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	return finishEvaluatorGeneration(projectDir, cfg.ID, evaluatorPath, designPath, before, opts.ValidationTimeout, result.InvocationPath, result.StdoutPath, result.StderrPath, result.FinalPath, stdout, stderr)
}

func finishEvaluatorGeneration(projectDir, runID, evaluatorPath, designPath string, before []byte, validationTimeout time.Duration, invocationPath, stdoutPath, stderrPath, finalPath string, stdout, stderr io.Writer) int {
	after, err := os.ReadFile(evaluatorPath)
	if err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: evaluator was not written: %v\n", err)
		return 1
	}
	if string(after) == string(before) {
		fmt.Fprintf(stderr, "evaluator generate failed: provider did not update %s\n", evaluatorPath)
		return 1
	}
	if err := os.Chmod(evaluatorPath, 0o755); err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	validation, err := run.ValidateGeneratedEvaluator(run.EvaluatorValidationOptions{
		ProjectDir: projectDir,
		RunID:      runID,
		Timeout:    validationTimeout,
	})
	if validation != nil {
		fmt.Fprintf(stdout, "Validation: %s\n", validation.ReportPath)
		fmt.Fprintf(stdout, "Validation stdout: %s\n", validation.StdoutPath)
		fmt.Fprintf(stdout, "Validation stderr: %s\n", validation.StderrPath)
	}
	if err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	report, err := run.MarkEvaluatorReady(run.EvaluatorReadyOptions{
		ProjectDir: projectDir,
		RunID:      runID,
	})
	if err != nil {
		fmt.Fprintf(stderr, "evaluator generate failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nEvaluator generation complete\n")
	fmt.Fprintf(stdout, "Evaluator: %s\n", evaluatorPath)
	if _, err := os.Stat(designPath); err == nil {
		fmt.Fprintf(stdout, "Design: %s\n", designPath)
	}
	fmt.Fprintf(stdout, "Invocation: %s\n", invocationPath)
	fmt.Fprintf(stdout, "Stdout: %s\n", stdoutPath)
	fmt.Fprintf(stdout, "Stderr: %s\n", stderrPath)
	fmt.Fprintf(stdout, "Final message: %s\n", finalPath)
	if len(report.UpdatedCandidates) > 0 {
		fmt.Fprintf(stdout, "Ready for evaluation: %s\n", strings.Join(report.UpdatedCandidates, ", "))
	}
	return 0
}

func printEvaluatorDryRun(stdout io.Writer, providerName string, command []string, promptPath, projectDir, runDir, evaluatorPath, finalPath string) {
	fmt.Fprintf(stdout, "Agent provider: %s\n", providerName)
	fmt.Fprintf(stdout, "Provider command:\n%s\n\n", agent.FormatCommand(command))
	fmt.Fprintf(stdout, "Prompt stdin: %s\n", promptPath)
	fmt.Fprintf(stdout, "Project root: %s\n", projectDir)
	fmt.Fprintf(stdout, "Run archive: %s\n", runDir)
	fmt.Fprintf(stdout, "Evaluator output: %s\n", evaluatorPath)
	fmt.Fprintf(stdout, "Final message: %s\n", finalPath)
}

func printProviderFailure(stderr io.Writer, label string, result any) {
	switch r := result.(type) {
	case *agent.CodexResult:
		if r != nil {
			fmt.Fprintf(stderr, "%s exited with status %d\n", label, r.ExitCode)
			fmt.Fprintf(stderr, "Invocation: %s\n", r.InvocationPath)
			fmt.Fprintf(stderr, "Stdout: %s\n", r.StdoutPath)
			fmt.Fprintf(stderr, "Stderr: %s\n", r.StderrPath)
		}
	case *agent.CommandProviderResult:
		if r != nil {
			fmt.Fprintf(stderr, "Provider %s exited with status %d\n", label, r.ExitCode)
			fmt.Fprintf(stderr, "Invocation: %s\n", r.InvocationPath)
			fmt.Fprintf(stderr, "Stdout: %s\n", r.StdoutPath)
			fmt.Fprintf(stderr, "Stderr: %s\n", r.StderrPath)
		}
	}
}

func evaluatorBaselineSourceDir(projectDir, runDir string) string {
	board, err := archive.LoadLeaderboard(filepath.Join(runDir, "leaderboard.json"))
	if err != nil {
		return filepath.Join(runDir, "round-0001", "candidate-0000-baseline", "src")
	}
	for _, result := range board.Results {
		if result.Candidate.Baseline {
			sourcePath := archive.ProjectPath(projectDir, result.Candidate.SourcePath)
			if filepath.Base(sourcePath) == "src" {
				return sourcePath
			}
			return filepath.Join(sourcePath, "src")
		}
	}
	return filepath.Join(runDir, "round-0001", "candidate-0000-baseline", "src")
}
