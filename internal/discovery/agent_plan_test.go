package discovery

import "testing"

func TestDecodeAgentPlanFromFencedJSON(t *testing.T) {
	markdown := []byte(`# Discovery

Use the checkout pricing package.

` + "```json" + `
{
  "source_path": "internal/checkout/pricing.go",
  "source_path_confidence": "high",
  "drop_in_interface": "PriceCheckout(ctx, cart)",
  "inputs": ["cart fixture"],
  "outputs": ["total price"],
  "external_mode": "mock",
  "evaluator_strategy": ["golden total tests", "benchmark PriceCheckout"],
  "metrics": ["p95 latency"],
  "clarifying_questions": [],
  "suggested_next_command": "crucible run \"reduce checkout pricing latency\" --source-path internal/checkout/pricing.go"
}
` + "```" + `
`)

	plan, err := DecodeAgentPlan(markdown)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourcePath != "internal/checkout/pricing.go" {
		t.Fatalf("SourcePath = %q", plan.SourcePath)
	}
	if plan.ExternalMode != "mock" {
		t.Fatalf("ExternalMode = %q", plan.ExternalMode)
	}
	if len(plan.EvaluatorStrategy) != 2 {
		t.Fatalf("EvaluatorStrategy length = %d", len(plan.EvaluatorStrategy))
	}
}

func TestDecodeAgentPlanRejectsMarkdownWithoutJSON(t *testing.T) {
	if _, err := DecodeAgentPlan([]byte("# Discovery\n\nNo structured handoff.")); err == nil {
		t.Fatal("DecodeAgentPlan succeeded without JSON")
	}
}

func TestDecodeAgentPlanRejectsEmptyJSON(t *testing.T) {
	if _, err := DecodeAgentPlan([]byte(`{}`)); err == nil {
		t.Fatal("DecodeAgentPlan succeeded with empty JSON")
	}
}
