package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

func TestPromoteCandidateDefaultsToBestPassingCompetitor(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "rank.go")
	if err := os.WriteFile(sourcePath, []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     2,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	addPromotionCandidate(t, projectDir, created.RunDir, "candidate-0001", "rank.go", "package search\n\nfunc Rank() int { return 2 }\n", 40)
	addPromotionCandidate(t, projectDir, created.RunDir, "candidate-0002", "rank.go", "package search\n\nfunc Rank() int { return 3 }\n", 95)

	report, err := PromoteCandidate(PromotionOptions{
		ProjectDir: projectDir,
		RunID:      created.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "return 3") {
		t.Fatalf("promoted source did not use best candidate:\n%s", string(updated))
	}
	if report.CandidateID != "candidate-0002" {
		t.Fatalf("promoted candidate = %q, want candidate-0002", report.CandidateID)
	}
	if len(report.Files) != 1 || report.Files[0].Destination != "internal/search/rank.go" {
		t.Fatalf("promotion files = %#v", report.Files)
	}
	if report.ReportPath == "" {
		t.Fatal("promotion report path was not recorded")
	}
	if _, err := os.Stat(filepath.Join(projectDir, report.ReportPath)); err != nil {
		t.Fatalf("promotion report was not written: %v", err)
	}
}

func TestPromoteCandidateDryRunDoesNotCopy(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, "rank.go")
	original := "package search\n\nfunc Rank() int { return 1 }\n"
	if err := os.WriteFile(sourcePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	addPromotionCandidate(t, projectDir, created.RunDir, "candidate-0001", "rank.go", "package search\n\nfunc Rank() int { return 2 }\n", 80)

	report, err := PromoteCandidate(PromotionOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0001",
		DryRun:      true,
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != original {
		t.Fatalf("dry run modified source:\n%s", string(updated))
	}
	if !report.DryRun || report.ReportPath != "" {
		t.Fatalf("dry-run report = %#v", report)
	}
}

func TestPromoteCandidateRejectsUnpassedByDefault(t *testing.T) {
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
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	addPromotionCandidateWithStatus(t, projectDir, created.RunDir, "candidate-0001", "rank.go", "package search\n", 80, model.CandidateStatusFailed)

	if _, err := PromoteCandidate(PromotionOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0001",
	}); err == nil {
		t.Fatal("expected unpassed promotion to fail")
	}
	if _, err := PromoteCandidate(PromotionOptions{
		ProjectDir:    projectDir,
		RunID:         created.ID,
		CandidateID:   "candidate-0001",
		AllowUnpassed: true,
	}); err != nil {
		t.Fatalf("allow-unpassed promotion failed: %v", err)
	}
}

func TestPromoteCandidateRejectsSymlinkDestination(t *testing.T) {
	projectDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(projectDir, "internal", "search")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	addPromotionCandidate(t, projectDir, created.RunDir, "candidate-0001", "rank.go", "package search\n", 80)

	if _, err := PromoteCandidate(PromotionOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0001",
	}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink promotion rejection, got %v", err)
	}
}

func TestPromoteCandidateOverlaysDirectoryTarget(t *testing.T) {
	projectDir := t.TempDir()
	targetDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "keep.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	addPromotionDirectoryCandidate(t, projectDir, created.RunDir, "candidate-0001", map[string]string{
		"rank.go":   "package search\n\nfunc Rank() int { return 9 }\n",
		"helper.go": "package search\n\nfunc Helper() {}\n",
	}, 80)

	report, err := PromoteCandidate(PromotionOptions{
		ProjectDir:  projectDir,
		RunID:       created.ID,
		CandidateID: "candidate-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 2 {
		t.Fatalf("promoted files = %d, want 2: %#v", len(report.Files), report.Files)
	}
	for _, path := range []string{"rank.go", "helper.go", "keep.go"} {
		if _, err := os.Stat(filepath.Join(targetDir, path)); err != nil {
			t.Fatalf("target file %s missing after promotion: %v", path, err)
		}
	}
	updated, err := os.ReadFile(filepath.Join(targetDir, "rank.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "return 9") {
		t.Fatalf("directory promotion did not overwrite rank.go:\n%s", string(updated))
	}
}

func addPromotionCandidate(t *testing.T, projectDir, runDir, id, fileName, body string, score float64) {
	t.Helper()
	addPromotionCandidateWithStatus(t, projectDir, runDir, id, fileName, body, score, model.CandidateStatusPassed)
}

func addPromotionCandidateWithStatus(t *testing.T, projectDir, runDir, id, fileName, body string, score float64, status string) {
	t.Helper()
	addPromotionDirectoryCandidateWithStatus(t, projectDir, runDir, id, map[string]string{fileName: body}, score, status)
}

func addPromotionDirectoryCandidate(t *testing.T, projectDir, runDir, id string, files map[string]string, score float64) {
	t.Helper()
	addPromotionDirectoryCandidateWithStatus(t, projectDir, runDir, id, files, score, model.CandidateStatusPassed)
}

func addPromotionDirectoryCandidateWithStatus(t *testing.T, projectDir, runDir, id string, files map[string]string, score float64, status string) {
	t.Helper()
	candidateSrc := filepath.Join(runDir, "round-0001", id, "src")
	if err := os.MkdirAll(candidateSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(candidateSrc, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	board, err := archive.LoadLeaderboard(filepath.Join(runDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	board.Results = append(board.Results, model.CandidateResult{
		Candidate: model.Candidate{
			ID:         id,
			Name:       id,
			Round:      1,
			ParentIDs:  []string{"candidate-0000-baseline"},
			Agent:      "codex",
			SourcePath: archive.ProjectRelativePath(projectDir, candidateSrc),
			CreatedAt:  time.Now().UTC(),
		},
		Metrics: model.Metrics{
			P95LatencyMS: score,
		},
		External: model.ExternalCallTrace{
			Mode:         model.ExternalModeDeny,
			PolicyPassed: true,
		},
		Verdict: model.Verdict{
			CorrectnessPassed:    status == model.CandidateStatusPassed,
			BenchmarkPassed:      status == model.CandidateStatusPassed,
			ExternalPolicyPassed: status == model.CandidateStatusPassed,
		},
		Score:  score,
		Status: status,
	})
	if err := archive.SaveJSON(filepath.Join(runDir, "leaderboard.json"), board); err != nil {
		t.Fatal(err)
	}
}
