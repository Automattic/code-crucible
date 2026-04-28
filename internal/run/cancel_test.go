package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

func TestMarkCanceledMarksOnlyCancelableEvaluationCandidates(t *testing.T) {
	projectDir := t.TempDir()
	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
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
	board.Results = append(board.Results, model.CandidateResult{
		Candidate: model.Candidate{ID: "candidate-0001"},
		Status:    model.CandidateStatusPassed,
		Score:     1000,
	})
	if err := archive.SaveJSON(boardPath, board); err != nil {
		t.Fatal(err)
	}

	event, err := MarkCanceled(CancellationOptions{
		ProjectDir: projectDir,
		RunID:      created.ID,
		Action:     "evaluate",
		Reason:     "user canceled test evaluation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(event.UpdatedCandidates) != 1 || event.UpdatedCandidates[0] != "candidate-0000-baseline" {
		t.Fatalf("UpdatedCandidates = %#v, want baseline only", event.UpdatedCandidates)
	}
	if len(event.SkippedCandidates) != 1 || event.SkippedCandidates[0] != "candidate-0001" {
		t.Fatalf("SkippedCandidates = %#v, want passed candidate skipped", event.SkippedCandidates)
	}
	if _, err := os.Stat(event.EventPath); err != nil {
		t.Fatalf("event path missing: %v", err)
	}
	eventData, err := os.ReadFile(event.EventPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(eventData), `"action":"evaluate"`) {
		t.Fatalf("event file did not record action:\n%s", string(eventData))
	}

	updated, err := archive.LoadLeaderboard(boardPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Results[0].Status != model.CandidateStatusCanceled {
		t.Fatalf("baseline status = %q, want canceled", updated.Results[0].Status)
	}
	if updated.Results[0].ScoreExplanation == nil || updated.Results[0].ScoreExplanation.Scoreable {
		t.Fatalf("baseline score explanation = %#v, want unscoreable", updated.Results[0].ScoreExplanation)
	}
	if updated.Results[1].Status != model.CandidateStatusPassed || updated.Results[1].Score != 1000 {
		t.Fatalf("passed candidate was modified: %#v", updated.Results[1])
	}
}

func TestEvaluateCandidatesHonorsCanceledContext(t *testing.T) {
	projectDir := t.TempDir()
	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := EvaluateCandidates(EvaluationOptions{
		Context:    ctx,
		ProjectDir: projectDir,
		RunID:      created.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Status != model.CandidateStatusCanceled {
		t.Fatalf("evaluation results = %#v, want canceled baseline", report.Results)
	}
	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if board.Results[0].Status != model.CandidateStatusCanceled {
		t.Fatalf("leaderboard status = %q, want canceled", board.Results[0].Status)
	}
}

func TestMarkCanceledGenerateAdoptsValidArtifactsAsCanceled(t *testing.T) {
	projectDir := t.TempDir()
	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeCandidateArtifact(t, filepath.Join(created.RunDir, "round-0001", "candidate-0001"), model.Candidate{
		ID:         "candidate-0001",
		Name:       "generated before cancel",
		Round:      1,
		ParentIDs:  []string{"candidate-0000-baseline"},
		Agent:      "codex",
		SourcePath: "src",
	})
	if err := os.MkdirAll(filepath.Join(created.RunDir, "round-0001", "candidate-0002", "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	event, err := MarkCanceled(CancellationOptions{
		ProjectDir: projectDir,
		RunID:      created.ID,
		Action:     "generate",
		Reason:     "user canceled generation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(event.UpdatedCandidates) != 1 || event.UpdatedCandidates[0] != "candidate-0001" {
		t.Fatalf("UpdatedCandidates = %#v, want valid generated candidate", event.UpdatedCandidates)
	}
	if len(event.PartialCandidates) != 1 || !strings.Contains(event.PartialCandidates[0], "candidate-0002") {
		t.Fatalf("PartialCandidates = %#v, want partial candidate-0002", event.PartialCandidates)
	}
	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, result := range board.Results {
		if result.Candidate.ID != "candidate-0001" {
			continue
		}
		found = true
		if result.Status != model.CandidateStatusCanceled {
			t.Fatalf("candidate-0001 status = %q, want canceled", result.Status)
		}
		if result.ScoreExplanation == nil || result.ScoreExplanation.Scoreable {
			t.Fatalf("candidate-0001 score explanation = %#v, want unscoreable", result.ScoreExplanation)
		}
	}
	if !found {
		t.Fatalf("leaderboard did not include canceled generated candidate: %#v", board.Results)
	}
}

func TestMarkCanceledCanTargetOneCandidate(t *testing.T) {
	projectDir := t.TempDir()
	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := MarkCanceled(CancellationOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		Action:      "evaluate",
		CandidateID: "candidate-0000-baseline",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(event.UpdatedCandidates) != 1 || event.UpdatedCandidates[0] != "candidate-0000-baseline" {
		t.Fatalf("UpdatedCandidates = %#v, want targeted baseline", event.UpdatedCandidates)
	}
}
