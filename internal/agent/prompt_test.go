package agent

import (
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/model"
)

func TestBuildGenerationPromptIncludesScoreExplanation(t *testing.T) {
	prompt := BuildGenerationPrompt(GenerationPromptRequest{
		RunConfig: model.RunConfig{
			Optimize: "optimize ranking",
			Variants: 1,
			External: model.ExternalPolicy{
				Mode: "deny",
			},
		},
		InterfaceDocPath:  "interfaces/contract.md",
		RunDir:            ".crucible/runs/run",
		RoundDir:          ".crucible/runs/run/round-0001",
		BaselineSourceDir: ".crucible/runs/run/round-0001/candidate-0000-baseline/src",
		History: []model.CandidateResult{
			{
				Candidate: model.Candidate{
					ID: "candidate-0001",
				},
				Metrics: model.Metrics{
					RuntimeMeanMS:   1,
					P95LatencyMS:    2,
					MemoryPeakBytes: 128,
				},
				External: model.ExternalCallTrace{
					RequestCount: 3,
				},
				Verdict: model.Verdict{
					CorrectnessPassed:    true,
					BenchmarkPassed:      true,
					ExternalPolicyPassed: true,
				},
				Score:  900,
				Status: "passed",
				ScoreExplanation: &model.ScoreExplanation{
					ExternalCallCount:      3,
					PrimaryPenalty:         10,
					MemoryPenalty:          2,
					ExternalCallPenalty:    75,
					ExternalLatencyPenalty: 1.5,
					ExternalCostPenalty:    0.25,
					TotalPenalty:           88.75,
				},
			},
		},
	})

	for _, want := range []string{
		"external_calls=3",
		"score_penalties={",
		"primary:10",
		"memory:2",
		"external_calls:75",
		"external_latency:1.5",
		"external_cost:0.25",
		"total:88.75",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("generation prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildGenerationPromptUsesSelectedAgentPlaceholder(t *testing.T) {
	prompt := BuildGenerationPrompt(GenerationPromptRequest{
		RunConfig: model.RunConfig{
			Optimize: "optimize ranking",
			Agent:    "custom-agent",
			Variants: 1,
			External: model.ExternalPolicy{
				Mode: "deny",
			},
		},
		InterfaceDocPath:  "interfaces/contract.md",
		RunDir:            ".crucible/runs/run",
		RoundDir:          ".crucible/runs/run/round-0001",
		BaselineSourceDir: ".crucible/runs/run/round-0001/candidate-0000-baseline/src",
	})
	if !strings.Contains(prompt, `"agent": "custom-agent"`) {
		t.Fatalf("generation prompt did not use configured agent:\n%s", prompt)
	}

	prompt = BuildGenerationPrompt(GenerationPromptRequest{
		RunConfig: model.RunConfig{
			Optimize: "optimize ranking",
			Variants: 1,
			External: model.ExternalPolicy{
				Mode: "deny",
			},
		},
		InterfaceDocPath:  "interfaces/contract.md",
		RunDir:            ".crucible/runs/run",
		RoundDir:          ".crucible/runs/run/round-0001",
		BaselineSourceDir: ".crucible/runs/run/round-0001/candidate-0000-baseline/src",
	})
	if !strings.Contains(prompt, `"agent": "selected provider name"`) {
		t.Fatalf("generation prompt did not use provider-neutral placeholder:\n%s", prompt)
	}
	if strings.Contains(prompt, `"agent": "codex"`) {
		t.Fatalf("generation prompt still hard-codes Codex:\n%s", prompt)
	}
}

func TestBuildGenerationPromptRequiresBaselineDiscoveryWhenSourcePathMissing(t *testing.T) {
	prompt := BuildGenerationPrompt(GenerationPromptRequest{
		RunConfig: model.RunConfig{
			Optimize: "reduce checkout latency",
			Variants: 2,
			External: model.ExternalPolicy{
				Mode: "deny",
			},
		},
		InterfaceDocPath:  ".crucible/runs/run/docs/interfaces.md",
		RunDir:            ".crucible/runs/run",
		RoundDir:          ".crucible/runs/run/round-0001",
		BaselineSourceDir: ".crucible/runs/run/round-0001/candidate-0000-baseline/src",
	})

	for _, want := range []string{
		"## Baseline Discovery Required",
		"No baseline source path was selected",
		"Copy the original drop-in baseline implementation into `.crucible/runs/run/round-0001/candidate-0000-baseline/src`",
		"Do not create candidate-NNNN competitors until candidate-0000-baseline/src contains the original baseline source.",
		"The only allowed baseline change is populating candidate-0000-baseline/src with original host-project source",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("generation prompt missing %q:\n%s", want, prompt)
		}
	}
}
