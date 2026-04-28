package run

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/discovery"
	externalfixtures "github.com/Automattic/code-crucible/internal/external"
	"github.com/Automattic/code-crucible/internal/model"
)

func TestEvaluateCandidatesUpdatesLeaderboard(t *testing.T) {
	projectDir, created := createEvaluationFixture(t)
	writeCandidateArtifact(t, filepath.Join(created.RunDir, "round-0001", "candidate-0001"), model.Candidate{
		ID:         "candidate-0001",
		Name:       "generated candidate",
		Round:      1,
		ParentIDs:  []string{"candidate-0000-baseline"},
		Agent:      "codex",
		SourcePath: "src",
		Baseline:   false,
	})

	if _, err := AdoptCandidates(AdoptionOptions{ProjectDir: projectDir, RunID: created.ID}); err != nil {
		t.Fatal(err)
	}

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
candidate_dir="$1"
run_dir="$2"
metrics_out="$3"
verdict_out="$4"
printf 'evaluating %s in %s\n' "$candidate_dir" "$run_dir"
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 12,
  "p95_latency_ms": 40,
  "memory_peak_bytes": 1048576,
  "external_call_count": 2
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir: projectDir,
		RunID:      created.ID,
		Adopt:      false,
		Jobs:       2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("evaluated results = %d, want baseline and generated candidate", len(report.Results))
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range board.Results {
		if result.Status != "passed" {
			t.Fatalf("%s status = %q, want passed", result.Candidate.ID, result.Status)
		}
		if result.Score <= 0 {
			t.Fatalf("%s score = %f, want positive", result.Candidate.ID, result.Score)
		}
		if result.ScoreExplanation == nil {
			t.Fatalf("%s score explanation is nil", result.Candidate.ID)
		}
		if result.ScoreExplanation.FinalScore != result.Score {
			t.Fatalf("%s explanation final score = %f, score = %f", result.Candidate.ID, result.ScoreExplanation.FinalScore, result.Score)
		}
		if result.ScoreExplanation.ExternalCallPenalty != 50 {
			t.Fatalf("%s external call penalty = %f, want 50", result.Candidate.ID, result.ScoreExplanation.ExternalCallPenalty)
		}
		if result.Metrics.P95LatencyMS != 40 {
			t.Fatalf("%s p95 = %f, want 40", result.Candidate.ID, result.Metrics.P95LatencyMS)
		}
		if result.Metrics.WallTimeMS <= 0 {
			t.Fatalf("%s wall time = %f, want positive", result.Candidate.ID, result.Metrics.WallTimeMS)
		}
		if result.Metrics.ResourceMetricSource != "host-process" {
			t.Fatalf("%s resource metric source = %q, want host-process", result.Candidate.ID, result.Metrics.ResourceMetricSource)
		}
	}

	metricsPath := filepath.Join(created.RunDir, "round-0001", "candidate-0000-baseline", "metrics.json")
	metrics, err := loadMetrics(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.WallTimeMS <= 0 {
		t.Fatalf("metrics artifact wall time = %f, want positive", metrics.WallTimeMS)
	}
	if metrics.ResourceMetricSource != "host-process" {
		t.Fatalf("metrics artifact resource source = %q, want host-process", metrics.ResourceMetricSource)
	}
}

func TestEvaluateCandidatesAggregatesRepeatedSamples(t *testing.T) {
	projectDir, created := createEvaluationFixture(t)

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
candidate_dir="$1"
metrics_out="$3"
verdict_out="$4"
counter="$candidate_dir/evaluation-counter"
current=0
if [[ -f "$counter" ]]; then
  current="$(cat "$counter")"
fi
current=$((current + 1))
printf '%s' "$current" > "$counter"
runtime=$((current * 10))
p95=$((runtime * 2))
memory=$((current * 100))
cat > "$metrics_out" <<JSON
{
  "runtime_mean_ms": $runtime,
  "p95_latency_ms": $p95,
  "memory_peak_bytes": $memory
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0000-baseline",
		Adopt:       false,
		Warmups:     1,
		Repetitions: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}
	if report.Results[0].SamplesPath == "" {
		t.Fatal("SamplesPath is empty")
	}
	if _, err := os.Stat(filepath.FromSlash(report.Results[0].SamplesPath)); err != nil {
		t.Fatalf("evaluation samples missing: %v", err)
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	metrics := board.Results[0].Metrics
	if metrics.EvaluationWarmups != 1 || metrics.EvaluationRepetitions != 3 {
		t.Fatalf("warmups/repetitions = %d/%d, want 1/3", metrics.EvaluationWarmups, metrics.EvaluationRepetitions)
	}
	if metrics.RuntimeMeanMS != 30 {
		t.Fatalf("runtime_mean_ms = %f, want 30", metrics.RuntimeMeanMS)
	}
	if metrics.RuntimeMinMS != 20 || metrics.RuntimeMaxMS != 40 {
		t.Fatalf("runtime min/max = %f/%f, want 20/40", metrics.RuntimeMinMS, metrics.RuntimeMaxMS)
	}
	if metrics.RuntimeStddevMS < 8.16 || metrics.RuntimeStddevMS > 8.17 {
		t.Fatalf("runtime_stddev_ms = %f, want about 8.165", metrics.RuntimeStddevMS)
	}
	if metrics.RuntimeMedianMS != 30 {
		t.Fatalf("runtime_median_ms = %f, want 30", metrics.RuntimeMedianMS)
	}
	if metrics.RuntimeStdErrorMS < 4.71 || metrics.RuntimeStdErrorMS > 4.72 {
		t.Fatalf("runtime_std_error_ms = %f, want about 4.714", metrics.RuntimeStdErrorMS)
	}
	if metrics.RuntimeCI95HalfWidthMS < 9.23 || metrics.RuntimeCI95HalfWidthMS > 9.24 {
		t.Fatalf("runtime_ci95_half_width_ms = %f, want about 9.238", metrics.RuntimeCI95HalfWidthMS)
	}
	if metrics.P95LatencyMS != 60 {
		t.Fatalf("p95_latency_ms = %f, want 60", metrics.P95LatencyMS)
	}
	if metrics.MemoryPeakBytes != 400 {
		t.Fatalf("memory_peak_bytes = %d, want 400", metrics.MemoryPeakBytes)
	}
}

func TestEvaluateCandidatesTrimsOutliersAndUsesConfiguredSampleStat(t *testing.T) {
	projectDir, created := createEvaluationFixture(t)

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
candidate_dir="$1"
metrics_out="$3"
verdict_out="$4"
counter="$candidate_dir/evaluation-counter"
current=0
if [[ -f "$counter" ]]; then
  current="$(cat "$counter")"
fi
current=$((current + 1))
printf '%s' "$current" > "$counter"
case "$current" in
  1) runtime=10 ;;
  2) runtime=30 ;;
  3) runtime=40 ;;
  *) runtime=1000 ;;
esac
cat > "$metrics_out" <<JSON
{
  "runtime_mean_ms": $runtime,
  "p95_latency_ms": $runtime
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0000-baseline",
		Adopt:       false,
		Repetitions: 4,
		OutlierMode: "trim-min-max",
		SampleStat:  "median",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	metrics := board.Results[0].Metrics
	if metrics.EvaluationOutlierMode != "trim-min-max" {
		t.Fatalf("outlier mode = %q", metrics.EvaluationOutlierMode)
	}
	if metrics.EvaluationSampleStat != "median" {
		t.Fatalf("sample stat = %q", metrics.EvaluationSampleStat)
	}
	if metrics.EvaluationOutliersRemoved != 2 {
		t.Fatalf("outliers removed = %d, want 2", metrics.EvaluationOutliersRemoved)
	}
	if metrics.EvaluationRepetitions != 2 {
		t.Fatalf("included repetitions = %d, want 2", metrics.EvaluationRepetitions)
	}
	if metrics.RuntimeMeanMS != 35 || metrics.P95LatencyMS != 35 {
		t.Fatalf("runtime/p95 = %f/%f, want 35/35", metrics.RuntimeMeanMS, metrics.P95LatencyMS)
	}
	if metrics.RuntimeMinMS != 30 || metrics.RuntimeMaxMS != 40 {
		t.Fatalf("runtime min/max = %f/%f, want 30/40", metrics.RuntimeMinMS, metrics.RuntimeMaxMS)
	}
}

func TestEvaluateCandidatesAppliesEnvironment(t *testing.T) {
	projectDir, created := createEvaluationFixture(t)

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
metrics_out="$3"
verdict_out="$4"
if [[ "${CRUCIBLE_TEST_ENV:-}" != "ok" ]]; then
  echo "missing CRUCIBLE_TEST_ENV" >&2
  exit 3
fi
sleep 0.01
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 1
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0000-baseline",
		Adopt:       false,
		Env:         []string{"CRUCIBLE_TEST_ENV=ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	result := board.Results[0]
	if result.Status != "passed" {
		t.Fatalf("status = %q, want passed", result.Status)
	}
	if result.Metrics.WallTimeMS <= 0 {
		t.Fatalf("wall time = %f, want positive", result.Metrics.WallTimeMS)
	}
}

func TestEvaluateCandidatesAppliesExternalFixtureEnvironment(t *testing.T) {
	projectDir, created := createEvaluationFixtureWithExternalMode(t, "replay")
	gatewayAddr := freeTCPAddr(t)
	gatewayHost, gatewayPort, err := net.SplitHostPort(gatewayAddr)
	if err != nil {
		t.Fatal(err)
	}

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
metrics_out="$3"
verdict_out="$4"
if [[ "${CRUCIBLE_EXTERNAL_MODE:-}" != "replay" ]]; then
  echo "missing CRUCIBLE_EXTERNAL_MODE" >&2
  exit 3
fi
if [[ ! -f "${CRUCIBLE_HTTP_FIXTURES:-}" ]]; then
  echo "missing CRUCIBLE_HTTP_FIXTURES" >&2
  exit 3
fi
if [[ ! -f "${CRUCIBLE_MOCK_GATEWAY_SOURCE:-}" ]]; then
  echo "missing CRUCIBLE_MOCK_GATEWAY_SOURCE" >&2
  exit 3
fi
if [[ "${CRUCIBLE_MOCK_GATEWAY_ADDR:-}" != "__GATEWAY_ADDR__" ]]; then
  echo "missing CRUCIBLE_MOCK_GATEWAY_ADDR" >&2
  exit 3
fi
if [[ "${CRUCIBLE_MOCK_GATEWAY_URL:-}" != "http://__GATEWAY_ADDR__" ]]; then
  echo "missing CRUCIBLE_MOCK_GATEWAY_URL" >&2
  exit 3
fi
if ! (: >"/dev/tcp/__GATEWAY_HOST__/__GATEWAY_PORT__") >/dev/null 2>&1; then
  echo "local mock gateway is not reachable" >&2
  exit 3
fi
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 1
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	evaluator = strings.ReplaceAll(evaluator, "__GATEWAY_ADDR__", gatewayAddr)
	evaluator = strings.ReplaceAll(evaluator, "__GATEWAY_HOST__", gatewayHost)
	evaluator = strings.ReplaceAll(evaluator, "__GATEWAY_PORT__", gatewayPort)
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0000-baseline",
		Adopt:       false,
		Env:         []string{"CRUCIBLE_MOCK_GATEWAY_ADDR=" + gatewayAddr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}
	if report.Results[0].Status != "passed" {
		t.Fatalf("status = %q, want passed", report.Results[0].Status)
	}
}

func TestExternalEvaluationEnvAddsFixtureGatewayDefaults(t *testing.T) {
	runDir := t.TempDir()
	externalDir := filepath.Join(runDir, "external")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gatewaySource := filepath.Join(externalDir, externalfixtures.MockGatewayName)
	if err := externalfixtures.WriteMockGateway(gatewaySource); err != nil {
		t.Fatal(err)
	}
	caCertPath := filepath.Join(externalDir, externalfixtures.MockCACertName)
	caKeyPath := filepath.Join(externalDir, externalfixtures.MockCAKeyName)
	if err := externalfixtures.WriteMockCA(caCertPath, caKeyPath); err != nil {
		t.Fatal(err)
	}
	fixturesPath := filepath.Join(externalDir, externalfixtures.HTTPFixturesName)

	env := externalEvaluationEnv(model.ExternalPolicy{
		Mode:     model.ExternalModeMock,
		Fixtures: fixturesPath,
	}, runDir, []string{"CRUCIBLE_MOCK_GATEWAY_ADDR=127.0.0.1:19090"})

	for _, want := range []string{
		"CRUCIBLE_EXTERNAL_MODE=mock",
		"CRUCIBLE_HTTP_FIXTURES=" + filepath.ToSlash(fixturesPath),
		"CRUCIBLE_MOCK_GATEWAY_SOURCE=" + filepath.ToSlash(gatewaySource),
		"CRUCIBLE_MOCK_CA_CERT=" + filepath.ToSlash(caCertPath),
		"CRUCIBLE_MOCK_CA_KEY=" + filepath.ToSlash(caKeyPath),
		"SSL_CERT_FILE=" + filepath.ToSlash(caCertPath),
		"REQUESTS_CA_BUNDLE=" + filepath.ToSlash(caCertPath),
		"CURL_CA_BUNDLE=" + filepath.ToSlash(caCertPath),
		"NODE_EXTRA_CA_CERTS=" + filepath.ToSlash(caCertPath),
		"GIT_SSL_CAINFO=" + filepath.ToSlash(caCertPath),
		"CRUCIBLE_MOCK_GATEWAY_ADDR=127.0.0.1:19090",
		"CRUCIBLE_MOCK_GATEWAY_URL=http://127.0.0.1:19090",
		"HTTP_PROXY=http://127.0.0.1:19090",
		"http_proxy=http://127.0.0.1:19090",
		"HTTPS_PROXY=http://127.0.0.1:19090",
		"https_proxy=http://127.0.0.1:19090",
		"NO_PROXY=localhost,127.0.0.1,::1",
		"no_proxy=localhost,127.0.0.1,::1",
	} {
		if !containsArg(env, want) {
			t.Fatalf("env missing %q: %#v", want, env)
		}
	}
}

func TestExternalEvaluationEnvAddsAllowlistGatewayDefaults(t *testing.T) {
	runDir := t.TempDir()
	externalDir := filepath.Join(runDir, "external")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gatewaySource := filepath.Join(externalDir, externalfixtures.MockGatewayName)
	if err := externalfixtures.WriteMockGateway(gatewaySource); err != nil {
		t.Fatal(err)
	}
	fixturesPath := filepath.Join(externalDir, externalfixtures.HTTPFixturesName)
	if err := externalfixtures.SaveHTTPFixtureSet(fixturesPath, externalfixtures.EmptyHTTPFixtureSet()); err != nil {
		t.Fatal(err)
	}

	env := externalEvaluationEnv(model.ExternalPolicy{
		Mode:      model.ExternalModeAllowlist,
		Allowlist: []string{"api.example.com", "auth.example.com", "api.example.com"},
		Fixtures:  fixturesPath,
	}, runDir, nil)

	for _, want := range []string{
		"CRUCIBLE_EXTERNAL_MODE=allowlist",
		"CRUCIBLE_HTTP_FIXTURES=" + filepath.ToSlash(fixturesPath),
		"CRUCIBLE_MOCK_GATEWAY_SOURCE=" + filepath.ToSlash(gatewaySource),
		"CRUCIBLE_ALLOWED_HOSTS=api.example.com,auth.example.com",
		"HTTP_PROXY=http://127.0.0.1:18080",
		"HTTPS_PROXY=http://127.0.0.1:18080",
	} {
		if !containsArg(env, want) {
			t.Fatalf("env missing %q: %#v", want, env)
		}
	}
}

func TestExternalCandidateEnvAddsTraceAndRecordPaths(t *testing.T) {
	candidateDir := t.TempDir()
	env, tracePath, recordPath := externalCandidateEnv(model.ExternalPolicy{
		Mode: model.ExternalModeRecord,
	}, SandboxOptions{}, candidateDir, []string{
		"CRUCIBLE_MOCK_GATEWAY_SOURCE=/tmp/mock-gateway.go",
		"CRUCIBLE_MOCK_GATEWAY_ADDR=127.0.0.1:18080",
		"CRUCIBLE_MOCK_GATEWAY_URL=http://127.0.0.1:18080",
		"HTTP_PROXY=http://127.0.0.1:18080",
		"HTTPS_PROXY=http://127.0.0.1:18080",
	})

	if filepath.Base(tracePath) != "external-trace.json" {
		t.Fatalf("tracePath = %q, want external-trace.json", tracePath)
	}
	if filepath.Base(recordPath) != "recorded-http-fixtures.json" {
		t.Fatalf("recordPath = %q, want recorded-http-fixtures.json", recordPath)
	}
	for _, wantPrefix := range []string{
		"CRUCIBLE_EXTERNAL_TRACE=" + filepath.ToSlash(tracePath),
		"CRUCIBLE_RECORD_FIXTURES=" + filepath.ToSlash(recordPath),
		"CRUCIBLE_MOCK_GATEWAY_ADDR=127.0.0.1:",
		"HTTP_PROXY=http://127.0.0.1:",
		"HTTPS_PROXY=http://127.0.0.1:",
	} {
		if !containsPrefix(env, wantPrefix) {
			t.Fatalf("env missing prefix %q: %#v", wantPrefix, env)
		}
	}
}

func TestWrapTasksetCommand(t *testing.T) {
	name, args := wrapTasksetCommand("bash", []string{"evaluator.sh"}, 2)
	if name != "taskset" {
		t.Fatalf("name = %q, want taskset", name)
	}
	want := []string{"-c", "0-1", "bash", "evaluator.sh"}
	if len(args) != len(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args = %#v, want %#v", args, want)
		}
	}
}

func TestNormalizeSandboxOptions(t *testing.T) {
	sandbox, err := NormalizeSandboxOptions(SandboxOptions{
		Engine: "docker",
		Image:  "golang:1.25",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sandbox.Network != "none" {
		t.Fatalf("network = %q, want none", sandbox.Network)
	}
	if sandbox.Profile != "default" {
		t.Fatalf("profile = %q, want default", sandbox.Profile)
	}

	strictSandbox, err := NormalizeSandboxOptions(SandboxOptions{
		Profile: "strict",
		Engine:  "docker",
		Image:   "golang:1.25",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strictSandbox.Network != "none" || strictSandbox.MemoryLimit != "1g" || strictSandbox.PIDsLimit != 256 {
		t.Fatalf("strict sandbox = %#v, want network none, memory 1g, pids 256", strictSandbox)
	}

	networkedSandbox, err := NormalizeSandboxOptions(SandboxOptions{
		Profile: "networked",
		Engine:  "podman",
		Image:   "golang:1.25",
	})
	if err != nil {
		t.Fatal(err)
	}
	if networkedSandbox.Network != "bridge" || networkedSandbox.MemoryLimit != "1g" || networkedSandbox.PIDsLimit != 256 {
		t.Fatalf("networked sandbox = %#v, want network bridge, memory 1g, pids 256", networkedSandbox)
	}

	overrideSandbox, err := NormalizeSandboxOptions(SandboxOptions{
		Profile:     "networked",
		Engine:      "docker",
		Image:       "golang:1.25",
		Network:     "host",
		MemoryLimit: "512m",
		PIDsLimit:   32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if overrideSandbox.Network != "host" || overrideSandbox.MemoryLimit != "512m" || overrideSandbox.PIDsLimit != 32 {
		t.Fatalf("override sandbox = %#v, want explicit values preserved", overrideSandbox)
	}

	if _, err := NormalizeSandboxOptions(SandboxOptions{Engine: "docker"}); err == nil {
		t.Fatal("expected missing image error")
	}
	if _, err := NormalizeSandboxOptions(SandboxOptions{Engine: "local", Image: "golang:1.25"}); err == nil {
		t.Fatal("expected local image error")
	}
	if _, err := NormalizeSandboxOptions(SandboxOptions{Engine: "local", MemoryLimit: "1g"}); err == nil {
		t.Fatal("expected local memory limit error")
	}
	if _, err := NormalizeSandboxOptions(SandboxOptions{Engine: "local", PIDsLimit: 64}); err == nil {
		t.Fatal("expected local pids limit error")
	}
	if _, err := NormalizeSandboxOptions(SandboxOptions{Engine: "local", Profile: "strict"}); err == nil {
		t.Fatal("expected local strict profile error")
	}
	if _, err := NormalizeSandboxOptions(SandboxOptions{Engine: "docker", Image: "golang:1.25", Profile: "strict", Network: "bridge"}); err == nil {
		t.Fatal("expected strict bridge network error")
	}
}

func TestBuildContainerEvaluatorCommand(t *testing.T) {
	projectDir := t.TempDir()
	runDir := filepath.Join(projectDir, ".crucible", "runs", "run")
	candidateDir := filepath.Join(runDir, "round-0001", "candidate-0001")
	evaluatorPath := filepath.Join(runDir, "evaluator", "evaluator.sh")
	metricsPath := filepath.Join(candidateDir, "metrics.json")
	resourcePath := filepath.Join(candidateDir, "resource-metrics.json")
	verdictPath := filepath.Join(candidateDir, "verdict.json")

	name, args, err := buildEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, evaluatorExecutionOptions{
		ProjectDir: projectDir,
		CPULimit:   2,
		Env:        []string{"GOMAXPROCS=1"},
		Sandbox: SandboxOptions{
			Engine:      "podman",
			Image:       "golang:1.25",
			Network:     "none",
			MemoryLimit: "1g",
			PIDsLimit:   256,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "podman" {
		t.Fatalf("name = %q, want podman", name)
	}

	for _, want := range []string{
		"run",
		"--rm",
		"--network",
		"none",
		"--volume",
		sandboxMount(runDir, "rw"),
		"--volume",
		sandboxMount(projectDir, "ro"),
		"--cpus",
		"2",
		"--memory",
		"1g",
		"--pids-limit",
		"256",
		"--userns",
		"keep-id",
		"--workdir",
		runDir,
		"--env",
		"HOME=" + sandboxHomeDir(runDir),
		"--env",
		"GOMAXPROCS=1",
		"golang:1.25",
		"bash",
		sandboxResourceWrapperPath(runDir),
		evaluatorPath,
		candidateDir,
		runDir,
		metricsPath,
		resourcePath,
		verdictPath,
	} {
		if !containsArg(args, want) {
			t.Fatalf("args missing %q: %#v", want, args)
		}
	}
}

func TestSandboxResourceWrapperStartsMockGateway(t *testing.T) {
	script := sandboxResourceWrapperScript()
	for _, want := range []string{
		"CRUCIBLE_MOCK_GATEWAY_SOURCE",
		"CRUCIBLE_MOCK_GATEWAY_BIN",
		"CRUCIBLE_HTTP_FIXTURES",
		"CRUCIBLE_ALLOWED_HOSTS",
		"CRUCIBLE_EXTERNAL_TRACE",
		"CRUCIBLE_RECORD_FIXTURES",
		"gateway_args=(-fixtures",
		"\"$CRUCIBLE_MOCK_GATEWAY_BIN\" \"${gateway_args[@]}\"",
		"-allow-hosts",
		"-passthrough",
		"-trace",
		"-record-fixtures",
		"-ca-cert",
		"-ca-key",
		"go run \"$CRUCIBLE_MOCK_GATEWAY_SOURCE\" \"${gateway_args[@]}\"",
		"/dev/tcp/${gateway_host}/${gateway_port}",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("resource wrapper missing %q", want)
		}
	}
}

func TestEnsureSandboxMockGatewayBinaryUsesPackagedBinary(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, externalfixtures.MockGatewayName)
	if err := os.WriteFile(sourcePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(dir, externalfixtures.MockGatewayBinaryName("linux", runtime.GOARCH))
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	env, err := ensureSandboxMockGatewayBinary([]string{
		"CRUCIBLE_MOCK_GATEWAY_SOURCE=" + filepath.ToSlash(sourcePath),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(env, "CRUCIBLE_MOCK_GATEWAY_BIN"); got != filepath.ToSlash(binaryPath) {
		t.Fatalf("CRUCIBLE_MOCK_GATEWAY_BIN = %q, want %q", got, filepath.ToSlash(binaryPath))
	}
}

func TestStartLocalMockGatewayRejectsUsedAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	cleanup, err := startLocalMockGateway(context.Background(), []string{
		"CRUCIBLE_MOCK_GATEWAY_SOURCE=/tmp/mock-gateway.go",
		"CRUCIBLE_HTTP_FIXTURES=/tmp/http-fixtures.json",
		"CRUCIBLE_MOCK_GATEWAY_ADDR=" + listener.Addr().String(),
	}, filepath.Join(t.TempDir(), "gateway.log"))
	cleanup()
	if err == nil {
		t.Fatal("expected used address error")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("error = %v, want address in use", err)
	}
}

func TestStartLocalMockGatewayRequiresAllowlistHosts(t *testing.T) {
	cleanup, err := startLocalMockGateway(context.Background(), []string{
		"CRUCIBLE_EXTERNAL_MODE=allowlist",
		"CRUCIBLE_MOCK_GATEWAY_SOURCE=/tmp/mock-gateway.go",
		"CRUCIBLE_HTTP_FIXTURES=/tmp/http-fixtures.json",
	}, filepath.Join(t.TempDir(), "gateway.log"))
	cleanup()
	if err == nil {
		t.Fatal("expected missing allowlist hosts error")
	}
	if !strings.Contains(err.Error(), "CRUCIBLE_ALLOWED_HOSTS") {
		t.Fatalf("error = %v, want missing allowed hosts", err)
	}
}

func TestMergeResourceMetricsCopiesSource(t *testing.T) {
	metrics := mergeResourceMetrics(model.Metrics{RuntimeMeanMS: 1}, model.Metrics{
		WallTimeMS:           250,
		CPUUserSeconds:       0.2,
		ResourceMetricSource: "container-wrapper",
	})
	if metrics.RuntimeMeanMS != 1 {
		t.Fatalf("runtime_mean_ms = %f, want evaluator metric preserved", metrics.RuntimeMeanMS)
	}
	if metrics.WallTimeMS != 250 {
		t.Fatalf("wall_time_ms = %f, want resource metric merged", metrics.WallTimeMS)
	}
	if metrics.ResourceMetricSource != "container-wrapper" {
		t.Fatalf("resource metric source = %q, want container-wrapper", metrics.ResourceMetricSource)
	}
}

func TestExternalPolicyEnforcement(t *testing.T) {
	enforcement := externalPolicyEnforcement(model.ExternalPolicy{Mode: model.ExternalModeDeny}, SandboxOptions{
		Engine:  "docker",
		Image:   "golang:1.22",
		Network: "none",
	})
	if enforcement.Status != "enforced" {
		t.Fatalf("deny container status = %q, want enforced", enforcement.Status)
	}
	if len(enforcement.Errors) != 0 {
		t.Fatalf("deny container errors = %#v, want none", enforcement.Errors)
	}

	enforcement = externalPolicyEnforcement(model.ExternalPolicy{Mode: model.ExternalModeDeny}, SandboxOptions{
		Engine:  "docker",
		Image:   "golang:1.22",
		Network: "bridge",
	})
	if enforcement.Status != "failed" || len(enforcement.Errors) == 0 {
		t.Fatalf("deny bridge enforcement = %#v, want failed with error", enforcement)
	}

	enforcement = externalPolicyEnforcement(model.ExternalPolicy{Mode: model.ExternalModeReplay}, SandboxOptions{
		Engine:  "podman",
		Image:   "golang:1.22",
		Network: "none",
	})
	if enforcement.Status != "partial" || len(enforcement.Warnings) == 0 {
		t.Fatalf("replay container enforcement = %#v, want partial with warning", enforcement)
	}

	enforcement = externalPolicyEnforcement(model.ExternalPolicy{Mode: model.ExternalModeAllowlist}, SandboxOptions{})
	if enforcement.Status != "failed" || len(enforcement.Errors) == 0 {
		t.Fatalf("empty allowlist enforcement = %#v, want failed with error", enforcement)
	}

	enforcement = externalPolicyEnforcement(model.ExternalPolicy{
		Mode:      model.ExternalModeAllowlist,
		Allowlist: []string{"api.example.com"},
	}, SandboxOptions{})
	if enforcement.Status != "partial" || enforcement.Mechanism != "proxy-allowlist" || len(enforcement.Warnings) == 0 {
		t.Fatalf("allowlist enforcement = %#v, want partial proxy warning", enforcement)
	}

	enforcement = externalPolicyEnforcement(model.ExternalPolicy{Mode: model.ExternalModeRecord}, SandboxOptions{})
	if enforcement.Status != "partial" || enforcement.Mechanism != "proxy-record" || len(enforcement.Warnings) == 0 {
		t.Fatalf("record enforcement = %#v, want partial proxy record warning", enforcement)
	}
}

func TestEvaluateCandidatesFailsClosedWhenVerdictMissing(t *testing.T) {
	projectDir, created := createEvaluationFixture(t)

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
metrics_out="$3"
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 3
}
JSON
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0000-baseline",
		Adopt:       false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	result := board.Results[0]
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Verdict.CorrectnessPassed || len(result.Verdict.Errors) == 0 {
		t.Fatalf("verdict did not fail closed: %#v", result.Verdict)
	}
	if result.Score != 0 {
		t.Fatalf("score = %f, want 0", result.Score)
	}
	if result.ScoreExplanation == nil {
		t.Fatal("score explanation is nil")
	}
	if result.ScoreExplanation.Reason != "correctness failed" {
		t.Fatalf("score explanation reason = %q, want correctness failed", result.ScoreExplanation.Reason)
	}
}

func TestEvaluateCandidatesFailsClosedWhenEvaluatorExitsAfterWritingPassedVerdict(t *testing.T) {
	projectDir, created := createEvaluationFixture(t)
	candidateDir := filepath.Join(created.RunDir, "round-0001", "candidate-0000-baseline")
	if err := os.WriteFile(filepath.Join(candidateDir, "metrics.json"), []byte(`{"runtime_mean_ms": 999}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "verdict.json"), []byte(`{"correctness_passed":true,"benchmark_passed":true,"external_policy_passed":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
metrics_out="$3"
verdict_out="$4"
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 1
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
exit 7
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0000-baseline",
		Adopt:       false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	result := board.Results[0]
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.Metrics.RuntimeMeanMS != 1 {
		t.Fatalf("runtime_mean_ms = %f, want freshly written metrics", result.Metrics.RuntimeMeanMS)
	}
	if result.Verdict.BenchmarkPassed {
		t.Fatalf("benchmark_passed = true, want evaluator process failure to fail closed")
	}
}

func TestEvaluateCandidatesFailsContractBeforeBenchmarking(t *testing.T) {
	projectDir, created := createEvaluationFixture(t)
	candidateDir := filepath.Join(created.RunDir, "round-0001", "candidate-0001")
	if err := os.MkdirAll(filepath.Join(candidateDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "src", "notes.txt"), []byte("not a Go replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "design.md"), []byte("# Candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidate := model.Candidate{
		ID:         "candidate-0001",
		Name:       "wrong language",
		Round:      1,
		ParentIDs:  []string{"candidate-0000-baseline"},
		Agent:      "codex",
		SourcePath: "src",
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "candidate.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AdoptCandidates(AdoptionOptions{ProjectDir: projectDir, RunID: created.ID}); err != nil {
		t.Fatal(err)
	}

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
touch "$1/evaluator-ran"
metrics_out="$3"
verdict_out="$4"
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 1
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0001",
		Adopt:       false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}
	if _, err := os.Stat(filepath.Join(candidateDir, "evaluator-ran")); !os.IsNotExist(err) {
		t.Fatalf("evaluator should not have run, stat err: %v", err)
	}
	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result model.CandidateResult
	for _, candidateResult := range board.Results {
		if candidateResult.Candidate.ID == "candidate-0001" {
			result = candidateResult
			break
		}
	}
	if result.Candidate.ID == "" {
		t.Fatal("candidate-0001 missing from leaderboard")
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if len(result.Verdict.Errors) == 0 || !strings.Contains(result.Verdict.Errors[0], "candidate contract failed") {
		t.Fatalf("verdict errors = %#v, want contract failure", result.Verdict.Errors)
	}
	if !result.Verdict.ExternalPolicyPassed {
		t.Fatalf("external_policy_passed = false, want contract failure to preserve passed policy state")
	}
}

func TestEvaluateCandidatesFailsGoSignatureContractBeforeBenchmarking(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "checkout")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	baselineSource := `package checkout

type Cart struct{}
type Money int

func PriceCheckout(cart Cart) Money { return 0 }
`
	if err := os.WriteFile(filepath.Join(sourceDir, "pricing.go"), []byte(baselineSource), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := &discovery.AgentPlan{
		SourcePath:      "internal/checkout/pricing.go",
		DropInInterface: "PriceCheckout(cart Cart) Money",
		Inputs:          []string{"cart fixture"},
		Outputs:         []string{"priced total"},
	}
	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "reduce checkout pricing latency",
		SourcePath:   plan.SourcePath,
		ExternalMode: "deny",
		AgentPlan:    plan,
	})
	if err != nil {
		t.Fatal(err)
	}

	candidateDir := filepath.Join(created.RunDir, "round-0001", "candidate-0001")
	srcDir := filepath.Join(candidateDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candidateSource := `package checkout

type Cart struct{}
type Money int

func PriceCheckout(cart Cart, discount int) Money { return 0 }
`
	if err := os.WriteFile(filepath.Join(srcDir, "pricing.go"), []byte(candidateSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "design.md"), []byte("# Candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidate := model.Candidate{
		ID:         "candidate-0001",
		Name:       "changed signature",
		Round:      1,
		ParentIDs:  []string{"candidate-0000-baseline"},
		Agent:      "codex",
		SourcePath: "src",
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "candidate.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AdoptCandidates(AdoptionOptions{ProjectDir: projectDir, RunID: created.ID}); err != nil {
		t.Fatal(err)
	}

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
touch "$1/evaluator-ran"
metrics_out="$3"
verdict_out="$4"
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 1
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0001",
		Adopt:       false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}
	if _, err := os.Stat(filepath.Join(candidateDir, "evaluator-ran")); !os.IsNotExist(err) {
		t.Fatalf("evaluator should not have run, stat err: %v", err)
	}
	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result model.CandidateResult
	for _, candidateResult := range board.Results {
		if candidateResult.Candidate.ID == "candidate-0001" {
			result = candidateResult
			break
		}
	}
	if result.Candidate.ID == "" {
		t.Fatal("candidate-0001 missing from leaderboard")
	}
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if len(result.Verdict.Errors) == 0 || !strings.Contains(result.Verdict.Errors[0], "signature changed") {
		t.Fatalf("verdict errors = %#v, want signature contract failure", result.Verdict.Errors)
	}
	if !result.Verdict.ExternalPolicyPassed {
		t.Fatalf("external_policy_passed = false, want signature failure to preserve passed policy state")
	}
}

func TestEvaluateCandidatesFailsClosedWhenDenyPolicyIsNotIsolated(t *testing.T) {
	projectDir, created := createEvaluationFixture(t)

	evaluator := `#!/usr/bin/env bash
set -euo pipefail
metrics_out="$3"
verdict_out="$4"
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 1
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	if err := os.WriteFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"), []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := EvaluateCandidates(EvaluationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0000-baseline",
		Adopt:       false,
		Sandbox: SandboxOptions{
			Engine:  "docker",
			Image:   "golang:1.22",
			Network: "bridge",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("evaluated results = %d, want one", len(report.Results))
	}
	if report.Results[0].ExternalPolicy == nil || report.Results[0].ExternalPolicy.Status != "failed" {
		t.Fatalf("report external policy = %#v, want failed", report.Results[0].ExternalPolicy)
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	result := board.Results[0]
	if result.Status != "failed" {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.External.PolicyEnforcement == nil || result.External.PolicyEnforcement.Status != "failed" {
		t.Fatalf("leaderboard external policy = %#v, want failed", result.External.PolicyEnforcement)
	}
	if result.Verdict.ExternalPolicyPassed {
		t.Fatalf("external_policy_passed = true, want false")
	}
}

func createEvaluationFixture(t *testing.T) (string, *CreatedRun) {
	t.Helper()
	return createEvaluationFixtureWithExternalMode(t, "deny")
}

func createEvaluationFixtureWithExternalMode(t *testing.T, externalMode string) (string, *CreatedRun) {
	t.Helper()
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: externalMode,
		Agent:        "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	return projectDir, created
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func containsPrefix(args []string, want string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, want) {
			return true
		}
	}
	return false
}
