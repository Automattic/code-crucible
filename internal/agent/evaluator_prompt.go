package agent

import (
	"fmt"
	"strings"

	"github.com/Automattic/code-crucible/internal/model"
)

type EvaluatorPromptRequest struct {
	RunConfig         model.RunConfig
	InterfaceDocPath  string
	RunDir            string
	EvaluatorPath     string
	BaselineSourceDir string
	ScratchDir        string
}

func BuildEvaluatorPrompt(req EvaluatorPromptRequest) string {
	cfg := req.RunConfig
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Crucible Evaluator Generation Prompt\n\n")
	fmt.Fprintf(&b, "Optimization request: %s\n\n", cfg.Optimize)
	fmt.Fprintf(&b, "Create a deterministic evaluator for this optimization tournament.\n\n")

	fmt.Fprintf(&b, "## Workspace\n\n")
	fmt.Fprintf(&b, "- Host project directory: `%s`\n", cfg.ProjectDir)
	fmt.Fprintf(&b, "- Code Crucible run directory: `%s`\n", req.RunDir)
	fmt.Fprintf(&b, "- Evaluator script to write: `%s`\n", req.EvaluatorPath)
	fmt.Fprintf(&b, "- Baseline source directory: `%s`\n", req.BaselineSourceDir)
	fmt.Fprintf(&b, "- Temporary verification scratch directory: `%s`\n\n", req.ScratchDir)

	fmt.Fprintf(&b, "Read `%s` and the baseline source before writing the evaluator. The evaluator must judge whether a candidate is a drop-in replacement for the documented interface and must measure the requested optimization target.\n\n", req.InterfaceDocPath)

	fmt.Fprintf(&b, "## Output Requirements\n\n")
	fmt.Fprintf(&b, "1. Replace `%s` with an executable Bash evaluator script.\n", req.EvaluatorPath)
	fmt.Fprintf(&b, "2. The evaluator must accept the standard arguments: candidate directory, run directory, metrics output path, and verdict output path.\n")
	fmt.Fprintf(&b, "3. The evaluator must write valid JSON to the supplied metrics and verdict output paths.\n")
	fmt.Fprintf(&b, "4. The evaluator should create deterministic fixtures under the run scratch directory or candidate scratch paths, not under host project source.\n")
	fmt.Fprintf(&b, "5. The baseline candidate must be able to pass the evaluator before generated competitors are trusted.\n")
	fmt.Fprintf(&b, "6. Do not modify host project source outside `.crucible`.\n")
	fmt.Fprintf(&b, "7. Do not modify generated competitors, leaderboard.json, or run metadata.\n")
	fmt.Fprintf(&b, "8. Include evaluator design notes at `%s` describing correctness checks, benchmark strategy, metrics, and known limits.\n\n", strings.TrimSuffix(req.EvaluatorPath, ".sh")+".md")

	fmt.Fprintf(&b, "## Required Verdict Shape\n\n")
	fmt.Fprintf(&b, "A passing candidate must produce:\n\n")
	fmt.Fprintf(&b, "```json\n")
	fmt.Fprintf(&b, "{\n  \"correctness_passed\": true,\n  \"benchmark_passed\": true,\n  \"external_policy_passed\": true\n}\n")
	fmt.Fprintf(&b, "```\n\n")

	fmt.Fprintf(&b, "## Required Metrics\n\n")
	fmt.Fprintf(&b, "Prefer metrics that directly support this optimization request. Use stable repeated measurements when practical. Useful fields include:\n\n")
	fmt.Fprintf(&b, "- `runtime_mean_ms`\n")
	fmt.Fprintf(&b, "- `p95_latency_ms`\n")
	fmt.Fprintf(&b, "- `benchmark_ns_per_op`\n")
	fmt.Fprintf(&b, "- `benchmark_runs`\n")
	fmt.Fprintf(&b, "- `memory_peak_bytes`\n")
	fmt.Fprintf(&b, "- `external_call_count`\n\n")

	fmt.Fprintf(&b, "## External Policy\n\n")
	fmt.Fprintf(&b, "- Mode: `%s`\n", cfg.External.Mode)
	if len(cfg.External.Allowlist) > 0 {
		fmt.Fprintf(&b, "- Allowlist: `%s`\n", strings.Join(cfg.External.Allowlist, ", "))
	}
	if strings.TrimSpace(cfg.External.Fixtures) != "" {
		fmt.Fprintf(&b, "- Fixtures: `%s`\n", cfg.External.Fixtures)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "Favor correctness over clever benchmarking. If a high-confidence benchmark cannot be built from the available project context, write an evaluator that fails closed with clear notes instead of producing misleading passing metrics.\n")
	return b.String()
}
