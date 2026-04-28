package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/discovery"
	"github.com/Automattic/code-crucible/internal/indexer"
	"github.com/Automattic/code-crucible/internal/project"
	"github.com/Automattic/code-crucible/internal/report"
	"github.com/Automattic/code-crucible/internal/run"
)

const defaultVariantCount = 3

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory to initialize")
	name := fs.String("name", "", "project name")
	defaultAgent := fs.String("default-agent", "", "default agent provider for generation and evolution")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := project.InitWithOptions(*projectDir, project.InitOptions{
		Name:         *name,
		DefaultAgent: *defaultAgent,
	})
	if err != nil {
		fmt.Fprintf(stderr, "init failed: %v\n", err)
		return 1
	}

	abs, _ := filepath.Abs(*projectDir)
	fmt.Fprintf(stdout, "Initialized Code Crucible work area for %s\n", cfg.ProjectName)
	fmt.Fprintf(stdout, "Work area: %s\n", project.WorkDir(abs))
	fmt.Fprintf(stdout, "Default agent: %s\n", cfg.DefaultAgent)
	return 0
}

func runTournament(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing or receiving .crucible")
	optimize := fs.String("optimize", "", "feature, function, or behavior to optimize")
	taskFile := fs.String("task-file", "", "path to a file containing the optimization task, relative to project directory")
	sourcePath := fs.String("source-path", "", "optional file or directory to use as the initial baseline source")
	agentPlanPath := fs.String("agent-plan", "", "optional structured discovery handoff JSON to seed interface and evaluator scaffolds")
	agentName := fs.String("agent", "", "agent provider name")
	variants := fs.Int("variants", defaultVariantCount, "number of new competitors to request per round")
	rounds := fs.Int("rounds", 1, "number of tournament rounds to prepare")
	exploration := fs.Float64("exploration", 0.35, "0..1 balance between iterative improvement and creative alternatives")
	evaluator := fs.String("evaluator", "", "deterministic evaluator command to run from each candidate src directory")
	evaluatorScript := fs.String("evaluator-script", "", "path to a full evaluator script copied into the run archive")
	externalMode := fs.String("external-mode", "deny", "external call mode: deny, allowlist, mock, replay, record")
	fixtures := fs.String("external-fixtures", "", "fixtures path for mock or replay mode")
	allowHosts := fs.String("allow-hosts", "", "comma-separated host allowlist")
	generateNow := fs.Bool("generate", false, "run the selected agent immediately after creating the run")
	codexBin := fs.String("codex-bin", agent.DefaultCodexBinary, "Codex CLI binary used with --generate")
	model := fs.String("model", "", "Codex model override used with --generate")
	profile := fs.String("profile", "", "Codex config profile used with --generate")
	sandbox := fs.String("sandbox", agent.DefaultCodexSandbox, "Codex sandbox mode used with --generate")
	approval := fs.String("approval", agent.DefaultApprovalPolicy, "Codex approval policy used with --generate")
	eventJSON := fs.Bool("event-json", true, "ask Codex to emit JSONL events when used with --generate")
	skipGitRepoCheck := fs.Bool("skip-git-repo-check", true, "allow Codex to run when the host project is not a git repository")
	outputLastMessage := fs.String("output-last-message", "", "path for Codex final response; defaults to a run artifact")
	if err := fs.Parse(flagsAnywhere(args, fs)); err != nil {
		return 2
	}

	task, code := optimizationRequest(*projectDir, strings.TrimSpace(*optimize), strings.TrimSpace(*taskFile), strings.TrimSpace(strings.Join(fs.Args(), " ")), stderr)
	if code != 0 {
		return code
	}

	var agentPlan *discovery.AgentPlan
	if strings.TrimSpace(*agentPlanPath) != "" {
		loaded, err := loadAgentPlanForProject(*projectDir, *agentPlanPath)
		if err != nil {
			fmt.Fprintf(stderr, "read agent plan failed: %v\n", err)
			return 1
		}
		agentPlan = loaded
		if strings.TrimSpace(*sourcePath) == "" && strings.TrimSpace(agentPlan.SourcePath) != "" {
			*sourcePath = strings.TrimSpace(agentPlan.SourcePath)
		}
	}

	runAgent := agent.NormalizeProviderName(*agentName)
	if runAgent != "" {
		provider, err := configuredProvider(*projectDir, runAgent, "generation")
		if err != nil {
			fmt.Fprintf(stderr, "run failed: %v\n", err)
			return 2
		}
		runAgent = provider.Name
	}
	if *generateNow && runAgent == "" {
		cfg, err := project.Ensure(*projectDir)
		if err != nil {
			fmt.Fprintf(stderr, "run setup failed: %v\n", err)
			return 1
		}
		runAgent = cfg.DefaultAgent
		provider, err := configuredProvider(*projectDir, runAgent, "generation")
		if err != nil {
			fmt.Fprintf(stderr, "run --generate failed: %v\n", err)
			return 2
		}
		runAgent = provider.Name
	}

	created, err := run.Create(run.Options{
		ProjectDir:      *projectDir,
		Optimize:        task,
		SourcePath:      *sourcePath,
		Agent:           runAgent,
		Variants:        *variants,
		Rounds:          *rounds,
		Exploration:     *exploration,
		Evaluator:       *evaluator,
		EvaluatorScript: *evaluatorScript,
		ExternalMode:    *externalMode,
		Fixtures:        *fixtures,
		AllowHosts:      splitCSV(*allowHosts),
		AgentPlan:       agentPlan,
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

	if *generateNow {
		fmt.Fprintln(stdout)
		return generateWithOptions(generationOptions{
			ProjectDir:        *projectDir,
			RunID:             created.ID,
			AgentName:         "",
			CodexBin:          *codexBin,
			Model:             *model,
			Profile:           *profile,
			Sandbox:           *sandbox,
			Approval:          *approval,
			EventJSON:         *eventJSON,
			SkipGitRepoCheck:  *skipGitRepoCheck,
			OutputLastMessage: *outputLastMessage,
		}, stdout, stderr)
	}

	return 0
}

func configuredProvider(projectDir, name, capability string) (agent.ProviderDefinition, error) {
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return agent.ProviderDefinition{}, err
	}
	var configured map[string]agent.ProviderDefinition
	if cfg, err := project.Load(absProject); err == nil {
		configured = cfg.AgentProviders
	}
	provider, ok := agent.ProviderFromConfig(name, configured)
	if !ok {
		return agent.ProviderDefinition{}, fmt.Errorf("unsupported agent provider %q", agent.NormalizeProviderName(name))
	}
	if err := agent.ValidateProviderDefinition(provider); err != nil {
		return agent.ProviderDefinition{}, err
	}
	if !provider.Supports(capability) {
		return agent.ProviderDefinition{}, fmt.Errorf("agent provider %q does not support %s", provider.Name, capability)
	}
	return provider, nil
}

func loadAgentPlanForProject(projectDir, path string) (*discovery.AgentPlan, error) {
	planPath := path
	if !filepath.IsAbs(planPath) {
		planPath = archive.ProjectPath(projectDir, planPath)
	}
	return discovery.LoadAgentPlan(planPath)
}

func optimizationRequest(projectDir, optimize, taskFile, positional string, stderr io.Writer) (string, int) {
	taskSources := 0
	if optimize != "" {
		taskSources++
	}
	if taskFile != "" {
		taskSources++
	}
	if positional != "" {
		taskSources++
	}
	if taskSources > 1 {
		fmt.Fprintf(stderr, "provide the optimization request only once: as a positional argument, --optimize, or --task-file\n")
		return "", 2
	}
	if positional != "" {
		return positional, 0
	}
	if taskFile == "" {
		return optimize, 0
	}
	taskPath := taskFile
	if !filepath.IsAbs(taskPath) {
		taskPath = filepath.Join(projectDir, taskPath)
	}
	data, err := os.ReadFile(taskPath)
	if err != nil {
		fmt.Fprintf(stderr, "read task file failed: %v\n", err)
		return "", 1
	}
	return strings.TrimSpace(string(data)), 0
}

type boolFlag interface {
	IsBoolFlag() bool
}

func flagsAnywhere(args []string, fs *flag.FlagSet) []string {
	flagArgs := make([]string, 0, len(args))
	positionalArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionalArgs = append(positionalArgs, args[i+1:]...)
			break
		}
		name, hasValue, isFlag := splitFlagName(arg)
		if !isFlag {
			positionalArgs = append(positionalArgs, arg)
			continue
		}
		registered := fs.Lookup(name)
		if registered == nil {
			flagArgs = append(flagArgs, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		if hasValue || flagAcceptsNoValue(registered) {
			continue
		}
		if i+1 < len(args) {
			flagArgs = append(flagArgs, args[i+1])
			i++
		}
	}
	return append(flagArgs, positionalArgs...)
}

func splitFlagName(arg string) (string, bool, bool) {
	prefix := ""
	switch {
	case strings.HasPrefix(arg, "--"):
		prefix = "--"
	case strings.HasPrefix(arg, "-"):
		prefix = "-"
	default:
		return "", false, false
	}
	if arg == prefix {
		return "", false, false
	}
	withoutPrefix := strings.TrimPrefix(arg, prefix)
	if withoutPrefix == "" {
		return "", false, false
	}
	name, _, hasValue := strings.Cut(withoutPrefix, "=")
	if name == "" {
		return "", false, false
	}
	return name, hasValue, true
}

func flagAcceptsNoValue(f *flag.Flag) bool {
	boolValue, ok := f.Value.(boolFlag)
	return ok && boolValue.IsBoolFlag()
}

func runIndex(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing or receiving .crucible")
	runID := fs.String("run", "", "optional run ID to rebuild in the index")
	jsonOut := fs.Bool("json", false, "print raw index rebuild report JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	report, err := indexer.Rebuild(indexer.Options{
		ProjectDir: *projectDir,
		RunID:      *runID,
	})
	if err != nil {
		fmt.Fprintf(stderr, "index failed: %v\n", err)
		return 1
	}

	if *jsonOut {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "index failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}

	fmt.Fprintf(stdout, "Rebuilt SQLite index\n")
	fmt.Fprintf(stdout, "Index: %s\n", report.IndexPath)
	fmt.Fprintf(stdout, "Schema: %d\n", report.Schema)
	fmt.Fprintf(stdout, "Rebuild mode: %s\n", report.RebuildMode)
	if report.SchemaReset {
		fmt.Fprintf(stdout, "Schema reset: yes\n")
	}
	fmt.Fprintf(stdout, "Runs: %d\n", report.Runs)
	fmt.Fprintf(stdout, "Candidates: %d\n", report.Candidates)
	return 0
}

func runReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	outputPath := fs.String("output", "", "HTML output path; defaults to reports/leaderboard.html in the run archive")
	jsonOut := fs.Bool("json", false, "print raw report metadata JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	generated, err := report.GenerateHTML(report.Options{
		ProjectDir: *projectDir,
		RunID:      *runID,
		OutputPath: *outputPath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "report failed: %v\n", err)
		return 1
	}

	if *jsonOut {
		data, err := json.MarshalIndent(generated, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "report failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}

	fmt.Fprintf(stdout, "Wrote HTML report\n")
	fmt.Fprintf(stdout, "Report: %s\n", generated.OutputPath)
	fmt.Fprintf(stdout, "Run: %s\n", generated.RunID)
	fmt.Fprintf(stdout, "Candidates: %d\n", generated.Candidates)
	fmt.Fprintf(stdout, "Passed: %d\n", generated.Passed)
	fmt.Fprintf(stdout, "Failed: %d\n", generated.Failed)
	fmt.Fprintf(stdout, "Pending: %d\n", generated.Pending)
	return 0
}

func runNextRound(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("next-round", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	parents := fs.Int("parents", defaultVariantCount, "number of passed candidates to seed the next round")
	jsonOut := fs.Bool("json", false, "print raw next-round report JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *parents < 1 {
		fmt.Fprintf(stderr, "next-round failed: --parents must be at least 1\n")
		return 2
	}

	report, err := run.PrepareNextRound(run.NextRoundOptions{
		ProjectDir: *projectDir,
		RunID:      *runID,
		Parents:    *parents,
	})
	if err != nil {
		fmt.Fprintf(stderr, "next-round failed: %v\n", err)
		return 1
	}

	if *jsonOut {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "next-round failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}

	fmt.Fprintf(stdout, "Prepared round %d for run %s\n", report.Round, report.RunID)
	fmt.Fprintf(stdout, "Round directory: %s\n", report.RoundDir)
	fmt.Fprintf(stdout, "Prompt: %s\n", report.PromptPath)
	fmt.Fprintf(stdout, "Parent candidates: %s\n", strings.Join(report.ParentIDs, ", "))
	fmt.Fprintf(stdout, "Next candidate: %s\n", report.NextCandidateID)
	return 0
}

func runEvolve(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("evolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	rounds := fs.Int("rounds", 1, "number of generate/evaluate cycles to run")
	parents := fs.Int("parents", defaultVariantCount, "number of passed candidates to seed each follow-up round")
	agentName := fs.String("agent", "", "agent provider to run; defaults to the run or project default")
	codexBin := fs.String("codex-bin", agent.DefaultCodexBinary, "Codex CLI binary")
	model := fs.String("model", "", "Codex model override")
	profile := fs.String("profile", "", "Codex config profile")
	codexSandbox := fs.String("sandbox", agent.DefaultCodexSandbox, "Codex sandbox mode")
	approval := fs.String("approval", agent.DefaultApprovalPolicy, "Codex approval policy")
	eventJSON := fs.Bool("event-json", true, "ask Codex to emit JSONL events")
	skipGitRepoCheck := fs.Bool("skip-git-repo-check", true, "allow Codex to run when the host project is not a git repository")
	outputLastMessage := fs.String("output-last-message", "", "path for Codex final response; defaults to a run artifact")
	timeoutValue := fs.String("timeout", "", "optional evaluator timeout, such as 30s or 2m")
	jobs := fs.Int("jobs", 1, "maximum number of candidates to evaluate concurrently")
	nice := fs.Int("nice", 10, "nice priority for evaluator processes; 0 disables priority adjustment")
	cpuLimit := fs.Int("cpu-limit", 0, "optional CPU limit for evaluator processes; local mode uses taskset affinity")
	warmups := fs.Int("warmups", 0, "number of evaluator warmup runs to discard before measured repetitions")
	repetitions := fs.Int("repetitions", 1, "number of measured evaluator repetitions to aggregate")
	outlierMode := fs.String("outliers", "none", "outlier handling for measured repetitions: none or trim-min-max")
	sampleStat := fs.String("sample-stat", "mean", "aggregate statistic for measured repetitions: mean, median, min, or max")
	sandboxProfile := fs.String("sandbox-profile", "default", "evaluator sandbox profile: default, strict, or networked")
	sandboxEngine := fs.String("sandbox-engine", "local", "evaluator sandbox engine: local, docker, or podman")
	sandboxImage := fs.String("sandbox-image", "", "container image for docker or podman evaluator sandboxes")
	sandboxNetwork := fs.String("sandbox-network", "", "container network mode for docker or podman evaluator sandboxes")
	memoryLimit := fs.String("memory-limit", "", "container memory limit for evaluator sandboxes, such as 1g or 512m")
	pidsLimit := fs.Int("pids-limit", 0, "container process limit for evaluator sandboxes; 0 uses the profile default")
	var env repeatedStrings
	fs.Var(&env, "env", "environment variable for evaluators in KEY=VALUE form; may be repeated")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *rounds < 1 {
		fmt.Fprintf(stderr, "evolve failed: --rounds must be at least 1\n")
		return 2
	}
	if *parents < 1 {
		fmt.Fprintf(stderr, "evolve failed: --parents must be at least 1\n")
		return 2
	}
	if *jobs < 1 {
		fmt.Fprintf(stderr, "evolve failed: --jobs must be at least 1\n")
		return 2
	}
	if *nice < 0 || *nice > 19 {
		fmt.Fprintf(stderr, "evolve failed: --nice must be between 0 and 19\n")
		return 2
	}
	if *cpuLimit < 0 {
		fmt.Fprintf(stderr, "evolve failed: --cpu-limit must be at least 0\n")
		return 2
	}
	if *warmups < 0 {
		fmt.Fprintf(stderr, "evolve failed: --warmups must be at least 0\n")
		return 2
	}
	if *repetitions < 1 {
		fmt.Fprintf(stderr, "evolve failed: --repetitions must be at least 1\n")
		return 2
	}
	if !validOutlierMode(*outlierMode) {
		fmt.Fprintf(stderr, "evolve failed: --outliers must be none or trim-min-max\n")
		return 2
	}
	if !validSampleStat(*sampleStat) {
		fmt.Fprintf(stderr, "evolve failed: --sample-stat must be mean, median, min, or max\n")
		return 2
	}
	if *pidsLimit < 0 {
		fmt.Fprintf(stderr, "evolve failed: --pids-limit must be at least 0\n")
		return 2
	}
	evaluatorSandbox, err := run.NormalizeSandboxOptions(run.SandboxOptions{
		Profile:     *sandboxProfile,
		Engine:      *sandboxEngine,
		Image:       *sandboxImage,
		Network:     *sandboxNetwork,
		MemoryLimit: *memoryLimit,
		PIDsLimit:   *pidsLimit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "evolve failed: %v\n", err)
		return 2
	}
	timeout := time.Duration(0)
	if strings.TrimSpace(*timeoutValue) != "" {
		parsed, err := time.ParseDuration(*timeoutValue)
		if err != nil {
			fmt.Fprintf(stderr, "evolve failed: invalid --timeout %q: %v\n", *timeoutValue, err)
			return 2
		}
		timeout = parsed
	}

	for cycle := 1; cycle <= *rounds; cycle++ {
		fmt.Fprintf(stdout, "\nEvolution cycle %d of %d\n", cycle, *rounds)
		code := generateWithOptions(generationOptions{
			ProjectDir:        *projectDir,
			RunID:             *runID,
			AgentName:         *agentName,
			CodexBin:          *codexBin,
			Model:             *model,
			Profile:           *profile,
			Sandbox:           *codexSandbox,
			Approval:          *approval,
			EventJSON:         *eventJSON,
			SkipGitRepoCheck:  *skipGitRepoCheck,
			OutputLastMessage: *outputLastMessage,
		}, stdout, stderr)
		if code != 0 {
			return code
		}

		report, err := run.EvaluateCandidates(run.EvaluationOptions{
			ProjectDir:  *projectDir,
			RunID:       *runID,
			Timeout:     timeout,
			Adopt:       false,
			Jobs:        *jobs,
			Nice:        *nice,
			CPULimit:    *cpuLimit,
			Warmups:     *warmups,
			Repetitions: *repetitions,
			OutlierMode: *outlierMode,
			SampleStat:  *sampleStat,
			Env:         []string(env),
			Sandbox:     evaluatorSandbox,
		})
		if err != nil {
			fmt.Fprintf(stderr, "evolve failed: %v\n", err)
			return 1
		}
		printEvaluationReport(stdout, report)

		if cycle < *rounds {
			next, err := run.PrepareNextRound(run.NextRoundOptions{
				ProjectDir: *projectDir,
				RunID:      *runID,
				Parents:    *parents,
			})
			if err != nil {
				fmt.Fprintf(stderr, "evolve failed: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "\nPrepared round %d\n", next.Round)
			fmt.Fprintf(stdout, "Round directory: %s\n", next.RoundDir)
			fmt.Fprintf(stdout, "Prompt: %s\n", next.PromptPath)
			fmt.Fprintf(stdout, "Parent candidates: %s\n", strings.Join(next.ParentIDs, ", "))
		}
	}
	return 0
}

func runAdopt(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	model := fs.String("model", "", "model name to fill into adopted candidates when missing")
	jsonOut := fs.Bool("json", false, "print raw adoption report JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	report, err := run.AdoptCandidates(run.AdoptionOptions{
		ProjectDir: *projectDir,
		RunID:      *runID,
		Model:      *model,
	})
	if err != nil {
		fmt.Fprintf(stderr, "adopt failed: %v\n", err)
		return 1
	}

	if *jsonOut {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "adopt failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}

	printAdoptionReport(stdout, stderr, report)
	return 0
}

func runEvaluate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	candidateID := fs.String("candidate", "", "candidate ID to evaluate; defaults to all leaderboard candidates")
	timeoutValue := fs.String("timeout", "", "optional evaluator timeout, such as 30s or 2m")
	jobs := fs.Int("jobs", 1, "maximum number of candidates to evaluate concurrently")
	nice := fs.Int("nice", 10, "nice priority for evaluator processes; 0 disables priority adjustment")
	cpuLimit := fs.Int("cpu-limit", 0, "optional CPU limit for evaluator processes; local mode uses taskset affinity")
	warmups := fs.Int("warmups", 0, "number of evaluator warmup runs to discard before measured repetitions")
	repetitions := fs.Int("repetitions", 1, "number of measured evaluator repetitions to aggregate")
	outlierMode := fs.String("outliers", "none", "outlier handling for measured repetitions: none or trim-min-max")
	sampleStat := fs.String("sample-stat", "mean", "aggregate statistic for measured repetitions: mean, median, min, or max")
	sandboxProfile := fs.String("sandbox-profile", "default", "evaluator sandbox profile: default, strict, or networked")
	sandboxEngine := fs.String("sandbox-engine", "local", "evaluator sandbox engine: local, docker, or podman")
	sandboxImage := fs.String("sandbox-image", "", "container image for docker or podman evaluator sandboxes")
	sandboxNetwork := fs.String("sandbox-network", "", "container network mode for docker or podman evaluator sandboxes")
	memoryLimit := fs.String("memory-limit", "", "container memory limit for evaluator sandboxes, such as 1g or 512m")
	pidsLimit := fs.Int("pids-limit", 0, "container process limit for evaluator sandboxes; 0 uses the profile default")
	var env repeatedStrings
	fs.Var(&env, "env", "environment variable for evaluators in KEY=VALUE form; may be repeated")
	adoptBefore := fs.Bool("adopt", true, "adopt generated candidates before evaluation")
	jsonOut := fs.Bool("json", false, "print raw evaluation report JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *jobs < 1 {
		fmt.Fprintf(stderr, "evaluate failed: --jobs must be at least 1\n")
		return 2
	}
	if *nice < 0 || *nice > 19 {
		fmt.Fprintf(stderr, "evaluate failed: --nice must be between 0 and 19\n")
		return 2
	}
	if *cpuLimit < 0 {
		fmt.Fprintf(stderr, "evaluate failed: --cpu-limit must be at least 0\n")
		return 2
	}
	if *warmups < 0 {
		fmt.Fprintf(stderr, "evaluate failed: --warmups must be at least 0\n")
		return 2
	}
	if *repetitions < 1 {
		fmt.Fprintf(stderr, "evaluate failed: --repetitions must be at least 1\n")
		return 2
	}
	if !validOutlierMode(*outlierMode) {
		fmt.Fprintf(stderr, "evaluate failed: --outliers must be none or trim-min-max\n")
		return 2
	}
	if !validSampleStat(*sampleStat) {
		fmt.Fprintf(stderr, "evaluate failed: --sample-stat must be mean, median, min, or max\n")
		return 2
	}
	if *pidsLimit < 0 {
		fmt.Fprintf(stderr, "evaluate failed: --pids-limit must be at least 0\n")
		return 2
	}
	sandbox, err := run.NormalizeSandboxOptions(run.SandboxOptions{
		Profile:     *sandboxProfile,
		Engine:      *sandboxEngine,
		Image:       *sandboxImage,
		Network:     *sandboxNetwork,
		MemoryLimit: *memoryLimit,
		PIDsLimit:   *pidsLimit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "evaluate failed: %v\n", err)
		return 2
	}

	timeout := time.Duration(0)
	if strings.TrimSpace(*timeoutValue) != "" {
		parsed, err := time.ParseDuration(*timeoutValue)
		if err != nil {
			fmt.Fprintf(stderr, "evaluate failed: invalid --timeout %q: %v\n", *timeoutValue, err)
			return 2
		}
		timeout = parsed
	}

	report, err := run.EvaluateCandidates(run.EvaluationOptions{
		ProjectDir:  *projectDir,
		RunID:       *runID,
		CandidateID: *candidateID,
		Timeout:     timeout,
		Adopt:       *adoptBefore,
		Jobs:        *jobs,
		Nice:        *nice,
		CPULimit:    *cpuLimit,
		Warmups:     *warmups,
		Repetitions: *repetitions,
		OutlierMode: *outlierMode,
		SampleStat:  *sampleStat,
		Env:         []string(env),
		Sandbox:     sandbox,
	})
	if err != nil {
		fmt.Fprintf(stderr, "evaluate failed: %v\n", err)
		return 1
	}

	if *jsonOut {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "evaluate failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}

	if report.Adoption != nil {
		printAdoptionReport(stdout, stderr, report.Adoption)
	}
	printEvaluationReport(stdout, report)
	return 0
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
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

func validOutlierMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "none", "trim-min-max":
		return true
	default:
		return false
	}
}

func validSampleStat(stat string) bool {
	switch strings.ToLower(strings.TrimSpace(stat)) {
	case "", "mean", "median", "min", "max":
		return true
	default:
		return false
	}
}

type repeatedStrings []string

func (v *repeatedStrings) String() string {
	return strings.Join(*v, ",")
}

func (v *repeatedStrings) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("value cannot be empty")
	}
	if !strings.Contains(value, "=") {
		return fmt.Errorf("value must use KEY=VALUE form")
	}
	*v = append(*v, value)
	return nil
}
