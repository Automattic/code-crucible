package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gaarai/code-crucible/internal/archive"
	"github.com/gaarai/code-crucible/internal/model"
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
