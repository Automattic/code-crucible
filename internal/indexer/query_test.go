package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/run"
)

func TestQueryRunsAndCandidates(t *testing.T) {
	projectDir := t.TempDir()
	created := createIndexedRunFixture(t, projectDir)

	if _, err := Rebuild(Options{ProjectDir: projectDir}); err != nil {
		t.Fatal(err)
	}

	runs, err := QueryRuns(QueryOptions{ProjectDir: projectDir})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].ID != created.ID || runs[0].Candidates != 3 || runs[0].Passed != 2 || runs[0].Failed != 1 {
		t.Fatalf("run summary = %#v", runs[0])
	}
	if runs[0].BestCandidate != "candidate-0001" || runs[0].BestScore != 1000 {
		t.Fatalf("best summary = %q score %f", runs[0].BestCandidate, runs[0].BestScore)
	}

	candidates, err := QueryCandidates(QueryOptions{ProjectDir: projectDir, RunID: created.ID, Status: "passed", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(candidates))
	}
	if candidates[0].ID != "candidate-0001" || candidates[0].RunID != created.ID || candidates[0].PrimaryMetricMS != 4 || candidates[0].MemoryMetricBytes != 2048 {
		t.Fatalf("candidate summary = %#v", candidates[0])
	}
}

func TestQueryMissingIndexExplainsRefreshStep(t *testing.T) {
	projectDir := t.TempDir()

	if _, err := QueryRuns(QueryOptions{ProjectDir: projectDir}); err == nil || !strings.Contains(err.Error(), "run crucible index first") {
		t.Fatalf("QueryRuns error = %v, want missing index guidance", err)
	}
}

func createIndexedRunFixture(t *testing.T, projectDir string) *run.CreatedRun {
	t.Helper()
	sourceDir := filepath.Join(projectDir, "ranking")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package ranking\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		TargetPath:   "ranking/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}

	boardPath := filepath.Join(created.RunDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(boardPath)
	if err != nil {
		t.Fatal(err)
	}
	board.Results[0].Status = "passed"
	board.Results[0].Score = 750
	board.Results[0].Metrics = model.Metrics{
		P95LatencyMS:     8,
		BenchmarkNsPerOp: 8_000_000,
		MemoryPeakBytes:  4096,
	}
	board.Results[0].Verdict = passedVerdict()
	board.Results = append(board.Results,
		model.CandidateResult{
			Candidate: model.Candidate{
				ID:         "candidate-0001",
				Name:       "faster ranking",
				Round:      1,
				ParentIDs:  []string{"candidate-0000-baseline"},
				Agent:      "codex",
				Model:      "test-model",
				SourcePath: "round-0001/candidate-0001/src",
				CreatedAt:  time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
			},
			Metrics: model.Metrics{
				P95LatencyMS:      4,
				BenchmarkNsPerOp:  4_000_000,
				MemoryPeakBytes:   2048,
				ExternalCallCount: 2,
			},
			External: model.ExternalCallTrace{
				Mode: model.ExternalModeDeny,
				PolicyEnforcement: &model.ExternalPolicyEnforcement{
					Mode:   model.ExternalModeDeny,
					Status: "passed",
				},
			},
			Verdict: passedVerdict(),
			Score:   1000,
			ScoreExplanation: &model.ScoreExplanation{
				Scoreable: true,
			},
			Status: "passed",
		},
		model.CandidateResult{
			Candidate: model.Candidate{
				ID:         "candidate-0002",
				Name:       "broken ranking",
				Round:      1,
				Agent:      "codex",
				SourcePath: "round-0001/candidate-0002/src",
				CreatedAt:  time.Date(2026, 4, 27, 12, 10, 0, 0, time.UTC),
			},
			Metrics: model.Metrics{
				P95LatencyMS:     2,
				BenchmarkNsPerOp: 2_000_000,
			},
			Verdict: model.Verdict{
				CorrectnessPassed:    false,
				BenchmarkPassed:      true,
				ExternalPolicyPassed: true,
			},
			Status: "failed",
		},
	)
	if err := archive.SaveJSON(boardPath, board); err != nil {
		t.Fatal(err)
	}
	return created
}

func passedVerdict() model.Verdict {
	return model.Verdict{
		CorrectnessPassed:    true,
		BenchmarkPassed:      true,
		ExternalPolicyPassed: true,
	}
}
