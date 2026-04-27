package evaluator

import (
	"fmt"
	"strconv"
	"strings"
)

func DefaultScript(command string) string {
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

	if strings.TrimSpace(command) == "" {
		b.WriteString(`cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": false,
  "benchmark_passed": false,
  "external_policy_passed": false,
  "warnings": [
    "No evaluator command has been configured yet."
  ],
  "notes": [
    "Replace .crucible/evaluators or this run's evaluator/evaluator.sh with deterministic checks and benchmarks."
  ]
}
JSON
`)
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
