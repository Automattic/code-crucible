package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

func TestPrepareNextRoundCreatesRoundPrompt(t *testing.T) {
	projectDir, created := createRoundFixture(t)

	report, err := PrepareNextRound(NextRoundOptions{
		ProjectDir: projectDir,
		RunID:      created.ID,
		Parents:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.PreviousRound != 1 || report.Round != 2 {
		t.Fatalf("rounds = %d -> %d, want 1 -> 2", report.PreviousRound, report.Round)
	}
	if report.NextCandidateID != "candidate-0003" {
		t.Fatalf("next candidate = %q, want candidate-0003", report.NextCandidateID)
	}
	if len(report.ParentIDs) != 1 || report.ParentIDs[0] != "candidate-0002" {
		t.Fatalf("parents = %#v, want top candidate-0002", report.ParentIDs)
	}
	if _, err := os.Stat(filepath.Join(created.RunDir, "round-0002")); err != nil {
		t.Fatalf("round directory missing: %v", err)
	}

	cfg, err := archive.LoadRunConfig(filepath.Join(created.RunDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.FromSlash(cfg.RoundDir), filepath.Join(created.RunDir, "round-0002")) {
		t.Fatalf("RoundDir = %q, want round-0002", cfg.RoundDir)
	}
	if !strings.HasSuffix(filepath.FromSlash(cfg.PromptPath), filepath.Join(created.RunDir, "prompts", "generation-round-0002.md")) {
		t.Fatalf("PromptPath = %q, want generation-round-0002.md", cfg.PromptPath)
	}
	if cfg.Rounds != 2 {
		t.Fatalf("Rounds = %d, want 2", cfg.Rounds)
	}

	prompt, err := os.ReadFile(filepath.FromSlash(cfg.PromptPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Generate 2 competitor implementations for round 2",
		"Preferred parent candidates: `candidate-0002`",
		"starting with candidate-0003",
		`"round": 2`,
		`"parent_ids": ["candidate-0002"]`,
	} {
		if !strings.Contains(string(prompt), want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPrepareNextRoundRequiresPassedParents(t *testing.T) {
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

	_, err = PrepareNextRound(NextRoundOptions{ProjectDir: projectDir, RunID: created.ID})
	if err == nil || !strings.Contains(err.Error(), "no passed candidates") {
		t.Fatalf("error = %v, want no passed candidates", err)
	}
}

func TestAdoptCandidatesUsesActiveRound(t *testing.T) {
	projectDir, created := createRoundFixture(t)
	if _, err := PrepareNextRound(NextRoundOptions{ProjectDir: projectDir, RunID: created.ID, Parents: 1}); err != nil {
		t.Fatal(err)
	}

	candidateDir := filepath.Join(created.RunDir, "round-0002", "candidate-0003")
	writeCandidateArtifact(t, candidateDir, model.Candidate{
		ID:         "candidate-0003",
		Name:       "round two candidate",
		Round:      2,
		ParentIDs:  []string{"candidate-0002"},
		Agent:      "codex",
		SourcePath: "src",
		Baseline:   false,
	})

	report, err := AdoptCandidates(AdoptionOptions{ProjectDir: projectDir, RunID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 || report.Added[0] != "candidate-0003" {
		t.Fatalf("Added = %#v, want candidate-0003", report.Added)
	}
}

func createRoundFixture(t *testing.T) (string, *CreatedRun) {
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
		Variants:     2,
		Rounds:       1,
		ExternalMode: "deny",
		Agent:        "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	board.Results[0].Status = "passed"
	board.Results[0].Verdict = model.Verdict{
		CorrectnessPassed:    true,
		BenchmarkPassed:      true,
		ExternalPolicyPassed: true,
	}
	board.Results[0].Score = 800
	board.Results = append(board.Results,
		model.CandidateResult{
			Candidate: model.Candidate{
				ID:         "candidate-0001",
				Name:       "generated one",
				Round:      1,
				ParentIDs:  []string{"candidate-0000-baseline"},
				Agent:      "codex",
				SourcePath: filepath.ToSlash(filepath.Join(created.RunDir, "round-0001", "candidate-0001", "src")),
			},
			Status: "passed",
			Score:  900,
			Verdict: model.Verdict{
				CorrectnessPassed:    true,
				BenchmarkPassed:      true,
				ExternalPolicyPassed: true,
			},
		},
		model.CandidateResult{
			Candidate: model.Candidate{
				ID:         "candidate-0002",
				Name:       "generated two",
				Round:      1,
				ParentIDs:  []string{"candidate-0000-baseline"},
				Agent:      "codex",
				SourcePath: filepath.ToSlash(filepath.Join(created.RunDir, "round-0001", "candidate-0002", "src")),
			},
			Status: "passed",
			Score:  950,
			Verdict: model.Verdict{
				CorrectnessPassed:    true,
				BenchmarkPassed:      true,
				ExternalPolicyPassed: true,
			},
		},
	)
	if err := archive.SaveJSON(filepath.Join(created.RunDir, "leaderboard.json"), board); err != nil {
		t.Fatal(err)
	}
	return projectDir, created
}
