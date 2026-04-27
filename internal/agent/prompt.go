package agent

import (
	"fmt"
	"strings"

	"github.com/Automattic/code-crucible/internal/model"
)

type GenerationPromptRequest struct {
	RunConfig         model.RunConfig
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

	fmt.Fprintf(&b, "# Code Crucible Candidate Generation Prompt\n\n")
	fmt.Fprintf(&b, "Optimization request: %s\n\n", cfg.Optimize)
	fmt.Fprintf(&b, "Generate %d competitor implementations for round 1.\n\n", cfg.Variants)

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

	fmt.Fprintf(&b, "## Historical Context\n\n")
	if len(req.History) == 0 {
		fmt.Fprintf(&b, "No prior competitor metrics exist yet. Use the baseline source as candidate-0000 and create new competitors beside it.\n\n")
	} else {
		for _, result := range req.History {
			fmt.Fprintf(&b, "- %s: status=%s score=%.4g runtime_mean_ms=%.6g p95_latency_ms=%.6g cpu_seconds=%.6g memory_peak_bytes=%d external_calls=%d\n",
				result.Candidate.ID,
				result.Status,
				result.Score,
				result.Metrics.RuntimeMeanMS,
				result.Metrics.P95LatencyMS,
				result.Metrics.CPUUserSeconds+result.Metrics.CPUSystemSeconds,
				result.Metrics.MemoryPeakBytes,
				result.Metrics.ExternalCallCount,
			)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, `## Output Requirements

For each competitor:

1. Create a directory named candidate-NNNN under the current round directory, starting with candidate-0001.
2. Put replacement source under candidate-NNNN/src.
3. Include candidate-NNNN/design.md explaining the approach, expected tradeoffs, and known risks.
4. Include candidate-NNNN/candidate.json using this shape:

   {
     "id": "candidate-NNNN",
     "name": "short descriptive name",
     "round": 1,
     "parent_ids": ["candidate-0000-baseline"],
     "agent": "codex",
     "model": "model name if known",
     "source_path": "src",
     "baseline": false,
     "created_at": "RFC3339 UTC timestamp"
   }

5. Do not modify baseline source, evaluator files, run metadata, leaderboard.json, or completed candidate artifacts.
6. Do not introduce external services or protocols that violate the external policy.
7. Avoid destructive cleanup commands such as rm -rf. Create fresh temporary paths under the scratch directory instead and leave scratch artifacts for archive inspection.

Favor measurable changes. If a competitor is experimental, make the experiment explicit in design.md.
`)

	return b.String()
}
