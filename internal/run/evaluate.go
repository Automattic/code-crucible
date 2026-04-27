package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaarai/code-crucible/internal/archive"
	"github.com/gaarai/code-crucible/internal/model"
	"github.com/gaarai/code-crucible/internal/scoring"
)

type EvaluationOptions struct {
	ProjectDir  string
	RunID       string
	CandidateID string
	Timeout     time.Duration
	Adopt       bool
}

type CandidateEvaluation struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	Score       float64  `json:"score"`
	MetricsPath string   `json:"metrics_path"`
	VerdictPath string   `json:"verdict_path"`
	StdoutPath  string   `json:"stdout_path"`
	StderrPath  string   `json:"stderr_path"`
	Errors      []string `json:"errors,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
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

	runDir := cfg.RunDir
	if runDir == "" {
		runDir = filepath.Dir(configPath)
	}
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	evaluatorPath := filepath.Join(runDir, "evaluator", "evaluator.sh")

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

	for i := range board.Results {
		result := &board.Results[i]
		if opts.CandidateID != "" && result.Candidate.ID != opts.CandidateID {
			continue
		}

		evaluation := evaluateOne(cfg, evaluatorPath, runDir, result, opts.Timeout)
		report.Results = append(report.Results, evaluation)
	}

	if opts.CandidateID != "" && len(report.Results) == 0 {
		return nil, fmt.Errorf("candidate %q was not found in run %s", opts.CandidateID, cfg.ID)
	}

	if err := archive.SaveJSON(leaderboardPath, board); err != nil {
		return nil, err
	}
	return report, nil
}

func evaluateOne(cfg *model.RunConfig, evaluatorPath, runDir string, result *model.CandidateResult, timeout time.Duration) CandidateEvaluation {
	candidateDir := candidateDirectory(result.Candidate)
	metricsPath := filepath.Join(candidateDir, "metrics.json")
	verdictPath := filepath.Join(candidateDir, "verdict.json")
	stdoutPath := filepath.Join(candidateDir, "evaluation.stdout.log")
	stderrPath := filepath.Join(candidateDir, "evaluation.stderr.log")

	evaluation := CandidateEvaluation{
		ID:          result.Candidate.ID,
		MetricsPath: filepath.ToSlash(metricsPath),
		VerdictPath: filepath.ToSlash(verdictPath),
		StdoutPath:  filepath.ToSlash(stdoutPath),
		StderrPath:  filepath.ToSlash(stderrPath),
	}

	if err := runEvaluatorScript(evaluatorPath, candidateDir, runDir, metricsPath, verdictPath, stdoutPath, stderrPath, timeout); err != nil {
		evaluation.Errors = append(evaluation.Errors, err.Error())
	}

	metrics, err := loadMetrics(metricsPath)
	if err != nil {
		evaluation.Warnings = append(evaluation.Warnings, err.Error())
	}

	verdict, err := loadVerdict(verdictPath)
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
	if len(evaluation.Errors) > 0 {
		verdict.Errors = append(verdict.Errors, evaluation.Errors...)
	}
	if len(evaluation.Warnings) > 0 {
		verdict.Warnings = append(verdict.Warnings, evaluation.Warnings...)
	}

	result.Metrics = metrics
	result.Verdict = verdict
	result.External.Mode = cfg.External.Mode
	result.External.PolicyPassed = verdict.ExternalPolicyPassed
	result.Score = scoring.Score(*result)
	result.Status = statusForVerdict(verdict)

	evaluation.Status = result.Status
	evaluation.Score = result.Score
	return evaluation
}

func runEvaluatorScript(evaluatorPath, candidateDir, runDir, metricsPath, verdictPath, stdoutPath, stderrPath string, timeout time.Duration) error {
	if info, err := os.Stat(evaluatorPath); err != nil || info.IsDir() {
		return fmt.Errorf("evaluator script is missing: %s", evaluatorPath)
	}
	if info, err := os.Stat(candidateDir); err != nil || !info.IsDir() {
		return fmt.Errorf("candidate directory is missing: %s", candidateDir)
	}

	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o755); err != nil {
		return err
	}
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return err
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return err
	}
	defer stderrFile.Close()

	ctx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", evaluatorPath, candidateDir, runDir, metricsPath, verdictPath)
	cmd.Dir = runDir
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("evaluator timed out after %s", timeout)
	}
	if err != nil {
		return fmt.Errorf("evaluator failed: %w", err)
	}
	return nil
}

func loadMetrics(path string) (model.Metrics, error) {
	var metrics model.Metrics
	if err := loadJSON(path, &metrics); err != nil {
		return metrics, fmt.Errorf("metrics missing or invalid: %w", err)
	}
	return metrics, nil
}

func loadVerdict(path string) (model.Verdict, error) {
	var verdict model.Verdict
	if err := loadJSON(path, &verdict); err != nil {
		return verdict, fmt.Errorf("verdict missing or invalid: %w", err)
	}
	return verdict, nil
}

func loadJSON(path string, out any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("contains trailing data")
	}
	return nil
}

func candidateDirectory(candidate model.Candidate) string {
	sourcePath := filepath.Clean(candidate.SourcePath)
	if filepath.Base(sourcePath) == "src" {
		return filepath.Dir(sourcePath)
	}
	return sourcePath
}

func statusForVerdict(verdict model.Verdict) string {
	if verdict.CorrectnessPassed && verdict.BenchmarkPassed && verdict.ExternalPolicyPassed {
		return "passed"
	}
	return "failed"
}
