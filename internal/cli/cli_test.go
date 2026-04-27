package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
