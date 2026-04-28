package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/run"
)

func TestTUIDashboardViewShowsRunCandidatesAndCommands(t *testing.T) {
	projectDir := t.TempDir()
	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     2,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	board := loadTestLeaderboard(t, created.RunDir)
	board.Results = append(board.Results, model.CandidateResult{
		Candidate: model.Candidate{
			ID:         "candidate-0001",
			Name:       "fast ranker",
			Round:      1,
			Agent:      "codex",
			SourcePath: "round-0001/candidate-0001/src",
		},
		Metrics: model.Metrics{
			P95LatencyMS:     4.5,
			BenchmarkNsPerOp: 1200,
			MemoryPeakBytes:  64 * 1024,
			CPUUserSeconds:   0.12,
		},
		External: model.ExternalCallTrace{
			Mode:         model.ExternalModeDeny,
			PolicyPassed: true,
		},
		Verdict: model.Verdict{
			CorrectnessPassed:    true,
			BenchmarkPassed:      true,
			ExternalPolicyPassed: true,
		},
		Score:  100,
		Status: "passed",
	})
	if err := archive.SaveJSON(filepath.Join(created.RunDir, "leaderboard.json"), board); err != nil {
		t.Fatal(err)
	}

	data, err := loadTUIDashboard(projectDir, "latest")
	if err != nil {
		t.Fatal(err)
	}
	view := newTUIDashboardModel(data).View()
	for _, want := range []string{
		"Code Crucible",
		"make ranking faster",
		"candidate-0001",
		"Candidate Detail",
		"Generate competitors: crucible generate --project-dir",
		"Inspect selected:     crucible inspect --project-dir",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard view did not contain %q:\n%s", want, view)
		}
	}
}

func TestTUIDashboardNavigationChangesSelectedCandidate(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{
		Config: model.RunConfig{ID: "run-1"},
		Results: []model.CandidateResult{
			{Candidate: model.Candidate{ID: "candidate-0000-baseline"}},
			{Candidate: model.Candidate{ID: "candidate-0001"}},
		},
	})

	updated, _ := dashboard.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	navigated := updated.(tuiDashboardModel)
	if navigated.selected != 1 {
		t.Fatalf("selected after j = %d, want 1", navigated.selected)
	}
	if !strings.Contains(navigated.candidateDetail(), "candidate-0001") {
		t.Fatalf("candidate detail did not follow selection:\n%s", navigated.candidateDetail())
	}

	updated, _ = navigated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	navigated = updated.(tuiDashboardModel)
	if navigated.selected != 0 {
		t.Fatalf("selected after k = %d, want 0", navigated.selected)
	}
}

func TestTUICommandRendersDashboard(t *testing.T) {
	projectDir := t.TempDir()
	if _, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     1,
		ExternalMode: "deny",
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunWithIO([]string{"tui", "--project-dir", projectDir, "--no-alt-screen"}, strings.NewReader("q"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tui returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Code Crucible") {
		t.Fatalf("stdout did not include dashboard:\n%s", stdout.String())
	}
}

func loadTestLeaderboard(t *testing.T, runDir string) *model.Leaderboard {
	t.Helper()
	board, err := archive.LoadLeaderboard(filepath.Join(runDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	return board
}
