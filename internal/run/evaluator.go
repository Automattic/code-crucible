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

const DefaultEvaluatorValidationTimeout = 60 * time.Second

type EvaluatorReadyOptions struct {
	ProjectDir string
	RunID      string
}

type EvaluatorValidationOptions struct {
	Context    context.Context
	ProjectDir string
	RunID      string
	Timeout    time.Duration
}

type EvaluatorValidationReport struct {
	RunID              string                           `json:"run_id"`
	CandidateID        string                           `json:"candidate_id"`
	Status             string                           `json:"status"`
	ValidationDir      string                           `json:"validation_dir"`
	ReportPath         string                           `json:"report_path"`
	MetricsPath        string                           `json:"metrics_path"`
	ResourcePath       string                           `json:"resource_metrics_path"`
	SamplesPath        string                           `json:"evaluation_samples_path"`
	ExternalTracePath  string                           `json:"external_trace_path,omitempty"`
	RecordFixturesPath string                           `json:"record_fixtures_path,omitempty"`
	VerdictPath        string                           `json:"verdict_path"`
	StdoutPath         string                           `json:"stdout_path"`
	StderrPath         string                           `json:"stderr_path"`
	ExternalPolicy     *model.ExternalPolicyEnforcement `json:"external_policy,omitempty"`
	Metrics            model.Metrics                    `json:"metrics,omitempty"`
	Verdict            model.Verdict                    `json:"verdict,omitempty"`
	Errors             []string                         `json:"errors,omitempty"`
	Warnings           []string                         `json:"warnings,omitempty"`
}

type EvaluatorReadyReport struct {
	RunID             string   `json:"run_id"`
	ConfigPath        string   `json:"config_path"`
	LeaderboardPath   string   `json:"leaderboard_path"`
	UpdatedCandidates []string `json:"updated_candidates"`
}

func ValidateGeneratedEvaluator(opts EvaluatorValidationOptions) (*EvaluatorValidationReport, error) {
	projectDir := opts.ProjectDir
	if projectDir == "" {
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
	runDir := cfg.RunDir
	if runDir == "" {
		runDir = filepath.Dir(configPath)
	} else {
		runDir = archive.ProjectPath(absProject, runDir)
	}
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return nil, err
	}
	baseline, ok := baselineCandidateResult(board.Results)
	if !ok {
		return nil, fmt.Errorf("baseline candidate was not found in %s", leaderboardPath)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultEvaluatorValidationTimeout
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}

	evaluatorPath := filepath.Join(runDir, "evaluator", "evaluator.sh")
	stamp := time.Now().UTC().Format("20060102-150405")
	validationDir := filepath.Join(runDir, "tmp", "evaluator-validation", stamp)
	if err := os.MkdirAll(validationDir, 0o755); err != nil {
		return nil, err
	}
	reportPath := filepath.Join(runDir, "evaluator", "validation.json")
	metricsPath := filepath.Join(validationDir, "metrics.json")
	resourcePath := filepath.Join(validationDir, "resource-metrics.json")
	samplesPath := filepath.Join(validationDir, "evaluation-samples.json")
	verdictPath := filepath.Join(validationDir, "verdict.json")
	stdoutPath := filepath.Join(validationDir, "evaluation.stdout.log")
	stderrPath := filepath.Join(validationDir, "evaluation.stderr.log")
	report := &EvaluatorValidationReport{
		RunID:         cfg.ID,
		CandidateID:   baseline.Candidate.ID,
		Status:        model.CandidateStatusFailed,
		ValidationDir: filepath.ToSlash(validationDir),
		ReportPath:    filepath.ToSlash(reportPath),
		MetricsPath:   filepath.ToSlash(metricsPath),
		ResourcePath:  filepath.ToSlash(resourcePath),
		SamplesPath:   filepath.ToSlash(samplesPath),
		VerdictPath:   filepath.ToSlash(verdictPath),
		StdoutPath:    filepath.ToSlash(stdoutPath),
		StderrPath:    filepath.ToSlash(stderrPath),
	}

	policy := cfg.External
	policy.Fixtures = archive.ProjectPath(absProject, policy.Fixtures)
	evaluatorEnv := appendEnvDefault(nil, "CRUCIBLE_PROJECT_DIR", absProject)
	evaluatorEnv = appendEnvDefault(evaluatorEnv, "CRUCIBLE_RUN_DIR", runDir)
	evaluatorEnv = externalEvaluationEnv(policy, runDir, evaluatorEnv)
	candidateEnv, tracePath, recordFixturesPath := externalCandidateEnv(cfg.External, SandboxOptions{}, validationDir, evaluatorEnv)
	report.ExternalTracePath = filepath.ToSlash(tracePath)
	report.RecordFixturesPath = filepath.ToSlash(recordFixturesPath)

	policyEnforcement := externalPolicyEnforcement(cfg.External, SandboxOptions{})
	report.ExternalPolicy = &policyEnforcement
	report.Warnings = append(report.Warnings, policyEnforcement.Warnings...)
	report.Errors = append(report.Errors, policyEnforcement.Errors...)

	if len(report.Errors) == 0 {
		candidateDir := candidateDirectory(absProject, baseline.Candidate)
		samples := runEvaluatorSampleSet(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, tracePath, recordFixturesPath, verdictPath, stdoutPath, stderrPath, samplesPath, evaluatorExecutionOptions{
			Context:    ctx,
			Timeout:    timeout,
			Nice:       10,
			Env:        candidateEnv,
			ProjectDir: absProject,
		})
		report.Metrics = samples.Metrics
		report.Verdict = samples.Verdict
		report.Errors = append(report.Errors, samples.Errors...)
		report.Warnings = append(report.Warnings, samples.Warnings...)
		if err := archive.SaveJSON(resourcePath, samples.Resource); err != nil {
			report.Warnings = append(report.Warnings, "save resource metrics: "+err.Error())
		}
		if metrics, err := loadMetrics(metricsPath); err == nil {
			report.Metrics = mergeResourceMetrics(metrics, samples.Resource)
		} else {
			report.Errors = append(report.Errors, "load validation metrics: "+err.Error())
		}
		if verdict, err := loadVerdict(verdictPath); err == nil {
			report.Verdict = verdict
		} else {
			report.Errors = append(report.Errors, "load validation verdict: "+err.Error())
		}
		if statusForVerdict(report.Verdict) != model.CandidateStatusPassed {
			report.Errors = append(report.Errors, "baseline candidate did not pass the generated evaluator")
		}
	}

	report.Errors = uniqueStrings(report.Errors)
	report.Warnings = uniqueStrings(report.Warnings)
	if len(report.Errors) == 0 {
		report.Status = model.CandidateStatusPassed
	}
	if err := archive.SaveJSON(reportPath, report); err != nil {
		return report, err
	}
	if report.Status != model.CandidateStatusPassed {
		return report, fmt.Errorf("generated evaluator validation failed for %s; see %s", report.CandidateID, reportPath)
	}
	if err := recordBaselineValidationResult(absProject, cfg, leaderboardPath, baseline, report); err != nil {
		return report, err
	}
	return report, nil
}

func recordBaselineValidationResult(projectDir string, cfg *model.RunConfig, leaderboardPath string, baseline model.CandidateResult, report *EvaluatorValidationReport) error {
	candidateDir := candidateDirectory(projectDir, baseline.Candidate)
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		return err
	}

	metricsPath := filepath.Join(candidateDir, "metrics.json")
	resourcePath := filepath.Join(candidateDir, "resource-metrics.json")
	samplesPath := filepath.Join(candidateDir, "evaluation-samples.json")
	verdictPath := filepath.Join(candidateDir, "verdict.json")
	stdoutPath := filepath.Join(candidateDir, "evaluation.stdout.log")
	stderrPath := filepath.Join(candidateDir, "evaluation.stderr.log")
	tracePath := filepath.Join(candidateDir, "external-trace.json")
	recordFixturesPath := filepath.Join(candidateDir, "recorded-http-fixtures.json")

	if err := removeEvaluationOutputs(metricsPath, resourcePath, samplesPath, verdictPath, tracePath, recordFixturesPath); err != nil {
		return err
	}
	if err := copyExistingArtifact(filepath.FromSlash(report.ResourcePath), resourcePath); err != nil {
		return err
	}
	if err := copyExistingArtifact(filepath.FromSlash(report.SamplesPath), samplesPath); err != nil {
		return err
	}
	if err := copyExistingArtifact(filepath.FromSlash(report.StdoutPath), stdoutPath); err != nil {
		return err
	}
	if err := copyExistingArtifact(filepath.FromSlash(report.StderrPath), stderrPath); err != nil {
		return err
	}

	metrics := report.Metrics
	verdict := report.Verdict
	externalTrace := model.ExternalCallTrace{
		Mode:              cfg.External.Mode,
		PolicyPassed:      verdict.ExternalPolicyPassed,
		PolicyEnforcement: report.ExternalPolicy,
	}
	if report.ExternalPolicy != nil {
		externalTrace.PolicyViolations = append(externalTrace.PolicyViolations, report.ExternalPolicy.Errors...)
	}

	if report.ExternalTracePath != "" {
		if err := copyExistingArtifact(filepath.FromSlash(report.ExternalTracePath), tracePath); err != nil {
			return err
		}
		trace, err := loadExternalTrace(tracePath)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		} else {
			externalTrace = trace
			externalTrace.PolicyPassed = verdict.ExternalPolicyPassed
			externalTrace.PolicyEnforcement = report.ExternalPolicy
			if report.ExternalPolicy != nil {
				externalTrace.PolicyViolations = appendMissingStrings(externalTrace.PolicyViolations, report.ExternalPolicy.Errors)
			}
			metrics = mergeExternalTraceMetrics(metrics, trace)
		}
		externalTrace.TracePath = filepath.ToSlash(tracePath)
	}
	if report.RecordFixturesPath != "" {
		if err := copyExistingArtifact(filepath.FromSlash(report.RecordFixturesPath), recordFixturesPath); err != nil {
			return err
		}
		externalTrace.RecordFixturesPath = filepath.ToSlash(recordFixturesPath)
	}

	verdict.Warnings = appendMissingStrings(verdict.Warnings, report.Warnings)
	verdict.Errors = appendMissingStrings(verdict.Errors, report.Errors)
	if err := archive.SaveJSON(metricsPath, metrics); err != nil {
		return err
	}
	if err := archive.SaveJSON(verdictPath, verdict); err != nil {
		return err
	}

	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return err
	}
	found := false
	for i := range board.Results {
		if board.Results[i].Candidate.ID != baseline.Candidate.ID {
			continue
		}
		board.Results[i].Metrics = metrics
		board.Results[i].Verdict = verdict
		board.Results[i].External = externalTrace
		board.Results[i].External.Mode = cfg.External.Mode
		board.Results[i].Status = statusForVerdict(verdict)
		found = true
		break
	}
	if !found {
		return fmt.Errorf("baseline candidate %s was not found in %s", baseline.Candidate.ID, leaderboardPath)
	}
	scoring.ScoreResults(board.Results)
	return archive.SaveJSON(leaderboardPath, board)
}

func copyExistingArtifact(src, dst string) error {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return archive.CopyPath(src, dst)
}

func MarkEvaluatorReady(opts EvaluatorReadyOptions) (*EvaluatorReadyReport, error) {
	projectDir := opts.ProjectDir
	if projectDir == "" {
		projectDir = "."
	}
	configPath, err := archive.RunConfigPath(projectDir, opts.RunID)
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
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return nil, err
	}

	report := &EvaluatorReadyReport{
		RunID:           cfg.ID,
		ConfigPath:      filepath.ToSlash(configPath),
		LeaderboardPath: filepath.ToSlash(leaderboardPath),
	}
	if !cfg.EvaluatorGenerated {
		cfg.EvaluatorGenerated = true
		if err := archive.SaveJSON(configPath, cfg); err != nil {
			return nil, err
		}
	}
	for i := range board.Results {
		if board.Results[i].Status != model.CandidateStatusNeedsEvaluator {
			continue
		}
		board.Results[i].Status = "pending"
		board.Results[i].Verdict.Notes = []string{
			"Evaluator is configured; candidate has not been evaluated yet.",
		}
		report.UpdatedCandidates = append(report.UpdatedCandidates, board.Results[i].Candidate.ID)
	}
	if len(report.UpdatedCandidates) > 0 {
		if err := archive.SaveJSON(leaderboardPath, board); err != nil {
			return nil, err
		}
	}
	return report, nil
}

func baselineCandidateResult(results []model.CandidateResult) (model.CandidateResult, bool) {
	for _, result := range results {
		if result.Candidate.Baseline {
			return result, true
		}
	}
	for _, result := range results {
		if result.Candidate.ID == "candidate-0000-baseline" {
			return result, true
		}
	}
	return model.CandidateResult{}, false
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
