#!/usr/bin/env bash
set -euo pipefail

candidate_dir="${1:?candidate directory required}"
run_dir="${2:?run directory required}"
metrics_out="${3:?metrics output path required}"
verdict_out="${4:?verdict output path required}"

work_dir="$run_dir/tmp/gateway-network-$(basename "$candidate_dir")"
rm -rf "$work_dir"
mkdir -p "$work_dir"

cat > "$work_dir/client.go" <<'GO'
package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		},
	}
	check(client, "http://api.crucible.test/v1/status", "http fixture ok")
	check(client, "https://secure.crucible.test/v1/status", "https fixture ok")
}

func check(client *http.Client, target, want string) {
	resp, err := client.Get(target)
	if err != nil {
		fail("%s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fail("%s: read body: %v", target, err)
	}
	if resp.StatusCode != http.StatusOK {
		fail("%s: status %d body %q", target, resp.StatusCode, string(body))
	}
	if string(body) != want {
		fail("%s: body %q, want %q", target, string(body), want)
	}
	fmt.Printf("%s -> %s\n", target, body)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
GO

client_log="$candidate_dir/gateway-network-client.log"
started_ns="$(date +%s%N 2>/dev/null || printf '0')"
if go run "$work_dir/client.go" >"$client_log" 2>&1; then
  passed=true
  errors_json="[]"
else
  passed=false
  errors_json='["gateway-network client failed; see gateway-network-client.log"]'
fi
finished_ns="$(date +%s%N 2>/dev/null || printf '0')"

runtime_mean_ms="$(awk -v start="$started_ns" -v end="$finished_ns" 'BEGIN {
  if (start > 0 && end >= start) {
    printf "%.6f", (end - start) / 1000000
  } else {
    printf "0.000000"
  }
}')"

cat > "$metrics_out" <<JSON
{
  "runtime_mean_ms": $runtime_mean_ms,
  "p95_latency_ms": $runtime_mean_ms,
  "external_call_count": 2
}
JSON

cat > "$verdict_out" <<JSON
{
  "correctness_passed": $passed,
  "benchmark_passed": $passed,
  "external_policy_passed": $passed,
  "errors": $errors_json,
  "notes": [
    "raw HTTP and HTTPS requests ignore proxy environment variables",
    "see gateway-network-client.log"
  ]
}
JSON
