package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gaarai/code-crucible/internal/archive"
	"github.com/gaarai/code-crucible/internal/model"
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
		if result.Metrics.P95LatencyMS != 40 {
			t.Fatalf("%s p95 = %f, want 40", result.Candidate.ID, result.Metrics.P95LatencyMS)
		}
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
}

func createEvaluationFixture(t *testing.T) (string, *CreatedRun) {
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
		TargetPath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "deny",
		Agent:        "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	return projectDir, created
}
