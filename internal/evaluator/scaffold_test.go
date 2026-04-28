package evaluator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/discovery"
)

func TestDefaultScriptWithAgentPlanIncludesDiscoveryGuidance(t *testing.T) {
	script := DefaultScriptWithAgentPlan("", &discovery.AgentPlan{
		SourcePath:        "internal/checkout/pricing.go",
		DropInInterface:   "PriceCheckout(cart) Money",
		Inputs:            []string{"cart fixture"},
		Outputs:           []string{"priced total"},
		EvaluatorStrategy: []string{"golden fixture comparison"},
		Metrics:           []string{"p95 latency"},
	})

	for _, want := range []string{
		"Discovery-derived evaluator guidance",
		"# Drop-in interface: PriceCheckout(cart) Money",
		"# - cart fixture",
		"# - golden fixture comparison",
		"docs/agent-discovery.md",
		"contract-checks.json",
		"required_source_extensions=(\".go\")",
		"Metric: p95 latency",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestDefaultScriptWithAgentPlanRunsGeneratedContractChecks(t *testing.T) {
	runDir := t.TempDir()
	candidateDir := filepath.Join(runDir, "round-0001", "candidate-0001")
	srcDir := filepath.Join(candidateDir, "src")
	if err := os.MkdirAll(filepath.Join(runDir, "evaluator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		filepath.Join(candidateDir, "candidate.json"): `{"id":"candidate-0001","source_path":"src"}`,
		filepath.Join(candidateDir, "design.md"):      "# Candidate\n",
		filepath.Join(srcDir, "pricing.go"):           "package checkout\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	plan := &discovery.AgentPlan{
		SourcePath:        "internal/checkout/pricing.go",
		DropInInterface:   "PriceCheckout(cart) Money",
		Inputs:            []string{"cart fixture"},
		Outputs:           []string{"priced total"},
		EvaluatorStrategy: []string{"golden fixture comparison"},
		Metrics:           []string{"p95 latency"},
	}
	checks := BuildContractChecks(plan)
	data, err := MarshalContractChecks(checks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "evaluator", ContractChecksFilename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(runDir, "evaluator", "evaluator.sh")
	if err := os.WriteFile(scriptPath, []byte(DefaultScriptWithAgentPlan("", plan)), 0o755); err != nil {
		t.Fatal(err)
	}

	metricsPath := filepath.Join(candidateDir, "metrics.json")
	verdictPath := filepath.Join(candidateDir, "verdict.json")
	cmd := exec.Command("bash", scriptPath, candidateDir, runDir, metricsPath, verdictPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("evaluator script failed: %v\n%s", err, output)
	}

	var verdict struct {
		CorrectnessPassed    bool     `json:"correctness_passed"`
		BenchmarkPassed      bool     `json:"benchmark_passed"`
		ExternalPolicyPassed bool     `json:"external_policy_passed"`
		Errors               []string `json:"errors"`
	}
	verdictRaw, err := os.ReadFile(verdictPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(verdictRaw, &verdict); err != nil {
		t.Fatalf("decode verdict: %v\n%s", err, verdictRaw)
	}
	if !verdict.CorrectnessPassed {
		t.Fatalf("correctness_passed = false, errors: %#v", verdict.Errors)
	}
	if verdict.BenchmarkPassed {
		t.Fatalf("benchmark_passed = true, want generated checks to leave benchmark pending")
	}
	if !verdict.ExternalPolicyPassed {
		t.Fatalf("external_policy_passed = false, want generated checks to preserve policy state")
	}
}
