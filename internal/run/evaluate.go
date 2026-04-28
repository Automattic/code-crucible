package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	Env         []string
	Sandbox     SandboxOptions
}

type SandboxOptions struct {
	Engine  string `json:"engine,omitempty"`
	Image   string `json:"image,omitempty"`
	Network string `json:"network,omitempty"`
}

type CandidateEvaluation struct {
	ID               string                           `json:"id"`
	Status           string                           `json:"status"`
	Score            float64                          `json:"score"`
	ScoreExplanation *model.ScoreExplanation          `json:"score_explanation,omitempty"`
	MetricsPath      string                           `json:"metrics_path"`
	ResourcePath     string                           `json:"resource_metrics_path,omitempty"`
	VerdictPath      string                           `json:"verdict_path"`
	StdoutPath       string                           `json:"stdout_path"`
	StderrPath       string                           `json:"stderr_path"`
	Sandbox          *SandboxOptions                  `json:"sandbox,omitempty"`
	ExternalPolicy   *model.ExternalPolicyEnforcement `json:"external_policy,omitempty"`
	Errors           []string                         `json:"errors,omitempty"`
	Warnings         []string                         `json:"warnings,omitempty"`
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
	if !sandboxEnabled(opts.Sandbox) {
		cleanup, err := startLocalMockGateway(context.Background(), evaluatorEnv, filepath.Join(runDir, "external", "mock-gateway.local.log"))
		if err != nil {
			return nil, err
		}
		defer cleanup()
	}

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
				Timeout:    opts.Timeout,
				Nice:       opts.Nice,
				CPULimit:   opts.CPULimit,
				Env:        evaluatorEnv,
				ProjectDir: absProject,
				Sandbox:    opts.Sandbox,
			})
			board.Results[index] = updated
			report.Results = append(report.Results, evaluation)
		}
	} else {
		results := evaluateParallel(cfg, evaluatorPath, runDir, board.Results, indexes, jobs, evaluatorExecutionOptions{
			Timeout:    opts.Timeout,
			Nice:       opts.Nice,
			CPULimit:   opts.CPULimit,
			Env:        evaluatorEnv,
			ProjectDir: absProject,
			Sandbox:    opts.Sandbox,
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
	Timeout    time.Duration
	Nice       int
	CPULimit   int
	Env        []string
	ProjectDir string
	Sandbox    SandboxOptions
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
	verdictPath := filepath.Join(candidateDir, "verdict.json")
	stdoutPath := filepath.Join(candidateDir, "evaluation.stdout.log")
	stderrPath := filepath.Join(candidateDir, "evaluation.stderr.log")

	evaluation := CandidateEvaluation{
		ID:           result.Candidate.ID,
		MetricsPath:  filepath.ToSlash(metricsPath),
		ResourcePath: filepath.ToSlash(resourcePath),
		VerdictPath:  filepath.ToSlash(verdictPath),
		StdoutPath:   filepath.ToSlash(stdoutPath),
		StderrPath:   filepath.ToSlash(stderrPath),
	}
	if sandboxEnabled(execOpts.Sandbox) {
		sandbox := execOpts.Sandbox
		evaluation.Sandbox = &sandbox
	}
	policyEnforcement := externalPolicyEnforcement(cfg.External, execOpts.Sandbox)
	evaluation.ExternalPolicy = &policyEnforcement
	evaluation.Warnings = append(evaluation.Warnings, policyEnforcement.Warnings...)
	evaluation.Errors = append(evaluation.Errors, policyEnforcement.Errors...)

	if err := removeEvaluationOutputs(metricsPath, resourcePath, verdictPath); err != nil {
		evaluation.Warnings = append(evaluation.Warnings, err.Error())
	}
	policyFailed := len(policyEnforcement.Errors) > 0
	var evaluatorErrors []string
	var resourceMetrics model.Metrics
	if !policyFailed {
		var runErr error
		resourceMetrics, runErr = runEvaluatorScript(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, stdoutPath, stderrPath, execOpts)
		if runErr != nil {
			evaluatorErrors = append(evaluatorErrors, runErr.Error())
			evaluation.Errors = append(evaluation.Errors, evaluatorErrors...)
		}
	}
	if err := archive.SaveJSON(resourcePath, resourceMetrics); err != nil {
		evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("save resource metrics: %v", err))
	}

	var metrics model.Metrics
	var verdict model.Verdict
	if policyFailed {
		verdict = model.Verdict{
			CorrectnessPassed:    true,
			BenchmarkPassed:      true,
			ExternalPolicyPassed: false,
		}
	} else {
		var err error
		metrics, err = loadMetrics(metricsPath)
		if err != nil {
			evaluation.Warnings = append(evaluation.Warnings, err.Error())
		}

		verdict, err = loadVerdict(verdictPath)
		if err != nil {
			verdict = model.Verdict{
				CorrectnessPassed:    false,
				BenchmarkPassed:      false,
				ExternalPolicyPassed: false,
				Errors: []string{
					err.Error(),
				},
			}
		}
	}
	metrics = mergeResourceMetrics(metrics, resourceMetrics)
	if err := archive.SaveJSON(metricsPath, metrics); err != nil {
		evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("save merged metrics: %v", err))
	}
	if len(evaluatorErrors) > 0 {
		verdict.Errors = append(verdict.Errors, evaluatorErrors...)
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

func removeEvaluationOutputs(paths ...string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale evaluation output %s: %w", path, err)
		}
	}
	return nil
}
