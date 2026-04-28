package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
	"github.com/Automattic/code-crucible/internal/run"
)

type generationOptions struct {
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
	DryRun            bool
}

func runGenerate(args []string, stdout, stderr io.Writer) int {
	return runGenerateWithContext(context.Background(), args, stdout, stderr)
}

func runGenerateWithContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	agentName := fs.String("agent", "", "agent provider to run; defaults to the run or project default")
	codexBin := fs.String("codex-bin", agent.DefaultCodexBinary, "Codex CLI binary")
	model := fs.String("model", "", "agent model override")
	profile := fs.String("profile", "", "Codex config profile")
	sandbox := fs.String("sandbox", agent.DefaultCodexSandbox, "Codex sandbox mode")
	approval := fs.String("approval", agent.DefaultApprovalPolicy, "Codex approval policy")
	eventJSON := fs.Bool("event-json", true, "ask Codex to emit JSONL events")
	skipGitRepoCheck := fs.Bool("skip-git-repo-check", true, "allow Codex to run when the host project is not a git repository")
	outputLastMessage := fs.String("output-last-message", "", "path for provider final response; defaults to a run artifact")
	dryRun := fs.Bool("dry-run", false, "print the provider invocation without running it")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	return generateWithOptions(generationOptions{
		Context:           ctx,
		ProjectDir:        *projectDir,
		RunID:             *runID,
		AgentName:         *agentName,
		CodexBin:          *codexBin,
		Model:             *model,
		Profile:           *profile,
		Sandbox:           *sandbox,
		Approval:          *approval,
		EventJSON:         *eventJSON,
		SkipGitRepoCheck:  *skipGitRepoCheck,
		OutputLastMessage: *outputLastMessage,
		DryRun:            *dryRun,
	}, stdout, stderr)
}

func generateWithOptions(opts generationOptions, stdout, stderr io.Writer) int {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	configPath, err := archive.RunConfigPath(opts.ProjectDir, opts.RunID)
	if err != nil {
		fmt.Fprintf(stderr, "generate failed: %v\n", err)
		return 1
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "generate failed: %v\n", err)
		return 1
	}
	absProject, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		fmt.Fprintf(stderr, "generate failed: %v\n", err)
		return 1
	}
	provider, err := resolveGenerationProvider(absProject, cfg, opts.AgentName)
	if err != nil {
		fmt.Fprintf(stderr, "generate failed: %v\n", err)
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
	promptPath := cfg.PromptPath
	if promptPath == "" {
		promptPath = filepath.Join(runDir, "prompts", "generation-round-0001.md")
	} else {
		promptPath = archive.ProjectPath(projectDir, promptPath)
	}
	if opts.OutputLastMessage == "" {
		opts.OutputLastMessage = filepath.Join(runDir, "agents", "codex-final.md")
	}

	if provider.Kind == "command" {
		return runCommandGenerationProvider(provider, projectDir, runDir, promptPath, cfg, opts, stdout, stderr)
	}
	if provider.Kind != "codex" {
		fmt.Fprintf(stderr, "generate failed: provider %q is not implemented for generation yet\n", provider.Name)
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
		fmt.Fprintf(stdout, "Agent provider: %s\n", provider.Name)
		fmt.Fprintf(stdout, "Codex command:\n%s\n\n", agent.FormatCommand(command))
		fmt.Fprintf(stdout, "Prompt stdin: %s\n", promptPath)
		fmt.Fprintf(stdout, "Project root: %s\n", projectDir)
		fmt.Fprintf(stdout, "Run archive: %s\n", runDir)
		fmt.Fprintf(stdout, "Final message: %s\n", opts.OutputLastMessage)
		return 0
	}

	fmt.Fprintf(stdout, "Running %s for run %s\n", provider.Name, cfg.ID)
	fmt.Fprintf(stdout, "Command: %s\n", agent.FormatCommand(command))
	result, err := agent.RunCodex(ctx, codexOpts, stdout, stderr)
	if err != nil {
		if result != nil {
			fmt.Fprintf(stderr, "Codex exited with status %d\n", result.ExitCode)
			fmt.Fprintf(stderr, "Invocation: %s\n", result.InvocationPath)
			fmt.Fprintf(stderr, "Stdout: %s\n", result.StdoutPath)
			fmt.Fprintf(stderr, "Stderr: %s\n", result.StderrPath)
		}
		fmt.Fprintf(stderr, "generate failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "\nCodex generation complete\n")
	fmt.Fprintf(stdout, "Invocation: %s\n", result.InvocationPath)
	fmt.Fprintf(stdout, "Stdout: %s\n", result.StdoutPath)
	fmt.Fprintf(stdout, "Stderr: %s\n", result.StderrPath)
	fmt.Fprintf(stdout, "Final message: %s\n", result.FinalPath)

	report, err := run.AdoptCandidates(run.AdoptionOptions{
		ProjectDir: opts.ProjectDir,
		RunID:      cfg.ID,
		Model:      opts.Model,
	})
	if err != nil {
		fmt.Fprintf(stderr, "adopt failed after generation: %v\n", err)
		return 1
	}
	printAdoptionReport(stdout, stderr, report)
	printPostGenerationNextSteps(stdout, projectDir, cfg)
	return 0
}

func runCommandGenerationProvider(provider agent.ProviderDefinition, projectDir, runDir, promptPath string, cfg *model.RunConfig, opts generationOptions, stdout, stderr io.Writer) int {
	if opts.OutputLastMessage == filepath.Join(runDir, "agents", "codex-final.md") {
		opts.OutputLastMessage = filepath.Join(runDir, "agents", provider.Name+"-final.md")
	}
	command := agent.BuildCommandProviderCommand(provider)
	if opts.DryRun {
		fmt.Fprintf(stdout, "Agent provider: %s\n", provider.Name)
		fmt.Fprintf(stdout, "Provider command:\n%s\n\n", agent.FormatCommand(command))
		fmt.Fprintf(stdout, "Prompt stdin: %s\n", promptPath)
		fmt.Fprintf(stdout, "Project root: %s\n", projectDir)
		fmt.Fprintf(stdout, "Run archive: %s\n", runDir)
		fmt.Fprintf(stdout, "Final message: %s\n", opts.OutputLastMessage)
		return 0
	}

	runID := ""
	if cfg != nil {
		runID = cfg.ID
	}
	fmt.Fprintf(stdout, "Running %s for run %s\n", provider.Name, runID)
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
		if result != nil {
			fmt.Fprintf(stderr, "Provider %s exited with status %d\n", provider.Name, result.ExitCode)
			fmt.Fprintf(stderr, "Invocation: %s\n", result.InvocationPath)
			fmt.Fprintf(stderr, "Stdout: %s\n", result.StdoutPath)
			fmt.Fprintf(stderr, "Stderr: %s\n", result.StderrPath)
		}
		fmt.Fprintf(stderr, "generate failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "\n%s generation complete\n", provider.Name)
	fmt.Fprintf(stdout, "Invocation: %s\n", result.InvocationPath)
	fmt.Fprintf(stdout, "Stdout: %s\n", result.StdoutPath)
	fmt.Fprintf(stdout, "Stderr: %s\n", result.StderrPath)
	fmt.Fprintf(stdout, "Final message: %s\n", result.FinalPath)

	report, err := run.AdoptCandidates(run.AdoptionOptions{
		ProjectDir: opts.ProjectDir,
		RunID:      runID,
		Model:      opts.Model,
	})
	if err != nil {
		fmt.Fprintf(stderr, "adopt failed after generation: %v\n", err)
		return 1
	}
	printAdoptionReport(stdout, stderr, report)
	printPostGenerationNextSteps(stdout, projectDir, cfg)
	return 0
}

func printPostGenerationNextSteps(stdout io.Writer, projectDir string, cfg *model.RunConfig) {
	if cfg == nil {
		return
	}
	fmt.Fprintln(stdout)
	if !runConfigHasEvaluator(cfg) {
		fmt.Fprintf(stdout, "Generate evaluator: %s\n", agent.FormatCommand([]string{"crucible", "evaluator", "generate", "--project-dir", projectDir, "--run", cfg.ID}))
	}
	fmt.Fprintf(stdout, "Evaluate candidates: %s\n", agent.FormatCommand([]string{"crucible", "evaluate", "--project-dir", projectDir, "--run", cfg.ID}))
	if !runConfigHasEvaluator(cfg) {
		fmt.Fprintln(stdout, "Evaluator warning: this run uses the placeholder evaluator scaffold. Run evaluator generate or configure evaluator/evaluator.sh before expecting candidates to pass or produce meaningful metrics.")
	}
}

func runConfigHasEvaluator(cfg *model.RunConfig) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.Evaluator) != "" || strings.TrimSpace(cfg.EvaluatorScript) != "" || cfg.EvaluatorGenerated
}

func resolveGenerationProvider(projectDir string, cfg *model.RunConfig, requested string) (agent.ProviderDefinition, error) {
	name := agent.NormalizeProviderName(requested)
	if name == "" && cfg != nil {
		name = agent.NormalizeProviderName(cfg.Agent)
	}
	var configured map[string]agent.ProviderDefinition
	if projectConfig, err := project.Load(projectDir); err == nil {
		configured = projectConfig.AgentProviders
		if name == "" {
			name = agent.NormalizeProviderName(projectConfig.DefaultAgent)
		}
	}
	if name == "" {
		name = agent.ProviderCodex
	}
	provider, ok := agent.ProviderFromConfig(name, configured)
	if !ok {
		return agent.ProviderDefinition{}, fmt.Errorf("unsupported --agent %q", name)
	}
	if err := agent.ValidateProviderDefinition(provider); err != nil {
		return agent.ProviderDefinition{}, err
	}
	if !provider.Supports("generation") {
		return agent.ProviderDefinition{}, fmt.Errorf("agent provider %q does not support generation", provider.Name)
	}
	return provider, nil
}
