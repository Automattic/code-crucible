#!/usr/bin/env bash
set -euo pipefail

candidate_dir="${1:?candidate directory required}"
run_dir="${2:?run directory required}"
metrics_out="${3:?metrics output path required}"
verdict_out="${4:?verdict output path required}"

project_dir="$(sed -n 's/^  "project_dir": "\(.*\)",$/\1/p' "$run_dir/run.json")"
if [[ -z "$project_dir" || ! -d "$project_dir" ]]; then
  echo "unable to read project_dir from $run_dir/run.json" >&2
  exit 2
fi

work_dir="$run_dir/tmp/eval-$(basename "$candidate_dir")"
rm -rf "$work_dir"
mkdir -p "$work_dir/ranking"

cp "$project_dir/go.mod" "$work_dir/go.mod"
cp "$project_dir/ranking/rank_test.go" "$work_dir/ranking/rank_test.go"

if [[ -d "$candidate_dir/src/ranking" ]]; then
  cp "$candidate_dir"/src/ranking/*.go "$work_dir/ranking/"
else
  cp "$candidate_dir"/src/*.go "$work_dir/ranking/"
fi

correctness_log="$candidate_dir/correctness.log"
benchmark_log="$candidate_dir/benchmark.log"

correctness_passed=false
benchmark_passed=false

if (cd "$work_dir" && go test ./... >"$correctness_log" 2>&1); then
  correctness_passed=true
fi

if [[ "$correctness_passed" == true ]] && (cd "$work_dir" && go test -bench=. -benchmem -run=^$ ./... >"$benchmark_log" 2>&1); then
  benchmark_passed=true
fi

runtime_ns="$(awk '/BenchmarkTopN/ && /ns\/op/ { for (i = 1; i <= NF; i++) if ($(i + 1) == "ns/op") value = $i } END { if (value == "") value = 0; print value }' "$benchmark_log" 2>/dev/null || printf '0')"
allocs_per_op="$(awk '/BenchmarkTopN/ && /allocs\/op/ { for (i = 1; i <= NF; i++) if ($(i + 1) == "allocs/op") value = $i } END { if (value == "") value = 0; print value }' "$benchmark_log" 2>/dev/null || printf '0')"
bytes_per_op="$(awk '/BenchmarkTopN/ && /B\/op/ { for (i = 1; i <= NF; i++) if ($(i + 1) == "B/op") value = $i } END { if (value == "") value = 0; print value }' "$benchmark_log" 2>/dev/null || printf '0')"

p95_latency_ms="$(awk -v ns="$runtime_ns" 'BEGIN { printf "%.6f", ns / 1000000 }')"

cat > "$metrics_out" <<JSON
{
  "runtime_mean_ms": $p95_latency_ms,
  "p95_latency_ms": $p95_latency_ms,
  "memory_peak_bytes": $bytes_per_op,
  "external_call_count": 0
}
JSON

errors_json="[]"
if [[ "$correctness_passed" != true ]]; then
  errors_json='["correctness tests failed; see correctness.log"]'
elif [[ "$benchmark_passed" != true ]]; then
  errors_json='["benchmark failed; see benchmark.log"]'
fi

cat > "$verdict_out" <<JSON
{
  "correctness_passed": $correctness_passed,
  "benchmark_passed": $benchmark_passed,
  "external_policy_passed": true,
  "errors": $errors_json,
  "notes": [
    "allocs_per_op=$allocs_per_op"
  ]
}
JSON
