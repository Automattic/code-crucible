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

	"github.com/Automattic/code-crucible/internal/archive"
	externalfixtures "github.com/Automattic/code-crucible/internal/external"
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
	}
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	evaluatorPath := filepath.Join(runDir, "evaluator", "evaluator.sh")
	evaluatorEnv := externalEvaluationEnv(cfg.External, runDir, opts.Env)

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
	candidateDir := candidateDirectory(result.Candidate)
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

func NormalizeSandboxOptions(opts SandboxOptions) (SandboxOptions, error) {
	opts.Engine = strings.TrimSpace(opts.Engine)
	opts.Image = strings.TrimSpace(opts.Image)
	opts.Network = strings.TrimSpace(opts.Network)

	if opts.Engine == "" {
		opts.Engine = "local"
	}

	switch opts.Engine {
	case "local":
		if opts.Image != "" {
			return SandboxOptions{}, fmt.Errorf("--sandbox-image requires --sandbox-engine docker or podman")
		}
		return SandboxOptions{Engine: "local"}, nil
	case "docker", "podman":
		if opts.Image == "" {
			return SandboxOptions{}, fmt.Errorf("--sandbox-image is required when --sandbox-engine is %s", opts.Engine)
		}
		if opts.Network == "" {
			opts.Network = "none"
		}
		return opts, nil
	default:
		return SandboxOptions{}, fmt.Errorf("--sandbox-engine must be local, docker, or podman")
	}
}

func externalPolicyEnforcement(policy model.ExternalPolicy, sandbox SandboxOptions) model.ExternalPolicyEnforcement {
	enforcement := model.ExternalPolicyEnforcement{
		Mode:   policy.Mode,
		Status: "advisory",
	}

	if sandboxEnabled(sandbox) {
		enforcement.Mechanism = sandbox.Engine + "-network-" + sandbox.Network
	} else {
		enforcement.Mechanism = "evaluator-contract"
	}

	switch policy.Mode {
	case model.ExternalModeDeny:
		if sandboxEnabled(sandbox) {
			if sandbox.Network == "none" {
				enforcement.Status = "enforced"
				return enforcement
			}
			enforcement.Status = "failed"
			enforcement.Errors = append(enforcement.Errors, "external policy deny requires --sandbox-network none when using a container sandbox")
			return enforcement
		}
		enforcement.Warnings = append(enforcement.Warnings, "external policy deny is advisory in local mode; use --sandbox-engine docker or podman with --sandbox-network none to enforce network isolation")
	case model.ExternalModeMock, model.ExternalModeReplay:
		if sandboxEnabled(sandbox) && sandbox.Network == "none" {
			enforcement.Status = "partial"
			enforcement.Warnings = append(enforcement.Warnings, "live network is blocked and proxy-based fixture replay is available inside the sandbox; clients that ignore proxy or trust environment variables still require evaluator configuration")
			return enforcement
		}
		enforcement.Warnings = append(enforcement.Warnings, "fixture gateway and proxy environment are available when fixtures are archived, but network isolation requires a container sandbox with --sandbox-network none")
	case model.ExternalModeAllowlist:
		enforcement.Warnings = append(enforcement.Warnings, "framework-level allowlist enforcement is not implemented yet")
	case model.ExternalModeRecord:
		enforcement.Warnings = append(enforcement.Warnings, "framework-level external recording is not implemented yet")
	default:
		enforcement.Warnings = append(enforcement.Warnings, "external policy mode is not recognized by the enforcement layer")
	}

	return enforcement
}

func externalEvaluationEnv(policy model.ExternalPolicy, runDir string, env []string) []string {
	out := append([]string(nil), env...)
	out = appendEnvDefault(out, "CRUCIBLE_EXTERNAL_MODE", string(policy.Mode))

	if !fixtureBackedMode(policy.Mode) {
		return out
	}

	fixturesPath := strings.TrimSpace(policy.Fixtures)
	if fixturesPath == "" {
		candidatePath := filepath.Join(runDir, "external", externalfixtures.HTTPFixturesName)
		if fileExists(candidatePath) {
			fixturesPath = candidatePath
		}
	}
	if fixturesPath != "" {
		out = appendEnvDefault(out, "CRUCIBLE_HTTP_FIXTURES", filepath.ToSlash(fixturesPath))
	}
	if !gatewayBackedMode(policy.Mode) {
		return out
	}

	gatewaySource := filepath.Join(runDir, "external", externalfixtures.MockGatewayName)
	if fileExists(gatewaySource) {
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_GATEWAY_SOURCE", filepath.ToSlash(gatewaySource))
	}
	caCertPath := filepath.Join(runDir, "external", externalfixtures.MockCACertName)
	caKeyPath := filepath.Join(runDir, "external", externalfixtures.MockCAKeyName)
	if fileExists(caCertPath) && fileExists(caKeyPath) {
		caCertPath = filepath.ToSlash(caCertPath)
		caKeyPath = filepath.ToSlash(caKeyPath)
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_CA_CERT", caCertPath)
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_CA_KEY", caKeyPath)
		out = appendEnvDefault(out, "SSL_CERT_FILE", caCertPath)
		out = appendEnvDefault(out, "REQUESTS_CA_BUNDLE", caCertPath)
		out = appendEnvDefault(out, "CURL_CA_BUNDLE", caCertPath)
		out = appendEnvDefault(out, "NODE_EXTRA_CA_CERTS", caCertPath)
		out = appendEnvDefault(out, "GIT_SSL_CAINFO", caCertPath)
	}

	gatewayAddr := envValue(out, "CRUCIBLE_MOCK_GATEWAY_ADDR")
	if gatewayAddr == "" {
		gatewayAddr = "127.0.0.1:18080"
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_GATEWAY_ADDR", gatewayAddr)
	}
	gatewayURL := envValue(out, "CRUCIBLE_MOCK_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://" + gatewayAddr
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_GATEWAY_URL", gatewayURL)
	}
	out = appendEnvDefault(out, "HTTP_PROXY", gatewayURL)
	out = appendEnvDefault(out, "http_proxy", gatewayURL)
	out = appendEnvDefault(out, "HTTPS_PROXY", gatewayURL)
	out = appendEnvDefault(out, "https_proxy", gatewayURL)
	out = appendEnvDefault(out, "NO_PROXY", "localhost,127.0.0.1,::1")
	out = appendEnvDefault(out, "no_proxy", "localhost,127.0.0.1,::1")
	return out
}

func fixtureBackedMode(mode model.ExternalMode) bool {
	return mode == model.ExternalModeMock ||
		mode == model.ExternalModeReplay ||
		mode == model.ExternalModeRecord
}

func gatewayBackedMode(mode model.ExternalMode) bool {
	return mode == model.ExternalModeMock ||
		mode == model.ExternalModeReplay
}

func appendEnvDefault(env []string, key, value string) []string {
	if envValue(env, key) != "" {
		return env
	}
	return append(env, key+"="+value)
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func runEvaluatorScript(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, stdoutPath, stderrPath string, opts evaluatorExecutionOptions) (model.Metrics, error) {
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

	if sandboxEnabled(opts.Sandbox) {
		if err := os.MkdirAll(sandboxHomeDir(runDir), 0o755); err != nil {
			return model.Metrics{}, err
		}
		if err := ensureSandboxResourceWrapper(runDir); err != nil {
			return model.Metrics{}, err
		}
	}

	name, args, err := buildEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, opts)
	if err != nil {
		return model.Metrics{}, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = runDir
	if !sandboxEnabled(opts.Sandbox) && len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	start := time.Now()
	err = cmd.Run()
	resourceMetrics := processMetrics(cmd.ProcessState, time.Since(start))
	if sandboxEnabled(opts.Sandbox) {
		containerMetrics, loadErr := loadResourceMetrics(resourcePath)
		if loadErr == nil {
			resourceMetrics = containerMetrics
		} else if err == nil {
			err = loadErr
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return resourceMetrics, fmt.Errorf("evaluator timed out after %s", opts.Timeout)
	}
	if err != nil {
		return resourceMetrics, fmt.Errorf("evaluator failed: %w", err)
	}
	return resourceMetrics, nil
}

func buildEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath string, opts evaluatorExecutionOptions) (string, []string, error) {
	sandbox, err := NormalizeSandboxOptions(opts.Sandbox)
	if err != nil {
		return "", nil, err
	}
	opts.Sandbox = sandbox

	if sandboxEnabled(opts.Sandbox) {
		return buildContainerEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, opts)
	}

	name := "bash"
	args := []string{evaluatorPath, candidateDir, runDir, metricsPath, verdictPath}
	if opts.CPULimit > 0 {
		name, args = wrapTasksetCommand(name, args, opts.CPULimit)
	}
	if opts.Nice > 0 {
		args = append([]string{"-n", strconv.Itoa(opts.Nice), name}, args...)
		name = "nice"
	}
	return name, args, nil
}

func buildContainerEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath string, opts evaluatorExecutionOptions) (string, []string, error) {
	args := []string{
		"run",
		"--rm",
		"--network", opts.Sandbox.Network,
		"--volume", sandboxMount(runDir, "rw"),
	}

	if opts.ProjectDir != "" && filepath.Clean(opts.ProjectDir) != filepath.Clean(runDir) {
		args = append(args, "--volume", sandboxMount(opts.ProjectDir, "ro"))
	}

	if opts.CPULimit > 0 {
		args = append(args, "--cpus", strconv.Itoa(opts.CPULimit))
	}

	if opts.Sandbox.Engine == "podman" {
		args = append(args, "--userns", "keep-id")
	} else if uid := os.Getuid(); uid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, os.Getgid()))
	}

	args = append(args,
		"--workdir", runDir,
		"--env", "HOME="+sandboxHomeDir(runDir),
	)
	for _, env := range opts.Env {
		args = append(args, "--env", env)
	}

	args = append(args,
		opts.Sandbox.Image,
		"bash",
		sandboxResourceWrapperPath(runDir),
		evaluatorPath,
		candidateDir,
		runDir,
		metricsPath,
		resourcePath,
		verdictPath,
	)
	return opts.Sandbox.Engine, args, nil
}

func ensureSandboxResourceWrapper(runDir string) error {
	path := sandboxResourceWrapperPath(runDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sandboxResourceWrapperScript()), 0o755)
}

func sandboxResourceWrapperPath(runDir string) string {
	return filepath.Join(runDir, "evaluator", "resource-wrapper.sh")
}

func sandboxResourceWrapperScript() string {
	return `#!/usr/bin/env bash
set -u

evaluator_path="${1:?evaluator path required}"
candidate_dir="${2:?candidate directory required}"
run_dir="${3:?run directory required}"
metrics_out="${4:?metrics output path required}"
resource_out="${5:?resource metrics output path required}"
verdict_out="${6:?verdict output path required}"

mkdir -p "$(dirname "$resource_out")"
timing_out="${resource_out}.time"
rm -f "$timing_out"

gateway_pid=""
gateway_log="${resource_out}.mock-gateway.log"
cleanup_gateway() {
  if [[ -n "$gateway_pid" ]] && kill -0 "$gateway_pid" 2>/dev/null; then
    kill "$gateway_pid" 2>/dev/null || true
    wait "$gateway_pid" 2>/dev/null || true
  fi
}
trap cleanup_gateway EXIT

if [[ -n "${CRUCIBLE_MOCK_GATEWAY_SOURCE:-}" ]]; then
  if [[ -z "${CRUCIBLE_HTTP_FIXTURES:-}" ]]; then
    echo "CRUCIBLE_HTTP_FIXTURES is required when CRUCIBLE_MOCK_GATEWAY_SOURCE is set" >&2
    exit 126
  fi
  if ! command -v go >/dev/null 2>&1; then
    echo "mock gateway startup requires go in the sandbox image" >&2
    exit 126
  fi
  gateway_addr="${CRUCIBLE_MOCK_GATEWAY_ADDR:-127.0.0.1:18080}"
  gateway_host="${gateway_addr%:*}"
  gateway_port="${gateway_addr##*:}"
  rm -f "$gateway_log"
  gateway_args=("$CRUCIBLE_MOCK_GATEWAY_SOURCE" -fixtures "$CRUCIBLE_HTTP_FIXTURES" -addr "$gateway_addr")
  if [[ -n "${CRUCIBLE_MOCK_CA_CERT:-}" && -n "${CRUCIBLE_MOCK_CA_KEY:-}" ]]; then
    gateway_args+=(-ca-cert "$CRUCIBLE_MOCK_CA_CERT" -ca-key "$CRUCIBLE_MOCK_CA_KEY")
  fi
  go run "${gateway_args[@]}" >"$gateway_log" 2>&1 &
  gateway_pid=$!
  gateway_ready=0
  for _ in {1..100}; do
    if ! kill -0 "$gateway_pid" 2>/dev/null; then
      echo "mock gateway exited during startup" >&2
      cat "$gateway_log" >&2 2>/dev/null || true
      exit 126
    fi
    if (: >"/dev/tcp/${gateway_host}/${gateway_port}") >/dev/null 2>&1; then
      gateway_ready=1
      break
    fi
    sleep 0.1
  done
  if [[ "$gateway_ready" != "1" ]]; then
    echo "mock gateway did not become ready on ${gateway_addr}" >&2
    cat "$gateway_log" >&2 2>/dev/null || true
    exit 126
  fi
fi

start_ns="$(date +%s%N 2>/dev/null || printf '0')"
exec 3>&2
TIMEFORMAT=$'real_seconds=%3R\nuser_seconds=%3U\nsystem_seconds=%3S'
{ time bash "$evaluator_path" "$candidate_dir" "$run_dir" "$metrics_out" "$verdict_out" 2>&3; } 2>"$timing_out"
status=$?
exec 3>&-
end_ns="$(date +%s%N 2>/dev/null || printf '0')"

real_seconds="$(awk -F= '$1 == "real_seconds" { print $2 }' "$timing_out" 2>/dev/null || printf '0')"
user_seconds="$(awk -F= '$1 == "user_seconds" { print $2 }' "$timing_out" 2>/dev/null || printf '0')"
system_seconds="$(awk -F= '$1 == "system_seconds" { print $2 }' "$timing_out" 2>/dev/null || printf '0')"
real_seconds="${real_seconds:-0}"
user_seconds="${user_seconds:-0}"
system_seconds="${system_seconds:-0}"

wall_time_ms="$(awk -v start="$start_ns" -v end="$end_ns" -v real="$real_seconds" 'BEGIN {
  if (start > 0 && end >= start) {
    printf "%.3f", (end - start) / 1000000
  } else {
    printf "%.3f", real * 1000
  }
}')"
cpu_percent="$(awk -v user="$user_seconds" -v sys="$system_seconds" -v wall_ms="$wall_time_ms" 'BEGIN {
  if (wall_ms > 0) {
    printf "%.6f", (user + sys) / (wall_ms / 1000) * 100
  } else {
    printf "0"
  }
}')"

cat > "$resource_out" <<JSON
{
  "wall_time_ms": $wall_time_ms,
  "cpu_user_seconds": $user_seconds,
  "cpu_system_seconds": $system_seconds,
  "cpu_percent": $cpu_percent,
  "resource_metric_source": "container-wrapper"
}
JSON

exit "$status"
`
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

func sandboxEnabled(opts SandboxOptions) bool {
	return opts.Engine == "docker" || opts.Engine == "podman"
}

func sandboxMount(path, access string) string {
	clean := filepath.Clean(path)
	return clean + ":" + clean + ":" + access
}

func sandboxHomeDir(runDir string) string {
	return filepath.Join(runDir, "tmp", "sandbox-home")
}

func processMetrics(state *os.ProcessState, wall time.Duration) model.Metrics {
	metrics := model.Metrics{
		WallTimeMS:           float64(wall.Microseconds()) / 1000,
		ResourceMetricSource: "host-process",
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
	if resource.ResourceMetricSource != "" {
		metrics.ResourceMetricSource = resource.ResourceMetricSource
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

func loadResourceMetrics(path string) (model.Metrics, error) {
	var metrics model.Metrics
	if err := loadJSON(path, &metrics); err != nil {
		return metrics, fmt.Errorf("resource metrics missing or invalid: %w", err)
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
