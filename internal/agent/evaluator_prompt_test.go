package agent

import (
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/model"
)

func TestBuildEvaluatorPromptIncludesCacheResistantBenchmarkGuidance(t *testing.T) {
	prompt := BuildEvaluatorPrompt(EvaluatorPromptRequest{
		RunConfig: model.RunConfig{
			Optimize: "make ICO generation faster",
			External: model.ExternalPolicy{Mode: model.ExternalModeDeny},
		},
		RunDir:            ".crucible/runs/example",
		EvaluatorPath:     ".crucible/runs/example/evaluator/evaluator.sh",
		BaselineSourceDir: ".crucible/runs/example/round-0001/candidate-0000-baseline/src",
		ScratchDir:        ".crucible/runs/example/tmp/evaluator-generation",
		InterfaceDocPath:  ".crucible/runs/example/docs/interfaces.md",
	})

	for _, want := range []string{
		"## Benchmark Validity",
		"not an accidental cache hit",
		"Use varied deterministic inputs",
		"cache-warm and cache-cold metrics separately",
		"Document benchmark cache behavior",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
