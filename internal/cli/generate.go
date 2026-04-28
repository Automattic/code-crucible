package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
	"github.com/Automattic/code-crucible/internal/run"
)

type generationOptions struct {
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
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	agentName := fs.String("agent", "", "agent provider to run; defaults to the run or project default")
	codexBin := fs.String("codex-bin", agent.DefaultCodexBinary, "Codex CLI binary")
	model := fs.String("model", "", "Codex model override")
	profile := fs.String("profile", "", "Codex config profile")
	sandbox := fs.String("sandbox", agent.DefaultCodexSandbox, "Codex sandbox mode")
	approval := fs.String("approval", agent.DefaultApprovalPolicy, "Codex approval policy")
	eventJSON := fs.Bool("event-json", true, "ask Codex to emit JSONL events")
	skipGitRepoCheck := fs.Bool("skip-git-repo-check", true, "allow Codex to run when the host project is not a git repository")
	outputLastMessage := fs.String("output-last-message", "", "path for Codex final response; defaults to a run artifact")
	dryRun := fs.Bool("dry-run", false, "print the Codex invocation without running it")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	return generateWithOptions(generationOptions{
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
	if provider.Kind != "codex" {
		fmt.Fprintf(stderr, "generate failed: provider %q is not implemented for generation yet\n", provider.Name)
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
	result, err := agent.RunCodex(context.Background(), codexOpts, stdout, stderr)
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
	return 0
}

func resolveGenerationProvider(projectDir string, cfg *model.RunConfig, requested string) (agent.ProviderDefinition, error) {
	name := agent.NormalizeProviderName(requested)
	if name == "" && cfg != nil {
		name = agent.NormalizeProviderName(cfg.Agent)
	}
	if name == "" {
		if projectConfig, err := project.Load(projectDir); err == nil {
			name = agent.NormalizeProviderName(projectConfig.DefaultAgent)
		}
	}
	if name == "" {
		name = agent.ProviderCodex
	}
	provider, ok := agent.Provider(name)
	if !ok {
		return agent.ProviderDefinition{}, fmt.Errorf("unsupported --agent %q", name)
	}
	if !provider.Supports("generation") {
		return agent.ProviderDefinition{}, fmt.Errorf("agent provider %q does not support generation", provider.Name)
	}
	return provider, nil
}
