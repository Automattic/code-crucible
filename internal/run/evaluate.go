package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/scoring"
)

type EvaluationOptions struct {
	ProjectDir  string
	RunID       string
	CandidateID string
	Timeout     time.Duration
	Adopt       bool
	Jobs        int
	Nice        int
	CPULimit    int
	Warmups     int
	Repetitions int
	Env         []string
	Sandbox     SandboxOptions
}

type SandboxOptions struct {
	Profile     string `json:"profile,omitempty"`
	Engine      string `json:"engine,omitempty"`
	Image       string `json:"image,omitempty"`
	Network     string `json:"network,omitempty"`
	MemoryLimit string `json:"memory_limit,omitempty"`
	PIDsLimit   int    `json:"pids_limit,omitempty"`
}

type CandidateEvaluation struct {
	ID                 string                           `json:"id"`
	Status             string                           `json:"status"`
	Score              float64                          `json:"score"`
	ScoreExplanation   *model.ScoreExplanation          `json:"score_explanation,omitempty"`
	MetricsPath        string                           `json:"metrics_path"`
	ResourcePath       string                           `json:"resource_metrics_path,omitempty"`
	SamplesPath        string                           `json:"evaluation_samples_path,omitempty"`
	ExternalTracePath  string                           `json:"external_trace_path,omitempty"`
	RecordFixturesPath string                           `json:"record_fixtures_path,omitempty"`
	VerdictPath        string                           `json:"verdict_path"`
	StdoutPath         string                           `json:"stdout_path"`
	StderrPath         string                           `json:"stderr_path"`
	Sandbox            *SandboxOptions                  `json:"sandbox,omitempty"`
	ExternalPolicy     *model.ExternalPolicyEnforcement `json:"external_policy,omitempty"`
	Errors             []string                         `json:"errors,omitempty"`
	Warnings           []string                         `json:"warnings,omitempty"`
}

type EvaluationReport struct {
	RunID           string                `json:"run_id"`
	LeaderboardPath string                `json:"leaderboard_path"`
	EvaluatorPath   string                `json:"evaluator_path"`
	Adoption        *AdoptionReport       `json:"adoption,omitempty"`
	Results         []CandidateEvaluation `json:"results"`
}

func EvaluateCandidates(opts EvaluationOptions) (*EvaluationReport, error) {
	projectDir := opts.ProjectDir
	if strings.TrimSpace(projectDir) == "" {
		projectDir = "."
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	configPath, err := archive.RunConfigPath(absProject, opts.RunID)
	if err != nil {
		return nil, err
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		return nil, err
	}
	sandbox, err := NormalizeSandboxOptions(opts.Sandbox)
	if err != nil {
		return nil, err
	}
	opts.Sandbox = sandbox

	runDir := cfg.RunDir
	if runDir == "" {
		runDir = filepath.Dir(configPath)
	} else {
		runDir = archive.ProjectPath(absProject, runDir)
	}
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	evaluatorPath := filepath.Join(runDir, "evaluator", "evaluator.sh")
	policy := cfg.External
	policy.Fixtures = archive.ProjectPath(absProject, policy.Fixtures)
	evaluatorEnv := appendEnvDefault(opts.Env, "CRUCIBLE_PROJECT_DIR", absProject)
	evaluatorEnv = appendEnvDefault(evaluatorEnv, "CRUCIBLE_RUN_DIR", runDir)
	evaluatorEnv = externalEvaluationEnv(policy, runDir, evaluatorEnv)

	report := &EvaluationReport{
		RunID:           cfg.ID,
		LeaderboardPath: leaderboardPath,
		EvaluatorPath:   evaluatorPath,
	}

	if opts.Adopt {
		adoption, err := AdoptCandidates(AdoptionOptions{
			ProjectDir: absProject,
			RunID:      cfg.ID,
		})
		if err != nil {
			return nil, err
		}
		report.Adoption = adoption
	}

	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return nil, err
	}

	indexes := evaluationIndexes(board.Results, opts.CandidateID)
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = 1
	}
	if jobs > len(indexes) && len(indexes) > 0 {
		jobs = len(indexes)
	}

	if jobs <= 1 {
		for _, index := range indexes {
			evaluation, updated := evaluateOne(cfg, evaluatorPath, runDir, board.Results[index], evaluatorExecutionOptions{
				Timeout:     opts.Timeout,
				Nice:        opts.Nice,
				CPULimit:    opts.CPULimit,
				Warmups:     opts.Warmups,
				Repetitions: opts.Repetitions,
				Env:         evaluatorEnv,
				ProjectDir:  absProject,
				Sandbox:     opts.Sandbox,
			})
			board.Results[index] = updated
			report.Results = append(report.Results, evaluation)
		}
	} else {
		results := evaluateParallel(cfg, evaluatorPath, runDir, board.Results, indexes, jobs, evaluatorExecutionOptions{
			Timeout:     opts.Timeout,
			Nice:        opts.Nice,
			CPULimit:    opts.CPULimit,
			Warmups:     opts.Warmups,
			Repetitions: opts.Repetitions,
			Env:         evaluatorEnv,
			ProjectDir:  absProject,
			Sandbox:     opts.Sandbox,
		})
		for _, result := range results {
			board.Results[result.index] = result.updated
			report.Results = append(report.Results, result.CandidateEvaluation)
		}
	}

	if opts.CandidateID != "" && len(report.Results) == 0 {
		return nil, fmt.Errorf("candidate %q was not found in run %s", opts.CandidateID, cfg.ID)
	}

	scoring.ScoreResults(board.Results)
	syncEvaluationReportScores(report, board.Results)

	if err := archive.SaveJSON(leaderboardPath, board); err != nil {
		return nil, err
	}
	return report, nil
}

type evaluatorExecutionOptions struct {
	Timeout     time.Duration
	Nice        int
	CPULimit    int
	Warmups     int
	Repetitions int
	Env         []string
	ProjectDir  string
	Sandbox     SandboxOptions
}

type evaluationResult struct {
	CandidateEvaluation
	updated model.CandidateResult
	index   int
}

func evaluationIndexes(results []model.CandidateResult, candidateID string) []int {
	var indexes []int
	for i, result := range results {
		if candidateID != "" && result.Candidate.ID != candidateID {
			continue
		}
		indexes = append(indexes, i)
	}
	return indexes
}

func syncEvaluationReportScores(report *EvaluationReport, results []model.CandidateResult) {
	if report == nil {
		return
	}
	byID := make(map[string]model.CandidateResult, len(results))
	for _, result := range results {
		byID[result.Candidate.ID] = result
	}
	for i := range report.Results {
		if result, ok := byID[report.Results[i].ID]; ok {
			report.Results[i].Score = result.Score
			report.Results[i].ScoreExplanation = result.ScoreExplanation
			report.Results[i].Status = result.Status
		}
	}
}

func evaluateParallel(cfg *model.RunConfig, evaluatorPath, runDir string, results []model.CandidateResult, indexes []int, jobs int, execOpts evaluatorExecutionOptions) []evaluationResult {
	tasks := make(chan int)
	complete := make(chan evaluationResult)

	for i := 0; i < jobs; i++ {
		go func() {
			for index := range tasks {
				evaluation, updated := evaluateOne(cfg, evaluatorPath, runDir, results[index], execOpts)
				complete <- evaluationResult{
					CandidateEvaluation: evaluation,
					updated:             updated,
					index:               index,
				}
			}
		}()
	}

	go func() {
		for _, index := range indexes {
			tasks <- index
		}
		close(tasks)
	}()

	byIndex := make(map[int]evaluationResult, len(indexes))
	for range indexes {
		result := <-complete
		byIndex[result.index] = result
	}

	evaluations := make([]evaluationResult, 0, len(indexes))
	for _, index := range indexes {
		result := byIndex[index]
		evaluations = append(evaluations, result)
	}
	return evaluations
}

func evaluateOne(cfg *model.RunConfig, evaluatorPath, runDir string, result model.CandidateResult, execOpts evaluatorExecutionOptions) (CandidateEvaluation, model.CandidateResult) {
	candidateDir := candidateDirectory(execOpts.ProjectDir, result.Candidate)
	metricsPath := filepath.Join(candidateDir, "metrics.json")
	resourcePath := filepath.Join(candidateDir, "resource-metrics.json")
	samplesPath := filepath.Join(candidateDir, "evaluation-samples.json")
	verdictPath := filepath.Join(candidateDir, "verdict.json")
	stdoutPath := filepath.Join(candidateDir, "evaluation.stdout.log")
	stderrPath := filepath.Join(candidateDir, "evaluation.stderr.log")

	candidateEnv, tracePath, recordFixturesPath := externalCandidateEnv(cfg.External, execOpts.Sandbox, candidateDir, execOpts.Env)
	candidateExecOpts := execOpts
	candidateExecOpts.Env = candidateEnv

	evaluation := CandidateEvaluation{
		ID:           result.Candidate.ID,
		MetricsPath:  filepath.ToSlash(metricsPath),
		ResourcePath: filepath.ToSlash(resourcePath),
		VerdictPath:  filepath.ToSlash(verdictPath),
		StdoutPath:   filepath.ToSlash(stdoutPath),
		StderrPath:   filepath.ToSlash(stderrPath),
	}
	if tracePath != "" {
		evaluation.ExternalTracePath = filepath.ToSlash(tracePath)
	}
	if recordFixturesPath != "" {
		evaluation.RecordFixturesPath = filepath.ToSlash(recordFixturesPath)
	}
	if sandboxEnabled(execOpts.Sandbox) {
		sandbox := execOpts.Sandbox
		evaluation.Sandbox = &sandbox
	}
	policyEnforcement := externalPolicyEnforcement(cfg.External, execOpts.Sandbox)
	evaluation.ExternalPolicy = &policyEnforcement
	evaluation.Warnings = append(evaluation.Warnings, policyEnforcement.Warnings...)
	evaluation.Errors = append(evaluation.Errors, policyEnforcement.Errors...)

	if err := removeEvaluationOutputs(metricsPath, resourcePath, samplesPath, tracePath, recordFixturesPath, verdictPath); err != nil {
		evaluation.Warnings = append(evaluation.Warnings, err.Error())
	}
	policyFailed := len(policyEnforcement.Errors) > 0
	contractErrors := validateCandidateContract(runDir, candidateDir, result.Candidate)
	contractFailed := len(contractErrors) > 0
	if contractFailed {
		evaluation.Errors = append(evaluation.Errors, contractErrors...)
	}
	var evaluatorErrors []string
	var resourceMetrics model.Metrics
	var metrics model.Metrics
	var verdict model.Verdict
	if !policyFailed && !contractFailed {
		samples := runEvaluatorSampleSet(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, tracePath, recordFixturesPath, verdictPath, stdoutPath, stderrPath, samplesPath, candidateExecOpts)
		evaluation.SamplesPath = filepath.ToSlash(samplesPath)
		resourceMetrics = samples.Resource
		metrics = samples.Metrics
		verdict = samples.Verdict
		evaluatorErrors = append(evaluatorErrors, samples.Errors...)
		evaluation.Errors = append(evaluation.Errors, samples.Errors...)
		evaluation.Warnings = append(evaluation.Warnings, samples.Warnings...)
	}
	if err := archive.SaveJSON(resourcePath, resourceMetrics); err != nil {
		evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("save resource metrics: %v", err))
	}

	if policyFailed || contractFailed {
		verdict = model.Verdict{
			CorrectnessPassed:    !contractFailed,
			BenchmarkPassed:      !contractFailed,
			ExternalPolicyPassed: !policyFailed,
		}
		if contractFailed {
			verdict.Errors = append(verdict.Errors, contractErrors...)
		}
	}
	externalTrace := model.ExternalCallTrace{Mode: cfg.External.Mode}
	if tracePath != "" && !policyFailed {
		trace, err := loadExternalTrace(tracePath)
		if err != nil {
			if !os.IsNotExist(err) {
				evaluation.Warnings = append(evaluation.Warnings, err.Error())
			}
		} else {
			externalTrace = trace
			metrics = mergeExternalTraceMetrics(metrics, trace)
		}
	}
	if tracePath != "" {
		externalTrace.TracePath = filepath.ToSlash(tracePath)
	}
	if recordFixturesPath != "" {
		externalTrace.RecordFixturesPath = filepath.ToSlash(recordFixturesPath)
	}
	metrics = mergeResourceMetrics(metrics, resourceMetrics)
	if err := archive.SaveJSON(metricsPath, metrics); err != nil {
		evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("save merged metrics: %v", err))
	}
	if len(evaluatorErrors) > 0 {
		verdict.Errors = appendMissingStrings(verdict.Errors, evaluatorErrors)
		verdict.BenchmarkPassed = false
	}
	if policyFailed {
		verdict.Errors = append(verdict.Errors, policyEnforcement.Errors...)
		verdict.ExternalPolicyPassed = false
	}
	if len(evaluation.Warnings) > 0 {
		verdict.Warnings = append(verdict.Warnings, evaluation.Warnings...)
	}

	result.Metrics = metrics
	result.Verdict = verdict
	result.External = externalTrace
	result.External.Mode = cfg.External.Mode
	result.External.PolicyPassed = verdict.ExternalPolicyPassed
	result.External.PolicyEnforcement = &policyEnforcement
	result.External.PolicyViolations = append(result.External.PolicyViolations, policyEnforcement.Errors...)
	result.Score = scoring.Score(result)
	result.Status = statusForVerdict(verdict)

	evaluation.Status = result.Status
	evaluation.Score = result.Score
	return evaluation, result
}

func validateCandidateContract(runDir, candidateDir string, candidate model.Candidate) []string {
	srcDir := filepath.Join(candidateDir, "src")
	info, err := os.Stat(srcDir)
	if err != nil || !info.IsDir() {
		return []string{"candidate contract failed: src directory is missing"}
	}
	candidateExts, candidateFiles, err := contractSourceExtensions(srcDir)
	if err != nil {
		return []string{"candidate contract failed: " + err.Error()}
	}
	if candidateFiles == 0 {
		return []string{"candidate contract failed: src directory contains no source files"}
	}
	if candidate.Baseline {
		return nil
	}

	baselineSrc := filepath.Join(runDir, "round-0001", "candidate-0000-baseline", "src")
	baselineExts, baselineFiles, err := contractSourceExtensions(baselineSrc)
	if err != nil || baselineFiles == 0 || len(baselineExts) == 0 {
		return nil
	}
	var errors []string
	var missing []string
	for ext := range baselineExts {
		if !candidateExts[ext] {
			missing = append(missing, ext)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		errors = append(errors, fmt.Sprintf("candidate contract failed: src does not contain baseline language extensions %s", strings.Join(missing, ", ")))
	}
	errors = append(errors, semanticContractErrors(runDir, baselineSrc, srcDir, baselineExts, candidateExts)...)
	return errors
}

func contractSourceExtensions(root string) (map[string]bool, int, error) {
	exts := map[string]bool{}
	files := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "target", "dist", "build":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		files++
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if contractSourceExt(ext) {
			exts[ext] = true
		}
		return nil
	})
	return exts, files, err
}

func contractSourceExt(ext string) bool {
	switch ext {
	case ".go", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".py", ".php", ".rb", ".rs", ".java", ".kt", ".kts", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".swift", ".scala", ".sh", ".sql":
		return true
	default:
		return false
	}
}

func removeEvaluationOutputs(paths ...string) error {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale evaluation output %s: %w", path, err)
		}
	}
	return nil
}

func appendMissingStrings(values []string, additions []string) []string {
	for _, addition := range additions {
		found := false
		for _, value := range values {
			if value == addition {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
}

func loadExternalTrace(path string) (model.ExternalCallTrace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.ExternalCallTrace{}, err
	}
	var trace model.ExternalCallTrace
	if err := json.Unmarshal(data, &trace); err != nil {
		return model.ExternalCallTrace{}, fmt.Errorf("external trace missing or invalid: %w", err)
	}
	return trace, nil
}

func mergeExternalTraceMetrics(metrics model.Metrics, trace model.ExternalCallTrace) model.Metrics {
	if trace.RequestCount > 0 {
		metrics.ExternalCallCount = trace.RequestCount
	}
	return metrics
}
