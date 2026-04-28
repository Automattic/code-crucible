package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/external"
	"github.com/Automattic/code-crucible/internal/model"
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

	cfg, err := archive.LoadRunConfig(filepath.Join(created.RunDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"project_dir":    cfg.ProjectDir,
		"run_dir":        cfg.RunDir,
		"round_dir":      cfg.RoundDir,
		"interface_docs": cfg.InterfaceDocs,
		"prompt_path":    cfg.PromptPath,
	} {
		if filepath.IsAbs(filepath.FromSlash(value)) {
			t.Fatalf("%s = %q, want project-relative archive path", name, value)
		}
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Results) != 1 {
		t.Fatalf("leaderboard results = %d, want baseline", len(board.Results))
	}
	if filepath.IsAbs(filepath.FromSlash(board.Results[0].Candidate.SourcePath)) {
		t.Fatalf("baseline source_path = %q, want project-relative path", board.Results[0].Candidate.SourcePath)
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

func TestCreateRunCopiesEvaluatorScript(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := "#!/usr/bin/env bash\nprintf 'custom evaluator\\n'\n"
	if err := os.WriteFile(filepath.Join(projectDir, "custom-evaluator.sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Create(Options{
		ProjectDir:      projectDir,
		Optimize:        "make ranking faster",
		TargetPath:      "internal/search/rank.go",
		Variants:        2,
		Rounds:          1,
		ExternalMode:    "deny",
		EvaluatorScript: "custom-evaluator.sh",
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	evaluatorPath := filepath.Join(created.RunDir, "evaluator", "evaluator.sh")
	copied, err := os.ReadFile(evaluatorPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != script {
		t.Fatalf("evaluator script was not copied correctly")
	}
	info, err := os.Stat(evaluatorPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("evaluator script mode = %v, want 0755", info.Mode().Perm())
	}

	cfg, err := archive.LoadRunConfig(filepath.Join(created.RunDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EvaluatorScript != "custom-evaluator.sh" {
		t.Fatalf("EvaluatorScript = %q, want custom-evaluator.sh", cfg.EvaluatorScript)
	}
}

func TestCreateRunArchivesHTTPFixtureTemplateForReplay(t *testing.T) {
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
		Variants:     1,
		ExternalMode: "replay",
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := archive.LoadRunConfig(filepath.Join(created.RunDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantFixtures := filepath.Join(created.RunDir, "external", external.HTTPFixturesName)
	if cfg.External.Fixtures != archive.ProjectRelativePath(projectDir, wantFixtures) {
		t.Fatalf("fixtures path = %q, want %q", cfg.External.Fixtures, archive.ProjectRelativePath(projectDir, wantFixtures))
	}
	fixtures, err := external.LoadHTTPFixtureSet(wantFixtures)
	if err != nil {
		t.Fatal(err)
	}
	if fixtures.Version != external.HTTPFixtureVersion || len(fixtures.Fixtures) != 0 {
		t.Fatalf("fixtures = %#v, want empty fixture template", fixtures)
	}
	if _, err := os.Stat(filepath.Join(created.RunDir, "external", external.MockGatewayName)); err != nil {
		t.Fatalf("mock gateway missing: %v", err)
	}
}

func TestCreateRunCopiesHTTPFixturesIntoArchive(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceFixtures := filepath.Join(projectDir, "fixtures.json")
	if err := external.SaveHTTPFixtureSet(sourceFixtures, external.HTTPFixtureSet{
		Version: external.HTTPFixtureVersion,
		Fixtures: []external.HTTPFixture{
			{
				ID: "checkout",
				Request: external.HTTPFixtureRequest{
					Method: "POST",
					URL:    "https://api.example.com/checkout",
				},
				Response: external.HTTPFixtureResponse{
					Status: 201,
					Body:   `{"ok":true}`,
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "make checkout deterministic",
		TargetPath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "mock",
		Fixtures:     filepath.Base(sourceFixtures),
	})
	if err != nil {
		t.Fatal(err)
	}

	policyPath := filepath.Join(created.RunDir, "external", "policy.json")
	data, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	var policy model.ExternalPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	fixtures, err := external.LoadHTTPFixtureSet(archive.ProjectPath(projectDir, policy.Fixtures))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Fixtures) != 1 || fixtures.Fixtures[0].ID != "checkout" {
		t.Fatalf("fixtures = %#v, want archived checkout fixture", fixtures)
	}
}
