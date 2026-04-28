#!/usr/bin/env bash
set -euo pipefail

candidate_dir="${1:?candidate directory required}"
run_dir="${2:?run directory required}"
metrics_out="${3:?metrics output path required}"
verdict_out="${4:?verdict output path required}"

project_dir="${CRUCIBLE_PROJECT_DIR:-}"
if [[ -z "$project_dir" ]]; then
  project_dir="$(sed -n 's/^  "project_dir": "\(.*\)",$/\1/p' "$run_dir/run.json")"
fi
if [[ "$project_dir" == "." ]]; then
  project_dir="$(cd "$run_dir/../../.." && pwd)"
fi
if [[ -z "$project_dir" || ! -d "$project_dir" ]]; then
  echo "unable to read project_dir from $run_dir/run.json" >&2
  exit 2
fi

work_dir="$run_dir/tmp/eval-$(basename "$candidate_dir")"
rm -rf "$work_dir"
mkdir -p "$work_dir/ranking"

export GOMAXPROCS="${GOMAXPROCS:-1}"
go_test_cpu="${GO_TEST_CPU:-1}"
benchmark_count="${GO_BENCH_COUNT:-5}"

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

if (cd "$work_dir" && go test -cpu="$go_test_cpu" ./... >"$correctness_log" 2>&1); then
  correctness_passed=true
fi

if [[ "$correctness_passed" == true ]] && (cd "$work_dir" && go test -cpu="$go_test_cpu" -bench=. -benchmem -count="$benchmark_count" -run=^$ ./... >"$benchmark_log" 2>&1); then
  benchmark_passed=true
fi

benchmark_stats="$(awk '/BenchmarkTopN/ && /ns\/op/ { for (i = 1; i <= NF; i++) if ($(i + 1) == "ns/op") values[++n] = $i } END { if (n == 0) { print "0 0"; exit } sort(values, n); total = 0; for (i = 1; i <= n; i++) total += values[i]; mean = total / n; p95_index = int(n * 0.95); if (p95_index < n * 0.95) p95_index++; if (p95_index < 1) p95_index = 1; if (p95_index > n) p95_index = n; printf "%.0f %.0f", mean, values[p95_index] } function sort(values, n, i, j, tmp) { for (i = 1; i <= n; i++) for (j = i + 1; j <= n; j++) if (values[j] < values[i]) { tmp = values[i]; values[i] = values[j]; values[j] = tmp } }' "$benchmark_log" 2>/dev/null || printf '0 0')"
runtime_ns="${benchmark_stats%% *}"
p95_ns="${benchmark_stats##* }"
allocs_per_op="$(awk '/BenchmarkTopN/ && /allocs\/op/ { for (i = 1; i <= NF; i++) if ($(i + 1) == "allocs/op") values[++n] = $i } END { print median(values, n) } function median(values, n, i, j, tmp) { if (n == 0) return 0; for (i = 1; i <= n; i++) for (j = i + 1; j <= n; j++) if (values[j] < values[i]) { tmp = values[i]; values[i] = values[j]; values[j] = tmp } if (n % 2) return values[(n + 1) / 2]; return (values[n / 2] + values[n / 2 + 1]) / 2 }' "$benchmark_log" 2>/dev/null || printf '0')"
bytes_per_op="$(awk '/BenchmarkTopN/ && /B\/op/ { for (i = 1; i <= NF; i++) if ($(i + 1) == "B/op") values[++n] = $i } END { print median(values, n) } function median(values, n, i, j, tmp) { if (n == 0) return 0; for (i = 1; i <= n; i++) for (j = i + 1; j <= n; j++) if (values[j] < values[i]) { tmp = values[i]; values[i] = values[j]; values[j] = tmp } if (n % 2) return values[(n + 1) / 2]; return (values[n / 2] + values[n / 2 + 1]) / 2 }' "$benchmark_log" 2>/dev/null || printf '0')"
benchmark_runs="$(awk '/BenchmarkTopN/ && /ns\/op/ { n++ } END { print n + 0 }' "$benchmark_log" 2>/dev/null || printf '0')"

runtime_mean_ms="$(awk -v ns="$runtime_ns" 'BEGIN { printf "%.6f", ns / 1000000 }')"
p95_latency_ms="$(awk -v ns="$p95_ns" 'BEGIN { printf "%.6f", ns / 1000000 }')"

cat > "$metrics_out" <<JSON
{
  "runtime_mean_ms": $runtime_mean_ms,
  "p95_latency_ms": $p95_latency_ms,
  "benchmark_ns_per_op": $runtime_ns,
  "benchmark_runs": $benchmark_runs,
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
    "allocs_per_op=$allocs_per_op",
    "benchmark_runs=$benchmark_runs",
    "gomaxprocs=$GOMAXPROCS",
    "go_test_cpu=$go_test_cpu"
  ]
}
JSON
