package evaluator

import (
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/discovery"
)

func TestDefaultScriptWithAgentPlanIncludesDiscoveryGuidance(t *testing.T) {
	script := DefaultScriptWithAgentPlan("", &discovery.AgentPlan{
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
		"Metric: p95 latency",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}
