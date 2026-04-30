package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
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
	view := newTUIDashboardModel(data, "").View()
	for _, want := range []string{
		"Code Crucible",
		"make ranking faster",
		"candidate-0001",
		"Candidate Detail",
		"[S] Start new tournament",
		"[G] Create more candidates",
		"[T] Test & score candidates",
		"[C] Continue tournament",
		"[E] Export report",
		"[?] Advanced tools",
		"[R] Review selected candidate (candidate-0001)",
		"[A] Apply selected candidate (candidate-0001)",
		"[S] start, [R] review, [A] apply",
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
	}, "")
	dashboard.selected = 0
	dashboard.configureBubbles()

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

func TestTUIDashboardDefaultsToFirstNonBaselinePassedCandidate(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{
		Config: model.RunConfig{ID: "run-1"},
		Results: []model.CandidateResult{
			{
				Candidate: model.Candidate{ID: "candidate-0000-baseline", Baseline: true},
				Status:    model.CandidateStatusPassed,
				Score:     100,
			},
			{
				Candidate: model.Candidate{ID: "candidate-0001"},
				Status:    model.CandidateStatusPassed,
				Score:     95,
			},
		},
	}, "")

	if dashboard.selected != 1 {
		t.Fatalf("selected = %d, want first non-baseline passed candidate", dashboard.selected)
	}
	if got := dashboard.selectedCandidateID(); got != "candidate-0001" {
		t.Fatalf("selected candidate = %q, want candidate-0001", got)
	}
}

func TestTUIReviewKeyRunsSelectedCandidateDirectly(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{
		ProjectDir: "/tmp/code crucible fixture",
		Config: model.RunConfig{
			ID: "run-1",
		},
		Results: []model.CandidateResult{
			{
				Candidate: model.Candidate{ID: "candidate-0000-baseline", Baseline: true},
				Status:    model.CandidateStatusPassed,
			},
			{
				Candidate: model.Candidate{ID: "candidate-0001"},
				Status:    model.CandidateStatusPassed,
			},
		},
	}, "")

	updated, cmd := dashboard.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	running := updated.(tuiDashboardModel)
	if cmd == nil {
		t.Fatal("review key did not start an action")
	}
	if !running.busy {
		t.Fatal("review key did not enter busy action state")
	}
	for _, want := range []string{"crucible inspect", "--run run-1", "candidate-0001"} {
		if !strings.Contains(running.actionCmd, want) {
			t.Fatalf("review command did not contain %q:\n%s", want, running.actionCmd)
		}
	}
}

func TestTUIApplyKeyOpensSelectedCandidateForm(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{
		ProjectDir: "/tmp/code crucible fixture",
		Config:     model.RunConfig{ID: "run-1"},
		Results: []model.CandidateResult{
			{
				Candidate: model.Candidate{ID: "candidate-0001"},
				Status:    model.CandidateStatusPassed,
			},
		},
	}, "")

	updated, _ := dashboard.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	form := updated.(tuiDashboardModel)
	if form.mode != tuiModeForm {
		t.Fatalf("mode after apply key = %v, want form", form.mode)
	}
	if form.form.Title != "Apply Selected Candidate" {
		t.Fatalf("form title = %q, want apply form", form.form.Title)
	}
	if got := form.form.value("candidate"); got != "candidate-0001" {
		t.Fatalf("candidate value = %q, want selected candidate", got)
	}
}

func TestTUIStartsWithoutExistingRun(t *testing.T) {
	projectDir := t.TempDir()
	data, message, err := loadInitialTUIDashboard(projectDir, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if data.Config.ID != "" {
		t.Fatalf("initial run ID = %q, want empty", data.Config.ID)
	}
	view := newTUIDashboardModel(data, message).View()
	for _, want := range []string{"No run is loaded yet", "[S] Start tournament:", "[?] Advanced:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty dashboard did not contain %q:\n%s", want, view)
		}
	}
}

func TestTUIAdvancedMenuOpensSelectedAction(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{
		Config: model.RunConfig{ID: "run-1"},
	}, "")

	updated, _ := dashboard.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	menu := updated.(tuiDashboardModel)
	if menu.mode != tuiModeAdvanced {
		t.Fatalf("mode after ? = %v, want advanced", menu.mode)
	}
	view := menu.View()
	for _, want := range []string{"Advanced", "Create test harness", "Import generated candidates", "Refresh archive index"} {
		if !strings.Contains(view, want) {
			t.Fatalf("advanced view did not contain %q:\n%s", want, view)
		}
	}

	updated, _ = menu.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	menu = updated.(tuiDashboardModel)
	updated, _ = menu.Update(tea.KeyMsg{Type: tea.KeyEnter})
	form := updated.(tuiDashboardModel)
	if form.mode != tuiModeForm {
		t.Fatalf("mode after advanced enter = %v, want form", form.mode)
	}
	if form.form.Title != "Import Generated Candidates" {
		t.Fatalf("advanced form title = %q, want import generated candidates", form.form.Title)
	}
}

func TestTUIFormsBuildCommandPreviews(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{
		ProjectDir: "/tmp/code crucible fixture",
		Config: model.RunConfig{
			ID:       "run-1",
			Agent:    "codex",
			Variants: 3,
		},
	}, "")

	dashboard.openForm(tuiActionEvaluate)
	setTUIFormValue(&dashboard.form, "candidate", "candidate-0001")
	setTUIFormValue(&dashboard.form, "jobs", "2")
	preview := dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{
		"crucible evaluate",
		"--project-dir '/tmp/code crucible fixture'",
		"--run run-1",
		"--candidate candidate-0001",
		"--jobs 2",
	} {
		if !strings.Contains(preview, want) {
			t.Fatalf("evaluate preview did not contain %q:\n%s", want, preview)
		}
	}

	dashboard.openForm(tuiActionRun)
	setTUIFormValue(&dashboard.form, "optimize", "make search faster")
	preview = dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{"crucible run", "--auto", "--variants 3", "--generate", "--evaluate", "'make search faster'"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("run preview did not contain %q:\n%s", want, preview)
		}
	}

	dashboard.openForm(tuiActionEvaluator)
	setTUIFormValue(&dashboard.form, "validation_timeout", "45s")
	preview = dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{
		"crucible evaluator generate",
		"--project-dir '/tmp/code crucible fixture'",
		"--run run-1",
		"--agent codex",
		"--validation-timeout 45s",
	} {
		if !strings.Contains(preview, want) {
			t.Fatalf("evaluator preview did not contain %q:\n%s", want, preview)
		}
	}

	dashboard.openForm(tuiActionAdopt)
	setTUIFormValue(&dashboard.form, "model", "gpt-test")
	preview = dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{"crucible adopt", "--run run-1", "--model gpt-test"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("adopt preview did not contain %q:\n%s", want, preview)
		}
	}

	dashboard.openForm(tuiActionPromote)
	setTUIFormValue(&dashboard.form, "candidate", "candidate-0001")
	setTUIFormValue(&dashboard.form, "dry_run", "true")
	preview = dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{"crucible promote", "--run run-1", "--candidate candidate-0001", "--dry-run"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("promote preview did not contain %q:\n%s", want, preview)
		}
	}

	dashboard.openForm(tuiActionNextRound)
	setTUIFormValue(&dashboard.form, "parents", "2")
	preview = dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{"crucible next-round", "--run run-1", "--parents 2"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("next-round preview did not contain %q:\n%s", want, preview)
		}
	}

	dashboard.openForm(tuiActionEvolve)
	setTUIFormValue(&dashboard.form, "rounds", "2")
	setTUIFormValue(&dashboard.form, "parents", "2")
	preview = dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{"crucible evolve", "--run run-1", "--rounds 2", "--parents 2", "--agent codex"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("evolve preview did not contain %q:\n%s", want, preview)
		}
	}

	dashboard.openForm(tuiActionIndex)
	preview = dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{"crucible index", "--project-dir '/tmp/code crucible fixture'", "--run run-1"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("index preview did not contain %q:\n%s", want, preview)
		}
	}

	dashboard.openForm(tuiActionInspect)
	setTUIFormValue(&dashboard.form, "candidate", "candidate-0001")
	preview = dashboard.form.commandPreview(dashboard.data.ProjectDir, dashboard.data.Config.ID)
	for _, want := range []string{"crucible inspect", "--run run-1", "candidate-0001"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("inspect preview did not contain %q:\n%s", want, preview)
		}
	}
}

func TestTUIFormEditingAcceptsSpacesAndBackspace(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{}, "")
	dashboard.openForm(tuiActionRun)

	updated, _ := dashboard.updateForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	dashboard = updated.(tuiDashboardModel)
	updated, _ = dashboard.updateForm(tea.KeyMsg{Type: tea.KeySpace})
	dashboard = updated.(tuiDashboardModel)
	updated, _ = dashboard.updateForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	dashboard = updated.(tuiDashboardModel)
	if got := dashboard.form.value("optimize"); got != "m x" {
		t.Fatalf("form value = %q, want space-preserving input", got)
	}

	updated, _ = dashboard.updateForm(tea.KeyMsg{Type: tea.KeyBackspace})
	dashboard = updated.(tuiDashboardModel)
	if got := dashboard.form.value("optimize"); got != "m" {
		t.Fatalf("form value after backspace = %q, want trimmed m", got)
	}
}

func TestTUIActionProgressViewShowsElapsedAndSelectedOptions(t *testing.T) {
	projectDir := t.TempDir()
	dashboard := newTUIDashboardModel(tuiDashboardData{
		ProjectDir: projectDir,
		Config: model.RunConfig{
			ID:       "run-1",
			Variants: 1,
		},
	}, "")
	dashboard.openForm(tuiActionGenerate)

	updated, cmd := dashboard.submitForm()
	if cmd == nil {
		t.Fatal("submitForm did not return an async command")
	}
	running := updated.(tuiDashboardModel)
	running.actionStart = time.Now().Add(-90 * time.Second)
	view := running.View()
	for _, want := range []string{
		"Running Create Candidates",
		"Elapsed: 1m30s",
		"Selected Options",
		"Tournament: run-1",
		"The dashboard will refresh when this finishes. Press h there for command history.",
		"Keys: c or esc request cancellation",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("progress view did not contain %q:\n%s", want, view)
		}
	}
}

func TestTUICommandHistoryShowsOptionsBeforeCommand(t *testing.T) {
	projectDir := t.TempDir()
	dashboard := newTUIDashboardModel(tuiDashboardData{
		ProjectDir: projectDir,
		Config: model.RunConfig{
			ID:       "run-1",
			Agent:    "codex",
			Variants: 2,
		},
	}, "")
	dashboard.openForm(tuiActionRun)
	setTUIFormValue(&dashboard.form, "optimize", "make ranking faster")
	setTUIFormValue(&dashboard.form, "generate", "true")

	updated, cmd := dashboard.submitForm()
	if cmd == nil {
		t.Fatal("submitForm did not return an async command")
	}
	running := updated.(tuiDashboardModel)
	if len(running.history) != 1 {
		t.Fatalf("history entries = %d, want 1", len(running.history))
	}

	updated, _ = running.Update(tuiActionDoneMsg{
		Title:      "Start Tournament",
		Code:       0,
		Stdout:     "created run\n",
		Data:       running.data,
		History:    0,
		FinishedAt: running.history[0].StartedAt.Add(2 * time.Second),
	})
	done := updated.(tuiDashboardModel)
	if done.mode != tuiModeDashboard {
		t.Fatalf("mode after action done = %v, want dashboard", done.mode)
	}

	updated, _ = done.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	history := updated.(tuiDashboardModel)
	view := history.View()
	for _, want := range []string{
		"Command History",
		"Start Tournament  (complete)",
		"Options",
		"What do you want to improve?: make ranking faster",
		"  Create candidate code: true",
		"  Test and score candidates: true",
		"Command",
		"crucible run",
		"--generate",
		"--evaluate",
		"Captured output: stdout 12 B, stderr 0 B",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("history view did not contain %q:\n%s", want, view)
		}
	}
	optionsIndex := strings.Index(view, "Options")
	commandIndex := -1
	if optionsIndex >= 0 {
		if offset := strings.Index(view[optionsIndex:], "Command"); offset >= 0 {
			commandIndex = optionsIndex + offset
		}
	}
	if optionsIndex < 0 || commandIndex < 0 || optionsIndex > commandIndex {
		t.Fatalf("history did not list options before command:\n%s", view)
	}
}

func TestTUISpinnerTickContinuesWhileBusy(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{}, "")
	dashboard.busy = true
	dashboard.actionTitle = "Test & Score Candidates"

	updated, cmd := dashboard.Update(dashboard.spinner.Tick())
	running := updated.(tuiDashboardModel)
	if !running.busy {
		t.Fatal("spinner tick cleared busy state")
	}
	if cmd == nil {
		t.Fatal("spinner tick did not schedule the next tick")
	}
}

func TestTUIBusyCancelRequestsCancellation(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{}, "")
	dashboard.busy = true
	dashboard.actionTitle = "Test & Score Candidates"
	canceled := false
	dashboard.actionCancel = func() { canceled = true }

	updated, _ := dashboard.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	running := updated.(tuiDashboardModel)
	if !canceled {
		t.Fatal("cancel function was not called")
	}
	if !running.canceling {
		t.Fatal("model did not enter canceling state")
	}
	if !strings.Contains(running.View(), "Cancel requested") {
		t.Fatalf("progress view did not show cancel request:\n%s", running.View())
	}
}

func TestTUIActionDoneCanceledShowsArtifactUpdate(t *testing.T) {
	dashboard := newTUIDashboardModel(tuiDashboardData{
		Config: model.RunConfig{ID: "run-1"},
	}, "")
	dashboard.openForm(tuiActionEvaluate)
	updated, cmd := dashboard.submitForm()
	if cmd == nil {
		t.Fatal("submitForm did not return an async command")
	}
	running := updated.(tuiDashboardModel)
	updated, _ = running.Update(tuiActionDoneMsg{
		Title:    "Test & Score Candidates",
		Canceled: true,
		CancelEvent: &run.CancellationEvent{
			EventPath:         "/tmp/run/events/cancellations.jsonl",
			UpdatedCandidates: []string{"candidate-0001"},
		},
		History:    0,
		FinishedAt: running.history[0].StartedAt.Add(time.Second),
	})
	done := updated.(tuiDashboardModel)
	if done.mode != tuiModeDashboard {
		t.Fatalf("mode after cancel = %v, want dashboard", done.mode)
	}
	view := done.commandHistoryContent()
	for _, want := range []string{
		"Test & Score Candidates  (canceled)",
		"candidate-0001",
		"Cancellation event: /tmp/run/events/cancellations.jsonl",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("canceled result view did not contain %q:\n%s", want, view)
		}
	}
}

func TestTUIRunFormCreatesRun(t *testing.T) {
	projectDir := t.TempDir()
	form := tuiForm{
		Action: tuiActionRun,
		Title:  "Create Run",
		Fields: []tuiFormField{
			{Name: "optimize", Label: "Optimization request", Value: "make ranking faster", Required: true},
			{Name: "variants", Label: "Candidates to create", Value: "2"},
			{Name: "generate", Label: "Generate now", Value: "false"},
		},
	}

	var stdout, stderr bytes.Buffer
	code := runTUIFormAction(WorkflowController{
		ProjectDir: projectDir,
		Stdout:     &stdout,
		Stderr:     &stderr,
	}, form)
	if code != 0 {
		t.Fatalf("run form returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Created run") {
		t.Fatalf("stdout did not include run creation:\n%s", stdout.String())
	}
	if _, err := loadTUIDashboard(projectDir, "latest"); err != nil {
		t.Fatalf("new run dashboard load failed: %v", err)
	}
}

func TestTUIAutoRunFormCreatesBaselineResult(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	providerScript := filepath.Join(t.TempDir(), "provider.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail
prompt="$(cat)"
cat > "$CRUCIBLE_RUN_DIR/evaluator/evaluator.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
metrics_out="${3:?metrics path required}"
verdict_out="${4:?verdict path required}"
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 2,
  "p95_latency_ms": 3,
  "memory_peak_bytes": 2048
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
SH
bt=$'\140'
round_dir="$(awk -v bt="$bt" 'index($0, "Current round directory:") { split($0, a, bt); print a[2]; exit }' <<< "$prompt")"
candidate="$(awk -v bt="$bt" 'index($0, "Create candidates starting at") { split($0, a, bt); print a[2]; exit }' <<< "$prompt")"
if [[ -n "$round_dir" && -n "$candidate" ]]; then
  mkdir -p "$round_dir/$candidate/src"
  printf 'package search\n\nfunc Rank() int { return 2 }\n' > "$round_dir/$candidate/src/rank.go"
  printf '# Candidate\n' > "$round_dir/$candidate/design.md"
  cat > "$round_dir/$candidate/candidate.json" <<JSON
{
  "id": "$candidate",
  "name": "generated $candidate",
  "round": 1,
  "parent_ids": ["candidate-0000-baseline"],
  "agent": "$CRUCIBLE_PROVIDER_NAME",
  "source_path": "src",
  "baseline": false
}
JSON
fi
printf 'tui auto provider output\n'
`
	if err := os.WriteFile(providerScript, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := project.Init(projectDir, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultAgent = "custom"
	cfg.AgentProviders = map[string]agent.ProviderDefinition{
		"custom": {
			Name:    "custom",
			Kind:    "command",
			Command: []string{providerScript},
			Capabilities: agent.ProviderCapabilities{
				SupportsGeneration: true,
			},
		},
	}
	if err := project.Save(projectDir, cfg); err != nil {
		t.Fatal(err)
	}

	dashboard := newTUIDashboardModel(tuiDashboardData{
		ProjectDir: projectDir,
		Config: model.RunConfig{
			Variants: 2,
		},
	}, "")
	form := dashboard.newForm(tuiActionRun)
	setTUIFormValue(&form, "optimize", "make rank faster")

	var stdout, stderr bytes.Buffer
	code := runTUIFormAction(WorkflowController{
		ProjectDir: projectDir,
		Stdout:     &stdout,
		Stderr:     &stderr,
	}, form)
	if code != 0 {
		t.Fatalf("auto run form returned %d, stderr: %s\nstdout:\n%s", code, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"Auto source path: internal/search/rank.go",
		"Baseline evaluated: candidate-0000-baseline",
		"custom generation complete",
		"Added: candidate-0001",
		"candidate-0000-baseline  passed",
		"candidate-0001           passed",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not include %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "tui auto provider output") {
		t.Fatalf("TUI auto run streamed provider stdout instead of archiving it:\n%s", stdout.String())
	}
	data, err := loadTUIDashboard(projectDir, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if data.Config.SourcePath != "internal/search/rank.go" {
		t.Fatalf("source path = %q, want auto-selected source", data.Config.SourcePath)
	}
	if len(data.Results) != 2 {
		t.Fatalf("results = %#v, want baseline and generated candidate", data.Results)
	}
	for _, result := range data.Results {
		if result.Status != model.CandidateStatusPassed {
			t.Fatalf("result %s status = %q, want passed", result.Candidate.ID, result.Status)
		}
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

func TestBareCommandLaunchesTUI(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	if _, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     1,
		ExternalMode: "deny",
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader("q"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("bare command returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Code Crucible") {
		t.Fatalf("stdout did not include TUI dashboard:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Use "+projectDir+" as the project directory?") {
		t.Fatalf("bare command launched prompt mode instead of TUI:\n%s", stdout.String())
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

func setTUIFormValue(form *tuiForm, name, value string) {
	for i := range form.Fields {
		if form.Fields[i].Name == name {
			form.Fields[i].Value = value
			if form.Fields[i].Input.Width > 0 {
				form.Fields[i].Input.SetValue(value)
				form.Fields[i].Input.SetCursor(len([]rune(value)))
			}
			return
		}
	}
	form.Fields = append(form.Fields, tuiFormField{Name: name, Value: value})
}
