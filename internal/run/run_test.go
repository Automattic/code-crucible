package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateRunCopiesFileBaseline(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}

	source := "package search\n\nfunc Rank(scores []int) int { return len(scores) }\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		TargetPath:   "internal/search/rank.go",
		Variants:     2,
		Rounds:       1,
		Exploration:  0.25,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	copied, err := os.ReadFile(filepath.Join(created.BaselineSourceDir, "rank.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != source {
		t.Fatalf("baseline source was not copied correctly")
	}

	for _, path := range []string{
		filepath.Join(created.RunDir, "run.json"),
		filepath.Join(created.RunDir, "docs", "interfaces.md"),
		filepath.Join(created.RunDir, "external", "policy.json"),
		filepath.Join(created.RunDir, "evaluator", "evaluator.sh"),
		filepath.Join(created.RunDir, "leaderboard.json"),
		created.PromptPath,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected generated file %s: %v", path, err)
		}
	}
}

func TestCreateRunWithoutTargetPathCreatesDiscoveryPrompt(t *testing.T) {
	projectDir := t.TempDir()

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "reduce checkout external calls",
		Variants:     3,
		Rounds:       1,
		ExternalMode: "mock",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	baselineReadme, err := os.ReadFile(filepath.Join(created.BaselineSourceDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(baselineReadme), "No target path was provided") {
		t.Fatalf("baseline README did not describe target discovery")
	}

	prompt, err := os.ReadFile(created.PromptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "External Policy") {
		t.Fatalf("generation prompt did not include external policy")
	}
	if !strings.Contains(string(prompt), "Generate 3 competitor implementations") {
		t.Fatalf("generation prompt did not include requested variant count")
	}
}
