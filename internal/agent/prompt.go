package agent

import (
	"fmt"
	"strings"

	"github.com/Automattic/code-crucible/internal/model"
)

type GenerationPromptRequest struct {
	RunConfig         model.RunConfig
	Round             int
	NextCandidate     int
	ParentIDs         []string
	InterfaceDocPath  string
	RunDir            string
	RoundDir          string
	BaselineSourceDir string
	ScratchDir        string
	History           []model.CandidateResult
}

func BuildGenerationPrompt(req GenerationPromptRequest) string {
	var b strings.Builder
	cfg := req.RunConfig
	baselinePending := strings.TrimSpace(cfg.SourcePath) == ""
	round := req.Round
	if round <= 0 {
		round = 1
	}
	nextCandidate := req.NextCandidate
	if nextCandidate <= 0 {
		nextCandidate = 1
	}
	parentIDs := req.ParentIDs
	if len(parentIDs) == 0 {
		parentIDs = []string{"candidate-0000-baseline"}
	}
	nextCandidateID := fmt.Sprintf("candidate-%04d", nextCandidate)
	candidateAgent := strings.TrimSpace(cfg.Agent)
	if candidateAgent == "" {
		candidateAgent = "selected provider name"
	}

	fmt.Fprintf(&b, "# Code Crucible Candidate Generation Prompt\n\n")
	fmt.Fprintf(&b, "Optimization request: %s\n\n", cfg.Optimize)
	fmt.Fprintf(&b, "Generate %d competitor implementations for round %d.\n\n", cfg.Variants, round)

	fmt.Fprintf(&b, "## Workspace\n\n")
	fmt.Fprintf(&b, "- Host project directory: `%s`\n", cfg.ProjectDir)
	fmt.Fprintf(&b, "- Code Crucible run directory: `%s`\n", req.RunDir)
	fmt.Fprintf(&b, "- Current round directory: `%s`\n", req.RoundDir)
	if req.ScratchDir != "" {
		fmt.Fprintf(&b, "- Temporary verification scratch directory: `%s`\n", req.ScratchDir)
	}
	fmt.Fprintf(&b, "- Baseline source directory: `%s`\n\n", req.BaselineSourceDir)
	fmt.Fprintf(&b, "Only write generated competitor artifacts under the current round directory. If temporary verification files are needed, write them under the scratch directory. Do not modify host project source files outside `.crucible`.\n\n")

	fmt.Fprintf(&b, "## Required Contract\n\n")
	fmt.Fprintf(&b, "Read and follow `%s`. Every generated competitor must be a drop-in replacement for the documented baseline interface.\n\n", req.InterfaceDocPath)
	if baselinePending {
		fmt.Fprintf(&b, "## Baseline Discovery Required\n\n")
		fmt.Fprintf(&b, "No baseline source path was selected when this run was created. Before creating competitor implementations:\n\n")
		fmt.Fprintf(&b, "1. Inspect the host project and identify the source files involved in the optimization request.\n")
		fmt.Fprintf(&b, "2. Copy the original drop-in baseline implementation into `%s`.\n", req.BaselineSourceDir)
		fmt.Fprintf(&b, "3. Preserve the relative filenames and layout required by the documented interface.\n")
		fmt.Fprintf(&b, "4. Update `%s` only if the discovered interface differs from the current notes or needs more detail.\n", req.InterfaceDocPath)
		fmt.Fprintf(&b, "5. Do not create candidate-NNNN competitors until candidate-0000-baseline/src contains the original baseline source.\n\n")
	}

	fmt.Fprintf(&b, "## External Policy\n\n")
	fmt.Fprintf(&b, "- Mode: `%s`\n", cfg.External.Mode)
	if len(cfg.External.Allowlist) > 0 {
		fmt.Fprintf(&b, "- Allowlist: `%s`\n", strings.Join(cfg.External.Allowlist, "`, `"))
	}
	if cfg.External.Fixtures != "" {
		fmt.Fprintf(&b, "- Fixtures: `%s`\n", cfg.External.Fixtures)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Generation Balance\n\n")
	fmt.Fprintf(&b, "Exploration setting: %.2f\n\n", cfg.Exploration)
	fmt.Fprintf(&b, "- Lower values should favor incremental improvements to known-good approaches.\n")
	fmt.Fprintf(&b, "- Higher values should reserve more variants for structurally different approaches.\n")
	fmt.Fprintf(&b, "- Keep all competitors compatible with the evaluator and external policy.\n\n")

	fmt.Fprintf(&b, "## Benchmark-Aware Optimization\n\n")
	fmt.Fprintf(&b, "- Optimize the documented real workload, not an accidental repeated-fixture or cache-hit shortcut in the evaluator.\n")
	fmt.Fprintf(&b, "- Caching and memoization are valid only when they preserve the drop-in contract and plausibly match the requested workload. Disclose cache keys, invalidation behavior, memory tradeoffs, and cache-cold performance in design.md.\n")
	fmt.Fprintf(&b, "- If a variant depends on cache-warm behavior, include that as an explicit risk and verify that non-cached or varied inputs remain competitive.\n\n")

	fmt.Fprintf(&b, "## Evolution Strategy\n\n")
	fmt.Fprintf(&b, "- Preferred parent candidates: `%s`\n", strings.Join(parentIDs, "`, `"))
	fmt.Fprintf(&b, "- Create candidates starting at `%s` and continue sequentially.\n", nextCandidateID)
	if round > 1 {
		fmt.Fprintf(&b, "- Use the historical metrics below to improve the strongest prior approaches while reserving exploration budget for different designs.\n")
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Historical Context\n\n")
	if len(req.History) == 0 {
		fmt.Fprintf(&b, "No prior competitor metrics exist yet. Use the baseline source as candidate-0000 and create new competitors beside it.\n\n")
	} else {
		for _, result := range req.History {
			externalCalls := historicalExternalCallCount(result)
			fmt.Fprintf(&b, "- %s: status=%s score=%.4g runtime_mean_ms=%.6g p95_latency_ms=%.6g cpu_seconds=%.6g memory_peak_bytes=%d external_calls=%d",
				result.Candidate.ID,
				result.Status,
				result.Score,
				result.Metrics.RuntimeMeanMS,
				result.Metrics.P95LatencyMS,
				result.Metrics.CPUUserSeconds+result.Metrics.CPUSystemSeconds,
				result.Metrics.MemoryPeakBytes,
				externalCalls,
			)
			if result.ScoreExplanation != nil {
				fmt.Fprintf(&b, " score_penalties={primary:%.4g memory:%.4g external_calls:%.4g external_latency:%.4g external_cost:%.4g total:%.4g}",
					result.ScoreExplanation.PrimaryPenalty,
					result.ScoreExplanation.MemoryPenalty,
					result.ScoreExplanation.ExternalCallPenalty,
					result.ScoreExplanation.ExternalLatencyPenalty,
					result.ScoreExplanation.ExternalCostPenalty,
					result.ScoreExplanation.TotalPenalty,
				)
			}
			fmt.Fprintf(&b, "\n")
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, `## Output Requirements

For each competitor:

1. Create a directory named candidate-NNNN under the current round directory, starting with %s.
2. Put replacement source under candidate-NNNN/src.
3. Include candidate-NNNN/design.md explaining the approach, expected tradeoffs, and known risks.
4. Include candidate-NNNN/candidate.json using this shape:

   {
     "id": "candidate-NNNN",
     "name": "short descriptive name",
     "round": %d,
     "parent_ids": %s,
     "agent": "%s",
     "model": "model name if known",
     "source_path": "src",
     "baseline": false,
     "created_at": "RFC3339 UTC timestamp"
   }

5. %s
6. Do not introduce external services or protocols that violate the external policy.
7. Avoid destructive cleanup commands such as rm -rf. Create fresh temporary paths under the scratch directory instead and leave scratch artifacts for archive inspection.

Favor measurable changes. If a competitor is experimental, make the experiment explicit in design.md.
`, nextCandidateID, round, jsonStringArray(parentIDs), candidateAgent, baselineModificationRule(baselinePending))

	return b.String()
}

func baselineModificationRule(baselinePending bool) string {
	if baselinePending {
		return "The only allowed baseline change is populating candidate-0000-baseline/src with original host-project source; otherwise do not modify evaluator files, run metadata, leaderboard.json, or completed candidate artifacts."
	}
	return "Do not modify baseline source, evaluator files, run metadata, leaderboard.json, or completed candidate artifacts."
}

func jsonStringArray(values []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", value)
	}
	b.WriteByte(']')
	return b.String()
}

func historicalExternalCallCount(result model.CandidateResult) int {
	if result.ScoreExplanation != nil && result.ScoreExplanation.ExternalCallCount > 0 {
		return result.ScoreExplanation.ExternalCallCount
	}
	if result.Metrics.ExternalCallCount > 0 {
		return result.Metrics.ExternalCallCount
	}
	return result.External.RequestCount
}
