package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
	"github.com/Automattic/code-crucible/internal/run"
	"github.com/Automattic/code-crucible/internal/scoring"
)

const version = "0.1.0"

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
	taskFile := fs.String("task-file", "", "path to a file containing the optimization task, relative to project directory")
	targetPath := fs.String("target-path", "", "optional file or directory to use as the initial baseline source")
	agentName := fs.String("agent", "", "agent provider name")
	variants := fs.Int("variants", 3, "number of new competitors to request per round")
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
	if err := fs.Parse(args); err != nil {
		return 2
	}

	task := strings.TrimSpace(*optimize)
	if strings.TrimSpace(*taskFile) != "" {
		if task != "" {
			fmt.Fprintf(stderr, "--optimize and --task-file cannot be used together\n")
			return 2
		}
		taskPath := *taskFile
		if !filepath.IsAbs(taskPath) {
			taskPath = filepath.Join(*projectDir, taskPath)
		}
		data, err := os.ReadFile(taskPath)
		if err != nil {
			fmt.Fprintf(stderr, "read task file failed: %v\n", err)
			return 1
		}
		task = strings.TrimSpace(string(data))
	}

	runAgent := *agentName
	if *generateNow {
		if runAgent == "" {
			runAgent = "codex"
		}
		if runAgent != "codex" {
			fmt.Fprintf(stderr, "run --generate currently supports only --agent codex\n")
			return 2
		}
	}

	created, err := run.Create(run.Options{
		ProjectDir:      *projectDir,
		Optimize:        task,
		TargetPath:      *targetPath,
		Agent:           runAgent,
		Variants:        *variants,
		Rounds:          *rounds,
		Exploration:     *exploration,
		Evaluator:       *evaluator,
		EvaluatorScript: *evaluatorScript,
		ExternalMode:    *externalMode,
		Fixtures:        *fixtures,
		AllowHosts:      splitCSV(*allowHosts),
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
			AgentName:         runAgent,
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
	printLeaderboardTable(stdout, rankedResults(board.Results))
	return 0
}

func rankedResults(results []model.CandidateResult) []model.CandidateResult {
	ranked := append([]model.CandidateResult(nil), results...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left := ranked[i]
		right := ranked[j]
		leftPriority := leaderboardStatusPriority(left.Status)
		rightPriority := leaderboardStatusPriority(right.Status)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if left.Metrics.P95LatencyMS != right.Metrics.P95LatencyMS {
			return left.Metrics.P95LatencyMS < right.Metrics.P95LatencyMS
		}
		return left.Candidate.ID < right.Candidate.ID
	})
	return ranked
}

func leaderboardStatusPriority(status string) int {
	switch status {
	case "passed":
		return 0
	case "failed":
		return 1
	case "generated":
		return 2
	case "pending":
		return 3
	default:
		return 4
	}
}

func printLeaderboardTable(stdout io.Writer, results []model.CandidateResult) {
	scoreValues := make([]float64, 0, len(results))
	p95Values := make([]float64, 0, len(results))
	nsPerOpValues := make([]float64, 0, len(results))
	cpuValues := make([]float64, 0, len(results))
	for _, result := range results {
		scoreValues = append(scoreValues, result.Score)
		p95Values = append(p95Values, result.Metrics.P95LatencyMS)
		nsPerOpValues = append(nsPerOpValues, result.Metrics.BenchmarkNsPerOp)
		cpuValues = append(cpuValues, result.Metrics.CPUUserSeconds+result.Metrics.CPUSystemSeconds)
	}

	scoreFormatter := newDynamicFloatFormatter(scoreValues, 2, 4)
	p95Formatter := newDynamicFloatFormatter(p95Values, 2, 6)
	nsPerOpFormatter := newDynamicFloatFormatter(nsPerOpValues, 0, 4)
	cpuFormatter := newDynamicFloatFormatter(cpuValues, 2, 4)
	baselinePrimary := leaderboardBaselinePrimary(results)
	baselineMemory := leaderboardBaselineMemory(results)

	rows := make([][]string, 0, len(results))
	for i, result := range results {
		rank := "-"
		if result.Status == "passed" {
			rank = fmt.Sprintf("%d", i+1)
		}
		rows = append(rows, []string{
			rank,
			result.Candidate.ID,
			result.Status,
			scoreFormatter.format(result.Score),
			formatOptionalFloat(p95Formatter, result.Metrics.P95LatencyMS),
			formatOptionalFloat(nsPerOpFormatter, result.Metrics.BenchmarkNsPerOp),
			formatSpeedup(baselinePrimary, scoring.PrimaryMetric(result.Metrics)),
			formatBytes(result.Metrics.MemoryPeakBytes),
			formatRelativeUsage(scoring.MemoryMetric(result.Metrics), baselineMemory),
			cpuFormatter.format(result.Metrics.CPUUserSeconds + result.Metrics.CPUSystemSeconds),
			fmt.Sprintf("%d", result.Metrics.ExternalCallCount),
		})
	}

	printTable(stdout, []string{"Rank", "Candidate", "Status", "Score", "P95 ms", "ns/op", "Speedup", "Memory", "Mem/Base", "Eval CPU s", "Calls"}, rows)
}

func leaderboardBaselinePrimary(results []model.CandidateResult) float64 {
	for _, result := range results {
		if result.Candidate.Baseline {
			return scoring.PrimaryMetric(result.Metrics)
		}
	}
	return 0
}

func leaderboardBaselineMemory(results []model.CandidateResult) float64 {
	for _, result := range results {
		if result.Candidate.Baseline {
			return scoring.MemoryMetric(result.Metrics)
		}
	}
	return 0
}

type dynamicFloatFormatter struct {
	decimals   int
	scientific bool
}

func newDynamicFloatFormatter(values []float64, defaultDecimals, maxDecimals int) dynamicFloatFormatter {
	if maxDecimals < defaultDecimals {
		maxDecimals = defaultDecimals
	}

	decimals := defaultDecimals
	finite := finiteAbsValues(values)
	if len(finite) == 0 {
		return dynamicFloatFormatter{decimals: decimals}
	}

	sort.Float64s(finite)
	minPositive := finite[0]
	maxValue := finite[len(finite)-1]
	if maxValue >= 1_000_000 || minPositive < math.Pow10(-maxDecimals) || maxValue/minPositive >= 1_000_000 {
		return dynamicFloatFormatter{decimals: maxDecimals, scientific: true}
	}

	if minPositive < 1 {
		decimals = maxInt(decimals, int(math.Ceil(-math.Log10(minPositive)))+2)
	}
	if minDelta := minDistinctDelta(finite); minDelta > 0 && minDelta < 1 {
		decimals = maxInt(decimals, int(math.Ceil(-math.Log10(minDelta)))+1)
	}
	if decimals > maxDecimals {
		decimals = maxDecimals
	}
	return dynamicFloatFormatter{decimals: decimals}
}

func finiteAbsValues(values []float64) []float64 {
	finite := make([]float64, 0, len(values))
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value == 0 {
			continue
		}
		finite = append(finite, math.Abs(value))
	}
	return finite
}

func minDistinctDelta(sorted []float64) float64 {
	var minDelta float64
	for i := 1; i < len(sorted); i++ {
		delta := sorted[i] - sorted[i-1]
		if delta <= 0 {
			continue
		}
		if minDelta == 0 || delta < minDelta {
			minDelta = delta
		}
	}
	return minDelta
}

func (f dynamicFloatFormatter) format(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "-"
	}
	if f.scientific && value != 0 {
		return fmt.Sprintf("%.*e", maxInt(3, f.decimals), value)
	}
	formatted := fmt.Sprintf("%.*f", f.decimals, value)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "" || formatted == "-0" {
		return "0"
	}
	return formatted
}

func formatOptionalFloat(formatter dynamicFloatFormatter, value float64) string {
	if value <= 0 {
		return "-"
	}
	return formatter.format(value)
}

func formatSpeedup(baseline, value float64) string {
	return formatMultiplier(baseline, value)
}

func formatMultiplier(baseline, value float64) string {
	if baseline <= 0 || value <= 0 || math.IsNaN(baseline) || math.IsNaN(value) || math.IsInf(baseline, 0) || math.IsInf(value, 0) {
		return "-"
	}
	multiplier := baseline / value
	if math.Abs(multiplier-1) < 0.005 {
		return "1x"
	}
	switch {
	case multiplier >= 100:
		return fmt.Sprintf("%.0fx", multiplier)
	case multiplier >= 10:
		return fmt.Sprintf("%.1fx", multiplier)
	default:
		return fmt.Sprintf("%.2fx", multiplier)
	}
}

func formatRelativeUsage(value, baseline float64) string {
	if baseline <= 0 || value <= 0 || math.IsNaN(baseline) || math.IsNaN(value) || math.IsInf(baseline, 0) || math.IsInf(value, 0) {
		return "-"
	}
	ratio := value / baseline
	if math.Abs(ratio-1) < 0.005 {
		return "1x"
	}
	switch {
	case ratio < 0.01:
		return fmt.Sprintf("%.4fx", ratio)
	case ratio < 0.1:
		return fmt.Sprintf("%.3fx", ratio)
	case ratio < 10:
		return fmt.Sprintf("%.2fx", ratio)
	case ratio < 100:
		return fmt.Sprintf("%.1fx", ratio)
	default:
		return fmt.Sprintf("%.0fx", ratio)
	}
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "-"
	}
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	scaled := float64(value)
	unitIndex := -1
	for scaled >= unit && unitIndex < len(units)-1 {
		scaled /= unit
		unitIndex++
	}
	if math.Abs(scaled-math.Round(scaled)) < 0.005 {
		return fmt.Sprintf("%.0f %s", scaled, units[unitIndex])
	}
	if scaled >= 100 {
		return fmt.Sprintf("%.0f %s", scaled, units[unitIndex])
	}
	if scaled >= 10 {
		return fmt.Sprintf("%.1f %s", scaled, units[unitIndex])
	}
	return fmt.Sprintf("%.2f %s", scaled, units[unitIndex])
}

func printTable(stdout io.Writer, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	printTableRow(stdout, widths, headers)
	for _, row := range rows {
		printTableRow(stdout, widths, row)
	}
}

func printTableRow(stdout io.Writer, widths []int, row []string) {
	for i, cell := range row {
		padding := widths[i]
		if i == len(row)-1 {
			fmt.Fprintf(stdout, "%-*s\n", padding, cell)
			return
		}
		fmt.Fprintf(stdout, "%-*s  ", padding, cell)
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func runAdopt(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := fs.String("project", ".", "project directory containing .crucible")
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
	projectDir := fs.String("project", ".", "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	candidateID := fs.String("candidate", "", "candidate ID to evaluate; defaults to all leaderboard candidates")
	timeoutValue := fs.String("timeout", "", "optional evaluator timeout, such as 30s or 2m")
	jobs := fs.Int("jobs", 1, "maximum number of candidates to evaluate concurrently")
	nice := fs.Int("nice", 10, "nice priority for evaluator processes; 0 disables priority adjustment")
	cpuLimit := fs.Int("cpu-limit", 0, "optional CPU affinity limit for evaluator processes; 0 disables affinity control")
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
		Env:         []string(env),
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

func runGenerate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := fs.String("project", ".", "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	agentName := fs.String("agent", "codex", "agent provider to run")
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
	if opts.AgentName == "" {
		opts.AgentName = "codex"
	}
	if opts.AgentName != "codex" {
		fmt.Fprintf(stderr, "generate currently supports only --agent codex\n")
		return 2
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
	if cfg.ProjectDir == "" {
		cfg.ProjectDir, _ = filepath.Abs(opts.ProjectDir)
	}

	runDir := cfg.RunDir
	if runDir == "" {
		runDir = filepath.Dir(configPath)
	}
	promptPath := cfg.PromptPath
	if promptPath == "" {
		promptPath = filepath.Join(runDir, "prompts", "generation-round-0001.md")
	}
	if opts.OutputLastMessage == "" {
		opts.OutputLastMessage = filepath.Join(runDir, "agents", "codex-final.md")
	}

	codexOpts := agent.CodexOptions{
		Binary:            opts.CodexBin,
		ProjectDir:        cfg.ProjectDir,
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
		fmt.Fprintf(stdout, "Codex command:\n%s\n\n", agent.FormatCommand(command))
		fmt.Fprintf(stdout, "Prompt stdin: %s\n", promptPath)
		fmt.Fprintf(stdout, "Project root: %s\n", cfg.ProjectDir)
		fmt.Fprintf(stdout, "Run archive: %s\n", runDir)
		fmt.Fprintf(stdout, "Final message: %s\n", opts.OutputLastMessage)
		return 0
	}

	fmt.Fprintf(stdout, "Running Codex for run %s\n", cfg.ID)
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

func printAdoptionReport(stdout, stderr io.Writer, report *run.AdoptionReport) {
	fmt.Fprintf(stdout, "\nAdoption report for run %s\n", report.RunID)
	fmt.Fprintf(stdout, "Leaderboard: %s\n", report.LeaderboardPath)
	fmt.Fprintf(stdout, "Added: %s\n", formatIDList(report.Added))
	fmt.Fprintf(stdout, "Already present: %s\n", formatIDList(report.Existing))
	if len(report.Invalid) == 0 {
		fmt.Fprintln(stdout, "Invalid: none")
		return
	}

	fmt.Fprintf(stderr, "Invalid generated candidates:\n")
	for _, issue := range report.Invalid {
		fmt.Fprintf(stderr, "- %s: %s (%s)\n", issue.ID, issue.Reason, issue.Path)
	}
}

func formatIDList(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

func printEvaluationReport(stdout io.Writer, report *run.EvaluationReport) {
	fmt.Fprintf(stdout, "\nEvaluation report for run %s\n", report.RunID)
	fmt.Fprintf(stdout, "Evaluator: %s\n", report.EvaluatorPath)
	fmt.Fprintf(stdout, "Leaderboard: %s\n", report.LeaderboardPath)
	if len(report.Results) == 0 {
		fmt.Fprintln(stdout, "No candidates evaluated.")
		return
	}

	scoreValues := make([]float64, 0, len(report.Results))
	for _, result := range report.Results {
		scoreValues = append(scoreValues, result.Score)
	}
	scoreFormatter := newDynamicFloatFormatter(scoreValues, 2, 4)
	rows := make([][]string, 0, len(report.Results))
	for _, result := range report.Results {
		rows = append(rows, []string{
			result.ID,
			result.Status,
			scoreFormatter.format(result.Score),
		})
	}
	printTable(stdout, []string{"Candidate", "Status", "Score"}, rows)
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

func printHelp(w io.Writer) {
	fmt.Fprint(w, `Code Crucible

Usage:
  crucible init [--project DIR] [--name NAME]
  crucible run (--optimize TEXT | --task-file PATH) [--project DIR] [--target-path PATH] [--variants N] [--generate]
  crucible generate [--project DIR] [--run RUN_ID] [--agent codex]
  crucible adopt [--project DIR] [--run RUN_ID]
  crucible evaluate [--project DIR] [--run RUN_ID] [--candidate ID] [--jobs N] [--nice N] [--cpu-limit N]
  crucible leaderboard [--project DIR] [--run RUN_ID] [--json]
  crucible inspect [--project DIR] [--run RUN_ID] [candidate-id]
  crucible version

Core workflow:
  1. Run "crucible init" inside an existing project.
  2. Run "crucible run --optimize ..." or "crucible run --task-file ..." to create a tournament workspace.
  3. Fill in docs/interfaces.md and evaluator/evaluator.sh.
  4. Run "crucible generate --agent codex" to ask Codex for competitors, or use "crucible run --generate" as an explicit shortcut.
  5. Run "crucible evaluate" to execute the run evaluator and update leaderboard results.

`)
}
