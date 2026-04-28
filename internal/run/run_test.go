package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/discovery"
	"github.com/Automattic/code-crucible/internal/evaluator"
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
		SourcePath:   "internal/search/rank.go",
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
	if cfg.SourcePath != "internal/search/rank.go" {
		t.Fatalf("SourcePath = %q, want internal/search/rank.go", cfg.SourcePath)
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
	runConfigRaw, err := os.ReadFile(filepath.Join(created.RunDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runConfigRaw), `"source_path": "internal/search/rank.go"`) {
		t.Fatalf("run.json did not store source_path: %s", string(runConfigRaw))
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
	if board.Results[0].Status != model.CandidateStatusNeedsEvaluator {
		t.Fatalf("baseline status = %q, want needs-evaluator", board.Results[0].Status)
	}
	if !strings.Contains(strings.Join(board.Results[0].Verdict.Notes, "\n"), "no evaluator is configured") {
		t.Fatalf("baseline notes did not explain missing evaluator: %#v", board.Results[0].Verdict.Notes)
	}
}

func TestCreateRunWithoutSourcePathCreatesDiscoveryPrompt(t *testing.T) {
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
	if !strings.Contains(string(baselineReadme), "No source path was provided") {
		t.Fatalf("baseline README did not describe source discovery")
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
	if !strings.Contains(string(prompt), "Baseline Discovery Required") {
		t.Fatalf("generation prompt did not require baseline discovery")
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
		SourcePath:      "internal/search/rank.go",
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

func TestCreateRunWithEvaluatorLeavesBaselinePending(t *testing.T) {
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
		Evaluator:    "go test ./...",
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}

	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if board.Results[0].Status != "pending" {
		t.Fatalf("baseline status = %q, want pending", board.Results[0].Status)
	}
	if !strings.Contains(strings.Join(board.Results[0].Verdict.Notes, "\n"), "not evaluated yet") {
		t.Fatalf("baseline notes did not explain pending evaluation: %#v", board.Results[0].Verdict.Notes)
	}
}

func TestCreateRunUsesAgentPlanForInterfaceAndEvaluatorScaffold(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "checkout")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pricing.go"), []byte("package checkout\n\nfunc PriceCheckout() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &discovery.AgentPlan{
		SourcePath:           "internal/checkout/pricing.go",
		SourcePathConfidence: "high",
		DropInInterface:      "PriceCheckout(cart) Money",
		Inputs:               []string{"cart fixture"},
		Outputs:              []string{"priced total"},
		EvaluatorStrategy:    []string{"golden fixture comparison", "benchmark PriceCheckout"},
		Metrics:              []string{"p95 latency", "cpu user seconds"},
		ExternalMode:         "deny",
		Notes:                []string{"keep behavior identical for discounts"},
	}
	created, err := Create(Options{
		ProjectDir:   projectDir,
		Optimize:     "reduce checkout pricing latency",
		SourcePath:   plan.SourcePath,
		Variants:     2,
		ExternalMode: "deny",
		AgentPlan:    plan,
		Clarifications: []discovery.Clarification{
			{Question: "Which percentile?", Answer: "p95"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	interfaceDoc, err := os.ReadFile(filepath.Join(created.RunDir, "docs", "interfaces.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Agent Discovery Handoff", "PriceCheckout(cart) Money", "golden fixture comparison", "Which percentile? p95"} {
		if !strings.Contains(string(interfaceDoc), want) {
			t.Fatalf("interfaces.md missing %q:\n%s", want, string(interfaceDoc))
		}
	}
	if _, err := os.Stat(filepath.Join(created.RunDir, "docs", "agent-discovery.json")); err != nil {
		t.Fatalf("agent-discovery.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(created.RunDir, "docs", "agent-discovery.md")); err != nil {
		t.Fatalf("agent-discovery.md missing: %v", err)
	}
	checksRaw, err := os.ReadFile(filepath.Join(created.RunDir, "evaluator", evaluator.ContractChecksFilename))
	if err != nil {
		t.Fatalf("contract checks missing: %v", err)
	}
	var checks evaluator.ContractChecks
	if err := json.Unmarshal(checksRaw, &checks); err != nil {
		t.Fatalf("decode contract checks: %v\n%s", err, checksRaw)
	}
	if checks.DropInInterface != "PriceCheckout(cart) Money" {
		t.Fatalf("DropInInterface = %q", checks.DropInInterface)
	}
	if len(checks.RequiredSourceExtensions) != 1 || checks.RequiredSourceExtensions[0] != ".go" {
		t.Fatalf("RequiredSourceExtensions = %#v, want .go", checks.RequiredSourceExtensions)
	}

	evaluatorScript, err := os.ReadFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Discovery-derived evaluator guidance", "PriceCheckout(cart) Money", "contract-checks.json", "Metric: p95 latency"} {
		if !strings.Contains(string(evaluatorScript), want) {
			t.Fatalf("evaluator scaffold missing %q:\n%s", want, string(evaluatorScript))
		}
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
		SourcePath:   "internal/search/rank.go",
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
		SourcePath:   "internal/search/rank.go",
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
