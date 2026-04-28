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
	checks := BuildContractChecks(plan)
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
contract_check_enabled=0
required_source_extensions=()
contract_check_errors=()
contract_check_notes=()

json_array() {
  local first=1
  local item
  printf '['
  for item in "$@"; do
    if [[ "$first" -eq 0 ]]; then
      printf ', '
    fi
    printf '"'
    printf '%s' "$item" | sed 's/\\/\\\\/g; s/"/\\"/g'
    printf '"'
    first=0
  done
  printf ']'
}

run_generated_contract_checks() {
  if [[ "$contract_check_enabled" != "1" ]]; then
    return
  fi
  contract_check_notes+=("Generated deterministic contract checks from evaluator/contract-checks.json.")
  if [[ ! -f "$candidate_dir/candidate.json" ]]; then
    contract_check_errors+=("candidate.json is missing")
  fi
  if [[ ! -f "$candidate_dir/design.md" ]]; then
    contract_check_errors+=("design.md is missing")
  fi
  if ! find "$src_dir" -type f -print -quit | grep -q .; then
    contract_check_errors+=("candidate src contains no source files")
  fi
  local required_ext
  for required_ext in "${required_source_extensions[@]}"; do
    if ! find "$src_dir" -type f -name "*${required_ext}" -print -quit | grep -q .; then
      contract_check_errors+=("candidate src does not contain required ${required_ext} source files")
    fi
  done
}

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
	if checks != nil {
		b.WriteString("contract_check_enabled=1\n")
		fmt.Fprintf(&b, "required_source_extensions=%s\n", shellStringArray(checks.RequiredSourceExtensions))
		b.WriteString("\n")
	}

	if strings.TrimSpace(command) == "" {
		warnings := []string{"No evaluator command has been configured yet."}
		notes := []string{"Replace .crucible/evaluators or this run's evaluator/evaluator.sh with deterministic checks and benchmarks."}
		if checks != nil {
			notes = []string{"Generated deterministic contract checks from docs/agent-discovery.md.", "Add a benchmark command or evaluator script before comparing performance scores."}
			if strings.TrimSpace(checks.DropInInterface) != "" {
				notes = append(notes, "Drop-in interface: "+strings.TrimSpace(checks.DropInInterface))
			}
			notes = append(notes, prefixedValues("Evaluator strategy: ", checks.EvaluatorStrategy)...)
			notes = append(notes, prefixedValues("Metric: ", checks.Metrics)...)
		} else if plan != nil && plan.HasContent() {
			notes = append(notes, "This evaluator scaffold includes discovery-derived contract guidance from docs/agent-discovery.md.")
			if strings.TrimSpace(plan.DropInInterface) != "" {
				notes = append(notes, "Drop-in interface: "+strings.TrimSpace(plan.DropInInterface))
			}
			notes = append(notes, prefixedValues("Evaluator strategy: ", plan.EvaluatorStrategy)...)
			notes = append(notes, prefixedValues("Metric: ", plan.Metrics)...)
		}
		if checks != nil {
			fmt.Fprintf(&b, "warnings=%s\n", shellStringArray(warnings))
			fmt.Fprintf(&b, "notes=%s\n", shellStringArray(notes))
			b.WriteString(`run_generated_contract_checks
notes+=("${contract_check_notes[@]}")
if [[ "${#contract_check_errors[@]}" -eq 0 ]]; then
  correctness_passed=true
else
  correctness_passed=false
  warnings=("Generated deterministic contract checks failed before benchmarking.")
fi
cat > "$verdict_out" <<JSON
{
  "correctness_passed": $correctness_passed,
  "benchmark_passed": false,
  "external_policy_passed": true,
  "errors": $(json_array "${contract_check_errors[@]}"),
  "warnings": $(json_array "${warnings[@]}"),
  "notes": $(json_array "${notes[@]}")
}
JSON
`)
		} else {
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
		}
	} else {
		quoted := strconv.Quote(command)
		fmt.Fprintf(&b, "evaluator_command=%s\n", quoted)
		b.WriteString(`run_generated_contract_checks
if [[ "${#contract_check_errors[@]}" -gt 0 ]]; then
  status="failed"
  error_message="generated contract checks failed"
elif (cd "$src_dir" && bash -lc "$evaluator_command"); then
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
  "external_policy_passed": true,
  "errors": $(if [[ "${#contract_check_errors[@]}" -gt 0 ]]; then json_array "${contract_check_errors[@]}"; else json_array "$error_message"; fi)
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

func shellStringArray(values []string) string {
	var b strings.Builder
	b.WriteByte('(')
	for i, value := range values {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.Quote(value))
	}
	b.WriteByte(')')
	return b.String()
}
