package report

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

func TestGenerateHTMLWritesRunReport(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "ranking")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package ranking\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make <script>alert(1)</script> faster",
		SourcePath:   "ranking/rank.go",
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
		P95LatencyMS:    12,
		MemoryPeakBytes: 4096,
	}
	board.Results[0].Verdict = passedVerdict()
	board.Results = append(board.Results, model.CandidateResult{
		Candidate: model.Candidate{
			ID:         "candidate-0001",
			Name:       "fast ranking",
			Round:      1,
			Agent:      "codex",
			Model:      "test-model",
			SourcePath: ".crucible/runs/test/round-0001/candidate-0001/src",
			CreatedAt:  time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
		},
		Metrics: model.Metrics{
			P95LatencyMS:     4,
			BenchmarkNsPerOp: 4000,
			MemoryPeakBytes:  2048,
			CPUUserSeconds:   1.2,
			CPUSystemSeconds: 0.3,
		},
		Verdict: passedVerdict(),
		Score:   1000,
		Status:  "passed",
	})
	if err := archive.SaveJSON(boardPath, board); err != nil {
		t.Fatal(err)
	}

	report, err := GenerateHTML(Options{ProjectDir: projectDir, RunID: created.ID})
	if err != nil {
		t.Fatal(err)
	}
	if report.Candidates != 2 || report.Passed != 2 || report.Failed != 0 || report.Pending != 0 {
		t.Fatalf("report counts = candidates %d passed %d failed %d pending %d", report.Candidates, report.Passed, report.Failed, report.Pending)
	}
	if !strings.HasSuffix(filepath.FromSlash(report.OutputPath), filepath.Join(created.RunDir, "reports", "leaderboard.html")) {
		t.Fatalf("output path = %q, want run reports path", report.OutputPath)
	}

	data, err := os.ReadFile(filepath.FromSlash(report.OutputPath))
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{"Code Crucible Report", "candidate-0001", "fast ranking", "Speedup", "Mem/Base"} {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML report missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatalf("HTML report did not escape optimize text:\n%s", html)
	}
	if !strings.Contains(html, "make &lt;script&gt;alert(1)&lt;/script&gt; faster") {
		t.Fatalf("HTML report missing escaped optimize text:\n%s", html)
	}
}

func passedVerdict() model.Verdict {
	return model.Verdict{
		CorrectnessPassed:    true,
		BenchmarkPassed:      true,
		ExternalPolicyPassed: true,
	}
}
