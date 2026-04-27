package run

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	Jobs        int
	Nice        int
	CPULimit    int
	Env         []string
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
				Timeout:  opts.Timeout,
				Nice:     opts.Nice,
				CPULimit: opts.CPULimit,
				Env:      opts.Env,
			})
			board.Results[index] = updated
			report.Results = append(report.Results, evaluation)
		}
	} else {
		results := evaluateParallel(cfg, evaluatorPath, runDir, board.Results, indexes, jobs, evaluatorExecutionOptions{
			Timeout:  opts.Timeout,
			Nice:     opts.Nice,
			CPULimit: opts.CPULimit,
			Env:      opts.Env,
		})
		for _, result := range results {
			board.Results[result.index] = result.updated
			report.Results = append(report.Results, result.CandidateEvaluation)
		}
	}

	if opts.CandidateID != "" && len(report.Results) == 0 {
		return nil, fmt.Errorf("candidate %q was not found in run %s", opts.CandidateID, cfg.ID)
	}

	if err := archive.SaveJSON(leaderboardPath, board); err != nil {
		return nil, err
	}
	return report, nil
}

type evaluatorExecutionOptions struct {
	Timeout  time.Duration
	Nice     int
	CPULimit int
	Env      []string
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

	resourceMetrics, err := runEvaluatorScript(evaluatorPath, candidateDir, runDir, metricsPath, verdictPath, stdoutPath, stderrPath, execOpts)
	if err != nil {
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
	metrics = mergeResourceMetrics(metrics, resourceMetrics)
	if err := archive.SaveJSON(metricsPath, metrics); err != nil {
		evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("save merged metrics: %v", err))
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
	result.Score = scoring.Score(result)
	result.Status = statusForVerdict(verdict)

	evaluation.Status = result.Status
	evaluation.Score = result.Score
	return evaluation, result
}

func runEvaluatorScript(evaluatorPath, candidateDir, runDir, metricsPath, verdictPath, stdoutPath, stderrPath string, opts evaluatorExecutionOptions) (model.Metrics, error) {
	if info, err := os.Stat(evaluatorPath); err != nil || info.IsDir() {
		return model.Metrics{}, fmt.Errorf("evaluator script is missing: %s", evaluatorPath)
	}
	if info, err := os.Stat(candidateDir); err != nil || !info.IsDir() {
		return model.Metrics{}, fmt.Errorf("candidate directory is missing: %s", candidateDir)
	}

	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o755); err != nil {
		return model.Metrics{}, err
	}
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return model.Metrics{}, err
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return model.Metrics{}, err
	}
	defer stderrFile.Close()

	ctx := context.Background()
	cancel := func() {}
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}
	defer cancel()

	name := "bash"
	args := []string{evaluatorPath, candidateDir, runDir, metricsPath, verdictPath}
	if opts.CPULimit > 0 {
		name, args = wrapTasksetCommand(name, args, opts.CPULimit)
	}
	if opts.Nice > 0 {
		args = append([]string{"-n", strconv.Itoa(opts.Nice), name}, args...)
		name = "nice"
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = runDir
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	start := time.Now()
	err = cmd.Run()
	resourceMetrics := processMetrics(cmd.ProcessState, time.Since(start))
	if ctx.Err() == context.DeadlineExceeded {
		return resourceMetrics, fmt.Errorf("evaluator timed out after %s", opts.Timeout)
	}
	if err != nil {
		return resourceMetrics, fmt.Errorf("evaluator failed: %w", err)
	}
	return resourceMetrics, nil
}

func wrapTasksetCommand(name string, args []string, cpuLimit int) (string, []string) {
	mask := "0"
	if cpuLimit > 1 {
		mask = fmt.Sprintf("0-%d", cpuLimit-1)
	}
	wrapped := []string{"-c", mask, name}
	wrapped = append(wrapped, args...)
	return "taskset", wrapped
}

func processMetrics(state *os.ProcessState, wall time.Duration) model.Metrics {
	metrics := model.Metrics{
		WallTimeMS: float64(wall.Microseconds()) / 1000,
	}
	if state == nil {
		return metrics
	}

	metrics.CPUUserSeconds = state.UserTime().Seconds()
	metrics.CPUSystemSeconds = state.SystemTime().Seconds()
	totalCPUSeconds := metrics.CPUUserSeconds + metrics.CPUSystemSeconds
	if wall > 0 {
		metrics.CPUPercent = totalCPUSeconds / wall.Seconds() * 100
	}

	if usage, ok := state.SysUsage().(*syscall.Rusage); ok && usage != nil {
		// Linux reports Maxrss in kilobytes.
		metrics.MaxRSSBytes = usage.Maxrss * 1024
		metrics.VoluntaryContextSwitches = usage.Nvcsw
		metrics.InvoluntaryContextSwitches = usage.Nivcsw
		metrics.IOBytesRead = usage.Inblock * 512
		metrics.IOBytesWritten = usage.Oublock * 512
	}

	return metrics
}

func mergeResourceMetrics(metrics, resource model.Metrics) model.Metrics {
	if resource.WallTimeMS > 0 {
		metrics.WallTimeMS = resource.WallTimeMS
	}
	if resource.CPUUserSeconds > 0 {
		metrics.CPUUserSeconds = resource.CPUUserSeconds
	}
	if resource.CPUSystemSeconds > 0 {
		metrics.CPUSystemSeconds = resource.CPUSystemSeconds
	}
	if resource.CPUPercent > 0 {
		metrics.CPUPercent = resource.CPUPercent
	}
	if resource.MaxRSSBytes > 0 {
		metrics.MaxRSSBytes = resource.MaxRSSBytes
	}
	if resource.VoluntaryContextSwitches > 0 {
		metrics.VoluntaryContextSwitches = resource.VoluntaryContextSwitches
	}
	if resource.InvoluntaryContextSwitches > 0 {
		metrics.InvoluntaryContextSwitches = resource.InvoluntaryContextSwitches
	}
	if resource.IOBytesRead > 0 {
		metrics.IOBytesRead = resource.IOBytesRead
	}
	if resource.IOBytesWritten > 0 {
		metrics.IOBytesWritten = resource.IOBytesWritten
	}
	return metrics
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
