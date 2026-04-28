package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

func TestRunGenerateShortcutInvokesCodex(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeCodex := filepath.Join(t.TempDir(), "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
out=""
while [[ $# -gt 0 ]]; do
  if [[ "$1" == "--output-last-message" ]]; then
    out="$2"
    shift 2
    continue
  fi
  shift
done
cat >/dev/null
mkdir -p "$(dirname "$out")"
printf 'fake codex complete\n' > "$out"
round="$(find .crucible/runs -name round-0001 -type d | sort | tail -n1)"
mkdir -p "$round/candidate-0001/src"
printf 'package search\n\nfunc Rank() int { return 2 }\n' > "$round/candidate-0001/src/rank.go"
printf '# Candidate\n' > "$round/candidate-0001/design.md"
cat > "$round/candidate-0001/candidate.json" <<'JSON'
{
  "id": "candidate-0001",
  "name": "fake generated candidate",
  "round": 1,
  "parent_ids": ["candidate-0000-baseline"],
  "agent": "codex",
  "source_path": "src",
  "baseline": false
}
JSON
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--target-path", "internal/search/rank.go",
		"--variants", "2",
		"--generate",
		"--codex-bin", fakeCodex,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, stderr: %s", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "Codex generation complete") {
		t.Fatalf("stdout did not include generation completion:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Added: candidate-0001") {
		t.Fatalf("stdout did not include adoption result:\n%s", stdout.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "agents", "codex-final.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one Codex final message artifact, found %d", len(matches))
	}
}

func TestRunGenerateRejectsUnsupportedAgentBeforeCreatingRun(t *testing.T) {
	projectDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--generate",
		"--agent", "prompt",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("Run returned %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "run --generate currently supports only --agent codex") {
		t.Fatalf("stderr did not explain unsupported agent:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".crucible", "runs")); !os.IsNotExist(err) {
		t.Fatalf("expected no run archive to be created, stat err: %v", err)
	}
}

func TestRunTaskFileCreatesRun(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	task := "# Optimization Task\n\nMake ranking faster.\n"
	taskPath := filepath.Join(projectDir, "task.md")
	if err := os.WriteFile(taskPath, []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--task-file", filepath.Base(taskPath),
		"--target-path", "internal/search/rank.go",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Optimize != strings.TrimSpace(task) {
		t.Fatalf("Optimize = %q, want %q", cfg.Optimize, strings.TrimSpace(task))
	}
}

func TestRunRejectsOptimizeAndTaskFileTogether(t *testing.T) {
	projectDir := t.TempDir()
	taskPath := filepath.Join(projectDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("make ranking faster\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--task-file", taskPath,
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--optimize and --task-file cannot be used together") {
		t.Fatalf("stderr did not explain task conflict:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".crucible", "runs")); !os.IsNotExist(err) {
		t.Fatalf("expected no run archive to be created, stat err: %v", err)
	}
}

func TestEvaluateCommandUpdatesLeaderboard(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--target-path", "internal/search/rank.go",
		"--evaluator", "true",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"evaluate",
		"--project", projectDir,
		"--candidate", "candidate-0000-baseline",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("evaluate returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "candidate-0000-baseline") {
		t.Fatalf("stdout did not include evaluated candidate:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "passed") {
		t.Fatalf("stdout did not include passed status:\n%s", stdout.String())
	}
}

func TestNextRoundCommandPreparesActiveRound(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--target-path", "internal/search/rank.go",
		"--evaluator", "true",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"evaluate",
		"--project", projectDir,
		"--candidate", "candidate-0000-baseline",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("evaluate returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"next-round",
		"--project", projectDir,
		"--parents", "1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("next-round returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Prepared round 2") {
		t.Fatalf("stdout did not describe prepared round:\n%s", stdout.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.FromSlash(cfg.RoundDir), "round-0002") {
		t.Fatalf("RoundDir = %q, want round-0002", cfg.RoundDir)
	}
}

func TestEvaluateRejectsInvalidResourceOptions(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "jobs",
			args: []string{"evaluate", "--jobs", "0"},
			want: "--jobs must be at least 1",
		},
		{
			name: "nice",
			args: []string{"evaluate", "--nice", "20"},
			want: "--nice must be between 0 and 19",
		},
		{
			name: "cpu limit",
			args: []string{"evaluate", "--cpu-limit", "-1"},
			want: "--cpu-limit must be at least 0",
		},
		{
			name: "sandbox engine",
			args: []string{"evaluate", "--sandbox-engine", "jail"},
			want: "--sandbox-engine must be local, docker, or podman",
		},
		{
			name: "sandbox image",
			args: []string{"evaluate", "--sandbox-engine", "docker"},
			want: "--sandbox-image is required when --sandbox-engine is docker",
		},
		{
			name: "local sandbox image",
			args: []string{"evaluate", "--sandbox-image", "golang:1.25"},
			want: "--sandbox-image requires --sandbox-engine docker or podman",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("Run returned %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestRankedResultsPrioritizesPassedScoreThenLatency(t *testing.T) {
	results := []model.CandidateResult{
		{
			Candidate: model.Candidate{ID: "candidate-0003"},
			Status:    "generated",
			Score:     9999,
		},
		{
			Candidate: model.Candidate{ID: "candidate-0002"},
			Status:    "passed",
			Score:     900,
			Metrics:   model.Metrics{P95LatencyMS: 30},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0001"},
			Status:    "passed",
			Score:     900,
			Metrics:   model.Metrics{P95LatencyMS: 20},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0004"},
			Status:    "failed",
			Score:     1000,
		},
	}

	ranked := rankedResults(results)
	got := []string{
		ranked[0].Candidate.ID,
		ranked[1].Candidate.ID,
		ranked[2].Candidate.ID,
		ranked[3].Candidate.ID,
	}
	want := []string{"candidate-0001", "candidate-0002", "candidate-0004", "candidate-0003"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ranked order = %#v, want %#v", got, want)
		}
	}
}

func TestDynamicFloatFormatterPreservesSmallDifferences(t *testing.T) {
	formatter := newDynamicFloatFormatter([]float64{0.003245, 0.003246, 11.85616}, 2, 6)

	if got := formatter.format(0.003245); got != "0.003245" {
		t.Fatalf("formatted tiny value = %q, want enough precision to distinguish values", got)
	}
	if got := formatter.format(11.85616); got != "11.85616" {
		t.Fatalf("formatted larger value = %q, want shared precision in column", got)
	}
}

func TestDynamicFloatFormatterUsesScientificForVeryWideColumns(t *testing.T) {
	formatter := newDynamicFloatFormatter([]float64{0.00000012, 4_200_000}, 2, 6)

	if got := formatter.format(0.00000012); got != "1.200000e-07" {
		t.Fatalf("formatted wide-range tiny value = %q, want scientific notation", got)
	}
	if got := formatter.format(4_200_000); got != "4.200000e+06" {
		t.Fatalf("formatted wide-range large value = %q, want scientific notation", got)
	}
}

func TestDynamicFloatFormatterUsesSamePresentationWithinColumn(t *testing.T) {
	formatter := newDynamicFloatFormatter([]float64{1910, 166832, 8193865}, 0, 4)

	for _, value := range []float64{1910, 166832, 8193865} {
		if got := formatter.format(value); !strings.Contains(got, "e") {
			t.Fatalf("formatted %f as %q, want scientific notation for every value in scientific column", value, got)
		}
	}
}

func TestLeaderboardTableShowsScoringDrivers(t *testing.T) {
	results := []model.CandidateResult{
		{
			Candidate: model.Candidate{ID: "candidate-0002"},
			Status:    "passed",
			Score:     1000,
			Metrics: model.Metrics{
				P95LatencyMS:     0.001914,
				BenchmarkNsPerOp: 1910,
				MemoryPeakBytes:  208,
				CPUUserSeconds:   6,
				CPUSystemSeconds: 0.18,
			},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0001"},
			Status:    "passed",
			Score:     282.2153,
			Metrics: model.Metrics{
				P95LatencyMS:     0.16709,
				BenchmarkNsPerOp: 166832,
				MemoryPeakBytes:  32768,
				CPUUserSeconds:   5.9,
				CPUSystemSeconds: 0.1,
			},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0000-baseline", Baseline: true},
			Status:    "passed",
			Score:     0,
			Metrics: model.Metrics{
				P95LatencyMS:     8.206311,
				BenchmarkNsPerOp: 8193865,
				MemoryPeakBytes:  32976,
				CPUUserSeconds:   10,
				CPUSystemSeconds: 0.4,
			},
		},
	}

	var out bytes.Buffer
	printLeaderboardTable(&out, results)
	table := out.String()

	for _, want := range []string{"Speedup", "Memory", "Mem/Base", "Eval CPU s", "ns/op", "1.9100e+03", "1.6683e+05", "8.1939e+06", "4288x", "49.1x", "0.0063x", "0.99x", "1x", "208 B", "32 KiB"} {
		if !strings.Contains(table, want) {
			t.Fatalf("leaderboard table missing %q:\n%s", want, table)
		}
	}
	if strings.Contains(table, "Ext Calls") {
		t.Fatalf("leaderboard table should omit external call column when all counts are zero:\n%s", table)
	}
	if !strings.Contains(table, "candidate-0000-baseline") || !strings.Contains(table, "  0 ") {
		t.Fatalf("leaderboard table should show zero score for baseline:\n%s", table)
	}
}

func TestLeaderboardTableShowsExternalCallsWhenPresent(t *testing.T) {
	results := []model.CandidateResult{
		{
			Candidate: model.Candidate{ID: "candidate-0000-baseline", Baseline: true},
			Status:    "passed",
			Metrics: model.Metrics{
				P95LatencyMS:     10,
				BenchmarkNsPerOp: 10_000_000,
				MemoryPeakBytes:  1024,
			},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0001"},
			Status:    "passed",
			Metrics: model.Metrics{
				P95LatencyMS:      5,
				BenchmarkNsPerOp:  5_000_000,
				MemoryPeakBytes:   512,
				ExternalCallCount: 3,
			},
		},
	}

	var out bytes.Buffer
	printLeaderboardTable(&out, results)
	table := out.String()

	for _, want := range []string{"Ext Calls", "candidate-0000-baseline", "candidate-0001", "  0", "  3"} {
		if !strings.Contains(table, want) {
			t.Fatalf("leaderboard table missing %q:\n%s", want, table)
		}
	}
}

func TestLeaderboardExternalCallCountFallsBackToTraceCount(t *testing.T) {
	result := model.CandidateResult{
		Metrics:  model.Metrics{ExternalCallCount: 0},
		External: model.ExternalCallTrace{RequestCount: 4},
	}

	if got := leaderboardExternalCallCount(result); got != 4 {
		t.Fatalf("external call count = %d, want trace request count fallback", got)
	}
}

func TestFormatMultiplier(t *testing.T) {
	for _, tt := range []struct {
		name     string
		baseline float64
		value    float64
		want     string
	}{
		{name: "same", baseline: 100, value: 100, want: "1x"},
		{name: "large improvement", baseline: 32976, value: 208, want: "159x"},
		{name: "small improvement", baseline: 32976, value: 32768, want: "1.01x"},
		{name: "regression", baseline: 100, value: 200, want: "0.50x"},
		{name: "missing", baseline: 0, value: 200, want: "-"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatMultiplier(tt.baseline, tt.value); got != tt.want {
				t.Fatalf("formatMultiplier(%f, %f) = %q, want %q", tt.baseline, tt.value, got, tt.want)
			}
		})
	}
}

func TestFormatRelativeUsage(t *testing.T) {
	for _, tt := range []struct {
		name     string
		value    float64
		baseline float64
		want     string
	}{
		{name: "same", value: 100, baseline: 100, want: "1x"},
		{name: "much less", value: 208, baseline: 32976, want: "0.0063x"},
		{name: "slightly less", value: 32768, baseline: 32976, want: "0.99x"},
		{name: "regression", value: 200, baseline: 100, want: "2.00x"},
		{name: "missing", value: 200, baseline: 0, want: "-"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRelativeUsage(tt.value, tt.baseline); got != tt.want {
				t.Fatalf("formatRelativeUsage(%f, %f) = %q, want %q", tt.value, tt.baseline, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	for _, tt := range []struct {
		value int64
		want  string
	}{
		{value: 0, want: "-"},
		{value: 208, want: "208 B"},
		{value: 32768, want: "32 KiB"},
		{value: 1048576, want: "1 MiB"},
	} {
		if got := formatBytes(tt.value); got != tt.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}
