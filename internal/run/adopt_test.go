package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

func TestAdoptCandidatesAddsValidGeneratedCandidates(t *testing.T) {
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
		Variants:     2,
		ExternalMode: "deny",
		Agent:        "codex",
	})
	if err != nil {
		t.Fatal(err)
	}

	candidateDir := filepath.Join(created.RunDir, "round-0001", "candidate-0001")
	writeCandidateArtifact(t, candidateDir, model.Candidate{
		ID:         "candidate-0001",
		Name:       "branchless ranking",
		Round:      1,
		ParentIDs:  []string{"candidate-0000-baseline"},
		Agent:      "codex",
		SourcePath: "src",
		Baseline:   false,
	})

	report, err := AdoptCandidates(AdoptionOptions{
		ProjectDir: projectDir,
		RunID:      created.ID,
		Model:      "gpt-5.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 || report.Added[0] != "candidate-0001" {
		t.Fatalf("Added = %#v, want candidate-0001", report.Added)
	}
	if len(report.Invalid) != 0 {
		t.Fatalf("Invalid = %#v, want none", report.Invalid)
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Results) != 2 {
		t.Fatalf("leaderboard results = %d, want 2", len(board.Results))
	}
	result := board.Results[1]
	if result.Candidate.ID != "candidate-0001" {
		t.Fatalf("adopted candidate ID = %q", result.Candidate.ID)
	}
	if result.Candidate.Model != "gpt-5.5" {
		t.Fatalf("adopted candidate model = %q", result.Candidate.Model)
	}
	if result.Status != "generated" {
		t.Fatalf("adopted candidate status = %q", result.Status)
	}

	report, err = AdoptCandidates(AdoptionOptions{ProjectDir: projectDir, RunID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 0 || len(report.Existing) != 1 || report.Existing[0] != "candidate-0001" {
		t.Fatalf("second adoption report = %#v", report)
	}
}

func TestAdoptCandidatesReportsInvalidGeneratedCandidate(t *testing.T) {
	projectDir := t.TempDir()

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     1,
		ExternalMode: "deny",
		Agent:        "codex",
	})
	if err != nil {
		t.Fatal(err)
	}

	invalidDir := filepath.Join(created.RunDir, "round-0001", "candidate-0001")
	if err := os.MkdirAll(filepath.Join(invalidDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := AdoptCandidates(AdoptionOptions{ProjectDir: projectDir, RunID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 0 {
		t.Fatalf("Added = %#v, want none", report.Added)
	}
	if len(report.Invalid) != 1 {
		t.Fatalf("Invalid = %#v, want one issue", report.Invalid)
	}
	if report.Invalid[0].Reason != "missing design.md" {
		t.Fatalf("invalid reason = %q", report.Invalid[0].Reason)
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Results) != 1 {
		t.Fatalf("leaderboard results = %d, want only baseline", len(board.Results))
	}
}

func writeCandidateArtifact(t *testing.T, candidateDir string, candidate model.Candidate) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(candidateDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "src", "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "design.md"), []byte("# Candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(candidateDir, "candidate.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
