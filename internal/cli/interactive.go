package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Automattic/code-crucible/internal/agent"
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

type interactiveRunOptions struct {
	Rounds          int
	Exploration     float64
	Evaluator       string
	EvaluatorScript string
	ExternalMode    string
	Fixtures        string
	AllowHosts      []string
}

type reviewArtifact struct {
	Label string
	Path  string
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
	controller := WorkflowController{
		ProjectDir: projectDir,
		Stdout:     s.stdout,
		Stderr:     s.stderr,
	}
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
		fmt.Fprintln(s.stdout, "  9. Agent settings")
		fmt.Fprintln(s.stdout, "  10. Discovery")
		fmt.Fprintln(s.stdout, "  11. Adopt generated candidates")
		fmt.Fprintln(s.stdout, "  12. Prepare next round")
		fmt.Fprintln(s.stdout, "  13. Query archive")
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
			runSelector, ok := s.askRunSelector(projectDir)
			if !ok {
				return 0
			}
			if code := controller.Leaderboard(runSelector); code != 0 {
				return code
			}
		case "3", "generate":
			runSelector, ok := s.askRunSelector(projectDir)
			if !ok {
				return 0
			}
			generationAgent, ok := s.askGenerationAgent(projectDir)
			if !ok {
				return 0
			}
			proceed, ok := s.reviewRunArtifactsBeforeGeneration(projectDir, runSelector)
			if !ok {
				return 0
			}
			if !proceed {
				fmt.Fprintln(s.stdout, "Generation skipped.")
				continue
			}
			advancedArgs, ok := s.askAdvancedGenerationOptions(projectDir, runSelector, generationAgent)
			if !ok {
				return 0
			}
			if code := controller.Generate(GenerateWorkflowOptions{
				RunSelector: runSelector,
				Agent:       generationAgent,
				ExtraArgs:   advancedArgs,
			}); code != 0 {
				return code
			}
		case "4", "evaluate":
			runSelector, ok := s.askRunSelector(projectDir)
			if !ok {
				return 0
			}
			advancedArgs, ok := s.askAdvancedEvaluateOptions()
			if !ok {
				return 0
			}
			if code := controller.Evaluate(EvaluateWorkflowOptions{
				RunSelector: runSelector,
				ExtraArgs:   advancedArgs,
			}); code != 0 {
				return code
			}
		case "5", "evolve":
			runSelector, ok := s.askRunSelector(projectDir)
			if !ok {
				return 0
			}
			rounds, ok := s.askInt("Rounds", 1)
			if !ok {
				return 0
			}
			parents, ok := s.askInt("Parents", defaultVariantCount)
			if !ok {
				return 0
			}
			generationAgent, ok := s.askGenerationAgent(projectDir)
			if !ok {
				return 0
			}
			if code := controller.Evolve(EvolveWorkflowOptions{
				RunSelector: runSelector,
				Rounds:      rounds,
				Parents:     parents,
				Agent:       generationAgent,
			}); code != 0 {
				return code
			}
		case "6", "report":
			runSelector, ok := s.askRunSelector(projectDir)
			if !ok {
				return 0
			}
			outputPath, ok := s.ask("Report output path [archive default]: ")
			if !ok {
				return 0
			}
			jsonOut, ok := s.confirm("Print report metadata JSON?", false)
			if !ok {
				return 0
			}
			if code := controller.Report(ReportWorkflowOptions{
				RunSelector: runSelector,
				OutputPath:  outputPath,
				JSON:        jsonOut,
			}); code != 0 {
				return code
			}
		case "7", "inspect":
			runSelector, ok := s.askRunSelector(projectDir)
			if !ok {
				return 0
			}
			candidateID, ok := s.ask("Candidate ID [candidate-0000-baseline]: ")
			if !ok {
				return 0
			}
			candidateID = strings.TrimSpace(candidateID)
			if candidateID == "" {
				candidateID = "candidate-0000-baseline"
			}
			if code := controller.Inspect(InspectWorkflowOptions{
				RunSelector: runSelector,
				CandidateID: candidateID,
			}); code != 0 {
				return code
			}
		case "8", "index":
			scopeRun, ok := s.confirm("Rebuild only one run?", false)
			if !ok {
				return 0
			}
			runSelector := ""
			if scopeRun {
				selected, ok := s.askRunSelector(projectDir)
				if !ok {
					return 0
				}
				runSelector = selected
			}
			jsonOut, ok := s.confirm("Print index rebuild JSON?", false)
			if !ok {
				return 0
			}
			if code := controller.Index(IndexWorkflowOptions{
				RunSelector: runSelector,
				ScopedRun:   scopeRun,
				JSON:        jsonOut,
			}); code != 0 {
				return code
			}
		case "9", "agent", "agents", "agent settings":
			if code := s.agentSettings(projectDir); code != 0 {
				return code
			}
		case "10", "discover", "discovery":
			if code := s.standaloneDiscovery(controller); code != 0 {
				return code
			}
		case "11", "adopt":
			runSelector, ok := s.askRunSelector(projectDir)
			if !ok {
				return 0
			}
			modelName, ok := s.ask("Model label [none]: ")
			if !ok {
				return 0
			}
			if code := controller.Adopt(AdoptWorkflowOptions{
				RunSelector: runSelector,
				Model:       modelName,
			}); code != 0 {
				return code
			}
		case "12", "next-round", "next round":
			runSelector, ok := s.askRunSelector(projectDir)
			if !ok {
				return 0
			}
			parents, ok := s.askInt("Parents", defaultVariantCount)
			if !ok {
				return 0
			}
			if code := controller.NextRound(NextRoundWorkflowOptions{
				RunSelector: runSelector,
				Parents:     parents,
			}); code != 0 {
				return code
			}
		case "13", "query":
			if code := s.queryArchive(controller); code != 0 {
				return code
			}
		default:
			fmt.Fprintf(s.stdout, "Unknown action %q\n", choice)
		}
	}
}

func (s interactiveSession) askRunSelector(projectDir string) (string, bool) {
	runDir, err := archive.LatestRunDir(projectDir)
	if err != nil {
		fmt.Fprintln(s.stdout, "No runs available.")
		return "", false
	}
	fmt.Fprintf(s.stdout, "Latest run: %s\n", filepath.Base(runDir))
	answer, ok := s.ask("Run [latest]: ")
	if !ok {
		return "", false
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "latest", true
	}
	return answer, true
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

	fmt.Fprintf(s.stdout, "Discovery plan: %s\n", plan.PlanPath)
	s.printSourceSuggestions(plan)

	agentPlan, agentPlanProvider, ok := s.maybeRunAgentDiscovery(projectDir, plan)
	if !ok {
		return 0
	}
	var clarifications []clarificationAnswer
	if agentPlan != nil {
		request, clarifications, ok = s.applyAgentClarifications(request, agentPlan, agentDisplayName(agentPlanProvider))
		if !ok {
			return 0
		}
	}

	sourcePath := ""
	if agentPlan != nil && strings.TrimSpace(agentPlan.SourcePath) != "" {
		recommended := strings.TrimSpace(agentPlan.SourcePath)
		if sourcePathExists(projectDir, recommended) {
			useSource, ok := s.confirm(fmt.Sprintf("Use %s recommended %s as the baseline source path?", agentDisplayName(agentPlanProvider), recommended), true)
			if !ok {
				return 0
			}
			if useSource {
				sourcePath = recommended
			}
		} else {
			fmt.Fprintf(s.stdout, "%s recommended source path %s, but that path was not found. Leaving source path unset unless you choose a local suggestion.\n", agentDisplayName(agentPlanProvider), recommended)
		}
	}
	if sourcePath == "" && len(plan.Suggestions) > 0 {
		useSuggestion, ok := s.confirm(fmt.Sprintf("Use %s as the baseline source path?", plan.Suggestions[0].Path), false)
		if !ok {
			return 0
		}
		if useSuggestion {
			sourcePath = plan.Suggestions[0].Path
		}
	}
	if sourcePath == "" {
		fmt.Fprintln(s.stdout, "No source path selected. Generation will ask the agent to discover the involved code.")
	}

	externalMode := s.externalModeFromAgentPlan(agentPlan, agentDisplayName(agentPlanProvider))
	advancedRun, ok := s.askAdvancedRunOptions(externalMode)
	if !ok {
		return 0
	}

	created, err := run.Create(run.Options{
		ProjectDir:      projectDir,
		Optimize:        request,
		SourcePath:      sourcePath,
		Variants:        variants,
		Rounds:          advancedRun.Rounds,
		Exploration:     advancedRun.Exploration,
		Evaluator:       advancedRun.Evaluator,
		EvaluatorScript: advancedRun.EvaluatorScript,
		ExternalMode:    advancedRun.ExternalMode,
		Fixtures:        advancedRun.Fixtures,
		AllowHosts:      advancedRun.AllowHosts,
		AgentPlan:       agentPlan,
		Clarifications: func() []discovery.Clarification {
			out := make([]discovery.Clarification, 0, len(clarifications))
			for _, clarification := range clarifications {
				out = append(out, discovery.Clarification{
					Question: clarification.Question,
					Answer:   clarification.Answer,
				})
			}
			return out
		}(),
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
	fmt.Fprintf(s.stdout, "Rounds: %d\n", advancedRun.Rounds)
	fmt.Fprintf(s.stdout, "Exploration: %.2f\n", advancedRun.Exploration)
	fmt.Fprintf(s.stdout, "External mode: %s\n", advancedRun.ExternalMode)
	if sourcePath == "" {
		fmt.Fprintln(s.stdout, "Source path: agent discovery pending")
	} else {
		fmt.Fprintf(s.stdout, "Source path: %s\n", sourcePath)
	}
	fmt.Fprintln(s.stdout)
	return runLeaderboard([]string{"--project-dir", projectDir}, s.stdout, s.stderr)
}

func (s interactiveSession) askAdvancedRunOptions(externalMode string) (interactiveRunOptions, bool) {
	opts := interactiveRunOptions{
		Rounds:       1,
		Exploration:  0.35,
		ExternalMode: strings.TrimSpace(externalMode),
	}
	if opts.ExternalMode == "" {
		opts.ExternalMode = string(model.ExternalModeDeny)
	}
	configure, ok := s.confirm("Configure advanced run options?", false)
	if !ok || !configure {
		return opts, ok
	}
	rounds, ok := s.askInt("Rounds", opts.Rounds)
	if !ok {
		return opts, false
	}
	opts.Rounds = rounds
	exploration, ok := s.askFloatRange("Exploration", opts.Exploration, 0, 1)
	if !ok {
		return opts, false
	}
	opts.Exploration = exploration
	evaluator, ok := s.ask("Evaluator command [none]: ")
	if !ok {
		return opts, false
	}
	opts.Evaluator = strings.TrimSpace(evaluator)
	evaluatorScript, ok := s.ask("Evaluator script path [none]: ")
	if !ok {
		return opts, false
	}
	opts.EvaluatorScript = strings.TrimSpace(evaluatorScript)
	mode, ok := s.askExternalMode(opts.ExternalMode)
	if !ok {
		return opts, false
	}
	opts.ExternalMode = mode
	fixtures, ok := s.ask("External fixtures path [none]: ")
	if !ok {
		return opts, false
	}
	opts.Fixtures = strings.TrimSpace(fixtures)
	allowHosts, ok := s.ask("Allow hosts, comma separated [none]: ")
	if !ok {
		return opts, false
	}
	opts.AllowHosts = splitCSV(allowHosts)
	return opts, true
}

func (s interactiveSession) askExternalMode(fallback string) (string, bool) {
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		fallback = string(model.ExternalModeDeny)
	}
	for {
		answer, ok := s.ask(fmt.Sprintf("External mode [%s]: ", fallback))
		if !ok {
			return "", false
		}
		mode := strings.TrimSpace(answer)
		if mode == "" {
			mode = fallback
		}
		if model.ExternalMode(mode).Valid() {
			return mode, true
		}
		fmt.Fprintln(s.stdout, "External mode must be deny, allowlist, mock, replay, or record.")
	}
}

func (s interactiveSession) askGenerationAgent(projectDir string) (string, bool) {
	cfg, err := project.Load(projectDir)
	defaultAgent := agent.ProviderCodex
	if err == nil {
		defaultAgent = cfg.DefaultAgent
	}
	s.printGenerationProviders(cfg)
	answer, ok := s.ask(fmt.Sprintf("Generation agent [%s]: ", defaultAgent))
	if !ok {
		return "", false
	}
	answer = agent.NormalizeProviderName(answer)
	if answer == "" || answer == defaultAgent {
		return "", true
	}
	if _, err := configuredProvider(projectDir, answer, "generation"); err != nil {
		fmt.Fprintf(s.stdout, "%v\n", err)
		return s.askGenerationAgent(projectDir)
	}
	return answer, true
}

func (s interactiveSession) standaloneDiscovery(controller WorkflowController) int {
	request, ok := s.askRequired("Optimization request: ")
	if !ok {
		return 0
	}
	discoveryAgent, ok := s.askDiscoveryAgent(controller.ProjectDir)
	if !ok {
		return 0
	}
	return controller.Discover(DiscoverWorkflowOptions{
		Agent:   discoveryAgent,
		Request: request,
	})
}

func (s interactiveSession) askAdvancedGenerationOptions(projectDir, runSelector, generationAgent string) ([]string, bool) {
	configure, ok := s.confirm("Configure advanced generation options?", false)
	if !ok || !configure {
		return nil, ok
	}
	provider, err := s.resolveInteractiveGenerationProvider(projectDir, runSelector, generationAgent)
	if err != nil {
		fmt.Fprintf(s.stderr, "generate options failed: %v\n", err)
		return nil, false
	}

	var args []string
	modelName, ok := s.ask("Model override [default]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(modelName) != "" {
		args = append(args, "--model", strings.TrimSpace(modelName))
	}
	outputPath, ok := s.ask("Final response output path [artifact default]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(outputPath) != "" {
		args = append(args, "--output-last-message", strings.TrimSpace(outputPath))
	}
	if provider.Kind == agent.ProviderCodex {
		profile, ok := s.ask("Codex profile [default]: ")
		if !ok {
			return nil, false
		}
		if strings.TrimSpace(profile) != "" {
			args = append(args, "--profile", strings.TrimSpace(profile))
		}
		sandbox, ok := s.ask(fmt.Sprintf("Codex sandbox [%s]: ", agent.DefaultCodexSandbox))
		if !ok {
			return nil, false
		}
		if strings.TrimSpace(sandbox) != "" {
			args = append(args, "--sandbox", strings.TrimSpace(sandbox))
		}
		approval, ok := s.ask(fmt.Sprintf("Codex approval mode [%s]: ", agent.DefaultApprovalPolicy))
		if !ok {
			return nil, false
		}
		if strings.TrimSpace(approval) != "" {
			args = append(args, "--approval", strings.TrimSpace(approval))
		}
	}
	dryRun, ok := s.confirm("Dry run only?", false)
	if !ok {
		return nil, false
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args, true
}

func (s interactiveSession) reviewRunArtifactsBeforeGeneration(projectDir, runSelector string) (bool, bool) {
	artifacts, err := runReviewArtifacts(projectDir, runSelector)
	if err != nil {
		fmt.Fprintf(s.stderr, "review failed: %v\n", err)
		return false, false
	}
	fmt.Fprintln(s.stdout, "Run artifacts to review before generation:")
	for _, artifact := range artifacts {
		fmt.Fprintf(s.stdout, "- %s: %s\n", artifact.Label, artifact.Path)
	}
	for {
		answer, ok := s.ask("Review action [continue/edit/skip]: ")
		if !ok {
			return false, false
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "c", "continue":
			return true, true
		case "s", "skip":
			return false, true
		case "e", "edit", "open":
			proceed, ok := s.editReviewArtifacts(artifacts)
			if !ok || !proceed {
				return proceed, ok
			}
			return true, true
		default:
			fmt.Fprintln(s.stdout, "Please choose continue, edit, or skip.")
		}
	}
}

func (s interactiveSession) editReviewArtifacts(artifacts []reviewArtifact) (bool, bool) {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		fmt.Fprintln(s.stdout, "VISUAL and EDITOR are not set. Open the listed files manually if needed.")
		return s.confirm("Continue with generation?", true)
	}
	args := strings.Fields(editor)
	if len(args) == 0 {
		fmt.Fprintln(s.stdout, "Editor command is empty. Open the listed files manually if needed.")
		return s.confirm("Continue with generation?", true)
	}
	for _, artifact := range artifacts {
		args = append(args, artifact.Path)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(s.stderr, "editor failed: %v\n", err)
		return false, false
	}
	return s.confirm("Continue with generation?", true)
}

func runReviewArtifacts(projectDir, runSelector string) ([]reviewArtifact, error) {
	configPath, err := archive.RunConfigPath(projectDir, runSelector)
	if err != nil {
		return nil, err
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		return nil, err
	}
	runDir := cfg.RunDir
	if runDir == "" {
		runDir = filepath.Dir(configPath)
	} else {
		runDir = archive.ProjectPath(projectDir, runDir)
	}
	artifacts := []reviewArtifact{
		{Label: "Interface docs", Path: archive.ProjectPath(projectDir, cfg.InterfaceDocs)},
		{Label: "Evaluator scaffold", Path: filepath.Join(runDir, "evaluator", "evaluator.sh")},
		{Label: "Generation prompt", Path: archive.ProjectPath(projectDir, cfg.PromptPath)},
	}
	for _, artifact := range []reviewArtifact{
		{Label: "Agent discovery JSON", Path: filepath.Join(runDir, "docs", "agent-discovery.json")},
		{Label: "Agent discovery Markdown", Path: filepath.Join(runDir, "docs", "agent-discovery.md")},
	} {
		if _, err := os.Stat(artifact.Path); err == nil {
			artifacts = append(artifacts, artifact)
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return artifacts, nil
}

func (s interactiveSession) resolveInteractiveGenerationProvider(projectDir, runSelector, generationAgent string) (agent.ProviderDefinition, error) {
	configPath, err := archive.RunConfigPath(projectDir, runSelector)
	if err != nil {
		return agent.ProviderDefinition{}, err
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		return agent.ProviderDefinition{}, err
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return agent.ProviderDefinition{}, err
	}
	return resolveGenerationProvider(absProject, cfg, generationAgent)
}

func (s interactiveSession) askAdvancedEvaluateOptions() ([]string, bool) {
	configure, ok := s.confirm("Configure advanced evaluation options?", false)
	if !ok || !configure {
		return nil, ok
	}
	var args []string
	candidateID, ok := s.ask("Candidate ID [all]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(candidateID) != "" {
		args = append(args, "--candidate", strings.TrimSpace(candidateID))
	}
	timeoutValue, ok := s.ask("Timeout [none]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(timeoutValue) != "" {
		args = append(args, "--timeout", strings.TrimSpace(timeoutValue))
	}
	jobs, ok := s.askInt("Jobs", 1)
	if !ok {
		return nil, false
	}
	args = append(args, "--jobs", strconv.Itoa(jobs))
	nice, ok := s.askIntRange("Nice priority", 10, 0, 19)
	if !ok {
		return nil, false
	}
	args = append(args, "--nice", strconv.Itoa(nice))
	cpuLimit, ok := s.askIntRange("CPU limit", 0, 0, 1024)
	if !ok {
		return nil, false
	}
	args = append(args, "--cpu-limit", strconv.Itoa(cpuLimit))
	sandboxProfile, ok := s.ask("Sandbox profile [default]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(sandboxProfile) != "" {
		args = append(args, "--sandbox-profile", strings.TrimSpace(sandboxProfile))
	}
	sandboxEngine, ok := s.ask("Sandbox engine [local]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(sandboxEngine) != "" {
		args = append(args, "--sandbox-engine", strings.TrimSpace(sandboxEngine))
	}
	sandboxImage, ok := s.ask("Sandbox image [none]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(sandboxImage) != "" {
		args = append(args, "--sandbox-image", strings.TrimSpace(sandboxImage))
	}
	sandboxNetwork, ok := s.ask("Sandbox network [profile default]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(sandboxNetwork) != "" {
		args = append(args, "--sandbox-network", strings.TrimSpace(sandboxNetwork))
	}
	externalRouting, ok := s.ask("External routing [none/gateway-network]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(externalRouting) != "" {
		args = append(args, "--external-routing", strings.TrimSpace(externalRouting))
	}
	memoryLimit, ok := s.ask("Memory limit [profile default]: ")
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(memoryLimit) != "" {
		args = append(args, "--memory-limit", strings.TrimSpace(memoryLimit))
	}
	pidsLimit, ok := s.askIntRange("PID limit", 0, 0, 1_000_000)
	if !ok {
		return nil, false
	}
	args = append(args, "--pids-limit", strconv.Itoa(pidsLimit))
	return args, true
}

func (s interactiveSession) queryArchive(controller WorkflowController) int {
	kind, ok := s.ask("Query runs or candidates [runs]: ")
	if !ok {
		return 0
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "runs"
	}
	limit, ok := s.askInt("Limit", 0)
	if !ok {
		return 0
	}
	runSelector := ""
	status := ""
	if kind == "candidates" {
		selected, ok := s.askRunSelector(controller.ProjectDir)
		if !ok {
			return 0
		}
		runSelector = selected
		answer, ok := s.ask("Status filter [all]: ")
		if !ok {
			return 0
		}
		status = answer
	}
	return controller.Query(QueryWorkflowOptions{
		Kind:        kind,
		Limit:       limit,
		RunSelector: runSelector,
		Status:      status,
	})
}

func (s interactiveSession) askDiscoveryAgent(projectDir string) (string, bool) {
	return s.askDiscoveryAgentWithDefault(projectDir, agent.ProviderLocal)
}

func (s interactiveSession) askDiscoveryAgentWithDefault(projectDir, defaultAgent string) (string, bool) {
	defaultAgent = agent.NormalizeProviderName(defaultAgent)
	if defaultAgent == "" {
		defaultAgent = agent.ProviderLocal
	}
	cfg, _ := project.Load(projectDir)
	s.printDiscoveryProviders(cfg)
	answer, ok := s.ask(fmt.Sprintf("Discovery agent [%s]: ", defaultAgent))
	if !ok {
		return "", false
	}
	answer = agent.NormalizeProviderName(answer)
	if answer == "" {
		return defaultAgent, true
	}
	if _, err := configuredProvider(projectDir, answer, "discovery"); err != nil {
		fmt.Fprintf(s.stdout, "%v\n", err)
		return s.askDiscoveryAgent(projectDir)
	}
	return answer, true
}

func (s interactiveSession) agentSettings(projectDir string) int {
	cfg, err := project.Load(projectDir)
	if err != nil {
		fmt.Fprintf(s.stderr, "agent settings failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(s.stdout, "Default generation agent: %s\n", cfg.DefaultAgent)
	s.printGenerationProviders(cfg)
	answer, ok := s.ask("New default generation agent [leave unchanged]: ")
	if !ok {
		return 0
	}
	answer = agent.NormalizeProviderName(answer)
	if answer == "" {
		return 0
	}
	provider, err := configuredProvider(projectDir, answer, "generation")
	if err != nil {
		fmt.Fprintf(s.stderr, "agent settings failed: %v\n", err)
		return 1
	}
	cfg.DefaultAgent = provider.Name
	if err := project.Save(projectDir, cfg); err != nil {
		fmt.Fprintf(s.stderr, "agent settings failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(s.stdout, "Default generation agent updated to %s\n", cfg.DefaultAgent)
	return 0
}

func (s interactiveSession) printGenerationProviders(cfg *project.Config) {
	fmt.Fprintln(s.stdout, "Generation-capable agents:")
	var providers []agent.ProviderDefinition
	for _, name := range agent.ProviderNames() {
		provider, _ := agent.Provider(name)
		if provider.Supports("generation") {
			providers = append(providers, provider)
		}
	}
	if cfg != nil {
		for _, provider := range cfg.AgentProviders {
			if err := agent.ValidateProviderDefinition(provider); err == nil && provider.Supports("generation") {
				providers = append(providers, provider)
			}
		}
	}
	seen := map[string]bool{}
	for _, provider := range providers {
		if seen[provider.Name] {
			continue
		}
		seen[provider.Name] = true
		fmt.Fprintf(s.stdout, "- %s (%s)\n", provider.Name, provider.Kind)
	}
}

func (s interactiveSession) printDiscoveryProviders(cfg *project.Config) {
	fmt.Fprintln(s.stdout, "Discovery-capable agents:")
	var providers []agent.ProviderDefinition
	for _, name := range agent.ProviderNames() {
		provider, _ := agent.Provider(name)
		if provider.Supports("discovery") {
			providers = append(providers, provider)
		}
	}
	if cfg != nil {
		for _, provider := range cfg.AgentProviders {
			if err := agent.ValidateProviderDefinition(provider); err == nil && provider.Supports("discovery") {
				providers = append(providers, provider)
			}
		}
	}
	seen := map[string]bool{}
	for _, provider := range providers {
		if seen[provider.Name] {
			continue
		}
		seen[provider.Name] = true
		fmt.Fprintf(s.stdout, "- %s (%s)\n", provider.Name, provider.Kind)
	}
}

func (s interactiveSession) printSourceSuggestions(plan *discovery.Plan) {
	if len(plan.Suggestions) == 0 {
		fmt.Fprintln(s.stdout, "Local source path suggestions: none")
		return
	}
	fmt.Fprintln(s.stdout, "Local source path suggestions:")
	for _, suggestion := range plan.Suggestions {
		fmt.Fprintf(s.stdout, "- %s (score %.1f): %s\n", suggestion.Path, suggestion.Score, suggestion.Reason)
	}
}

func (s interactiveSession) maybeRunAgentDiscovery(projectDir string, plan *discovery.Plan) (*discovery.AgentPlan, string, bool) {
	runAgent, ok := s.confirm("Run agent discovery before creating the run?", false)
	if !ok {
		return nil, "", false
	}
	if !runAgent {
		return nil, "", true
	}
	defaultAgent := s.defaultInteractiveDiscoveryAgent(projectDir)
	discoveryAgent, ok := s.askDiscoveryAgentWithDefault(projectDir, defaultAgent)
	if !ok {
		return nil, "", false
	}
	provider, err := configuredProvider(projectDir, discoveryAgent, "discovery")
	if err != nil {
		fmt.Fprintf(s.stderr, "discovery failed: %v\n", err)
		return nil, "", false
	}
	code := 0
	switch provider.Kind {
	case agent.ProviderLocal:
		fmt.Fprintln(s.stdout, "Local discovery plan already created; continuing without a structured agent handoff.")
		return nil, provider.Name, true
	case agent.ProviderCodex:
		code = runCodexDiscovery(projectDir, plan, codexDiscoveryOptions{}, s.stdout, s.stderr)
	case "command":
		code = runCommandDiscovery(projectDir, plan, provider, "", false, s.stdout, s.stderr)
	default:
		fmt.Fprintf(s.stderr, "discovery failed: provider %q is not implemented for discovery yet\n", provider.Name)
		return nil, "", false
	}
	if code != 0 {
		return nil, "", false
	}
	agentPlanPath := discovery.AgentPlanPath(archive.ProjectPath(projectDir, plan.PlanDir))
	agentPlan, err := discovery.LoadAgentPlan(agentPlanPath)
	if err != nil {
		fmt.Fprintf(s.stderr, "discovery warning: could not load structured agent plan: %v\n", err)
		return nil, provider.Name, true
	}
	return agentPlan, provider.Name, true
}

func (s interactiveSession) defaultInteractiveDiscoveryAgent(projectDir string) string {
	cfg, err := project.Load(projectDir)
	if err == nil {
		defaultAgent := agent.NormalizeProviderName(cfg.DefaultAgent)
		if defaultAgent != "" {
			if provider, err := configuredProvider(projectDir, defaultAgent, "discovery"); err == nil && provider.Supports("discovery") {
				return provider.Name
			}
		}
	}
	return agent.ProviderCodex
}

type clarificationAnswer struct {
	Question string
	Answer   string
}

func (s interactiveSession) applyAgentClarifications(request string, plan *discovery.AgentPlan, providerName string) (string, []clarificationAnswer, bool) {
	if plan == nil || len(plan.ClarifyingQuestions) == 0 {
		return request, nil, true
	}
	if strings.TrimSpace(providerName) == "" {
		providerName = "Agent"
	}
	fmt.Fprintln(s.stdout)
	fmt.Fprintf(s.stdout, "%s requested clarification before generation.\n", providerName)
	answers := make([]clarificationAnswer, 0, len(plan.ClarifyingQuestions))
	for _, question := range plan.ClarifyingQuestions {
		question = strings.TrimSpace(question)
		if question == "" {
			continue
		}
		answer, ok := s.askRequired(question + " ")
		if !ok {
			return "", nil, false
		}
		answers = append(answers, clarificationAnswer{Question: question, Answer: answer})
	}
	if len(answers) == 0 {
		return request, nil, true
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(request))
	b.WriteString("\n\nClarifications:\n")
	for _, answer := range answers {
		fmt.Fprintf(&b, "- %s %s\n", answer.Question, answer.Answer)
	}
	return b.String(), answers, true
}

func (s interactiveSession) externalModeFromAgentPlan(plan *discovery.AgentPlan, providerName string) string {
	mode := "deny"
	if plan == nil || strings.TrimSpace(plan.ExternalMode) == "" {
		return mode
	}
	if strings.TrimSpace(providerName) == "" {
		providerName = "Agent"
	}
	recommended := model.ExternalMode(strings.ToLower(strings.TrimSpace(plan.ExternalMode)))
	if !recommended.Valid() {
		fmt.Fprintf(s.stdout, "Ignoring unsupported %s external mode recommendation %q.\n", providerName, plan.ExternalMode)
		return mode
	}
	if recommended == model.ExternalModeDeny {
		return string(recommended)
	}
	useMode, ok := s.confirm(fmt.Sprintf("Use %s recommended external mode %s?", providerName, recommended), true)
	if !ok {
		return mode
	}
	if useMode {
		mode = string(recommended)
	}
	return mode
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

func sourcePathExists(projectDir, sourcePath string) bool {
	if strings.TrimSpace(sourcePath) == "" {
		return false
	}
	_, err := os.Stat(archive.ProjectPath(projectDir, sourcePath))
	return err == nil
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

func (s interactiveSession) askIntRange(label string, fallback, min, max int) (int, bool) {
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
		if err == nil && value >= min && value <= max {
			return value, true
		}
		fmt.Fprintf(s.stdout, "%s must be between %d and %d.\n", label, min, max)
	}
}

func (s interactiveSession) askFloatRange(label string, fallback, min, max float64) (float64, bool) {
	for {
		answer, ok := s.ask(fmt.Sprintf("%s [%.2f]: ", label, fallback))
		if !ok {
			return 0, false
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return fallback, true
		}
		value, err := strconv.ParseFloat(answer, 64)
		if err == nil && value >= min && value <= max {
			return value, true
		}
		fmt.Fprintf(s.stdout, "%s must be between %.2f and %.2f.\n", label, min, max)
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

func agentDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Agent"
	}
	if name == agent.ProviderCodex {
		return "Codex"
	}
	return name
}
