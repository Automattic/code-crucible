package evaluator

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Automattic/code-crucible/internal/discovery"
)

func DefaultScript(command string) string {
	return DefaultScriptWithAgentPlan(command, nil)
}

func DefaultScriptWithAgentPlan(command string, plan *discovery.AgentPlan) string {
	var b strings.Builder
	b.WriteString(`#!/usr/bin/env bash
set -euo pipefail

candidate_dir="${1:?candidate directory required}"
run_dir="${2:?run directory required}"
metrics_out="${3:-$candidate_dir/metrics.json}"
verdict_out="${4:-$candidate_dir/verdict.json}"

src_dir="$candidate_dir/src"

if [[ ! -d "$src_dir" ]]; then
  echo "candidate src directory does not exist: $src_dir" >&2
  exit 2
fi

start_ns="$(date +%s%N)"
status="pending"
error_message=""

`)

	if plan != nil && plan.HasContent() {
		b.WriteString("# Discovery-derived evaluator guidance\n")
		writeShellComment(&b, "Drop-in interface", plan.DropInInterface)
		writeShellCommentList(&b, "Required inputs", plan.Inputs)
		writeShellCommentList(&b, "Required outputs", plan.Outputs)
		writeShellCommentList(&b, "Evaluator strategy", plan.EvaluatorStrategy)
		writeShellCommentList(&b, "Metrics", plan.Metrics)
		b.WriteString("\n")
	}

	if strings.TrimSpace(command) == "" {
		warnings := []string{"No evaluator command has been configured yet."}
		notes := []string{"Replace .crucible/evaluators or this run's evaluator/evaluator.sh with deterministic checks and benchmarks."}
		if plan != nil && plan.HasContent() {
			notes = append(notes, "This evaluator scaffold includes discovery-derived contract guidance from docs/agent-discovery.md.")
			if strings.TrimSpace(plan.DropInInterface) != "" {
				notes = append(notes, "Drop-in interface: "+strings.TrimSpace(plan.DropInInterface))
			}
			notes = append(notes, prefixedValues("Evaluator strategy: ", plan.EvaluatorStrategy)...)
			notes = append(notes, prefixedValues("Metric: ", plan.Metrics)...)
		}
		fmt.Fprintf(&b, `cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": false,
  "benchmark_passed": false,
  "external_policy_passed": false,
  "warnings": %s,
  "notes": %s
}
JSON
`, jsonStringArray(warnings), jsonStringArray(notes))
	} else {
		quoted := strconv.Quote(command)
		fmt.Fprintf(&b, "evaluator_command=%s\n", quoted)
		b.WriteString(`if (cd "$src_dir" && bash -lc "$evaluator_command"); then
  status="passed"
else
  status="failed"
  error_message="evaluator command failed"
fi

if [[ "$status" == "passed" ]]; then
  cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
else
  cat > "$verdict_out" <<JSON
{
  "correctness_passed": false,
  "benchmark_passed": false,
  "external_policy_passed": false,
  "errors": [
    "$error_message"
  ]
}
JSON
fi
`)
	}

	b.WriteString(`
end_ns="$(date +%s%N)"
runtime_ms="$(( (end_ns - start_ns) / 1000000 ))"

cat > "$metrics_out" <<JSON
{
  "runtime_mean_ms": $runtime_ms
}
JSON
`)

	return b.String()
}

func writeShellComment(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	fmt.Fprintf(b, "# %s: %s\n", label, value)
}

func writeShellCommentList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "# %s:\n", label)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		fmt.Fprintf(b, "# - %s\n", value)
	}
}

func prefixedValues(prefix string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, prefix+value)
	}
	return out
}

func jsonStringArray(values []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(value))
	}
	b.WriteByte(']')
	return b.String()
}
