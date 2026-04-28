package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Automattic/code-crucible/internal/model"
)

func runEvaluatorScript(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, stdoutPath, stderrPath string, opts evaluatorExecutionOptions) (model.Metrics, error) {
	if info, err := os.Stat(evaluatorPath); err != nil || info.IsDir() {
		return model.Metrics{}, fmt.Errorf("evaluator script is missing: %s", evaluatorPath)
	}
	if info, err := os.Stat(candidateDir); err != nil || !info.IsDir() {
		return model.Metrics{}, fmt.Errorf("candidate directory is missing: %s", candidateDir)
	}

	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o755); err != nil {
		return model.Metrics{}, err
	}
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return model.Metrics{}, err
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return model.Metrics{}, err
	}
	defer stderrFile.Close()

	ctx := context.Background()
	cancel := func() {}
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}
	defer cancel()

	if sandboxEnabled(opts.Sandbox) {
		if err := os.MkdirAll(sandboxHomeDir(runDir), 0o755); err != nil {
			return model.Metrics{}, err
		}
		if err := ensureSandboxResourceWrapper(runDir); err != nil {
			return model.Metrics{}, err
		}
	}

	name, args, err := buildEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, opts)
	if err != nil {
		return model.Metrics{}, err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = runDir
	if !sandboxEnabled(opts.Sandbox) && len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	start := time.Now()
	err = cmd.Run()
	resourceMetrics := processMetrics(cmd.ProcessState, time.Since(start))
	if sandboxEnabled(opts.Sandbox) {
		containerMetrics, loadErr := loadResourceMetrics(resourcePath)
		if loadErr == nil {
			resourceMetrics = containerMetrics
		} else if err == nil {
			err = loadErr
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return resourceMetrics, fmt.Errorf("evaluator timed out after %s", opts.Timeout)
	}
	if err != nil {
		return resourceMetrics, fmt.Errorf("evaluator failed: %w", err)
	}
	return resourceMetrics, nil
}

func buildEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath string, opts evaluatorExecutionOptions) (string, []string, error) {
	sandbox, err := NormalizeSandboxOptions(opts.Sandbox)
	if err != nil {
		return "", nil, err
	}
	opts.Sandbox = sandbox

	if sandboxEnabled(opts.Sandbox) {
		return buildContainerEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, opts)
	}

	name := "bash"
	args := []string{evaluatorPath, candidateDir, runDir, metricsPath, verdictPath}
	if opts.CPULimit > 0 {
		name, args = wrapTasksetCommand(name, args, opts.CPULimit)
	}
	if opts.Nice > 0 {
		args = append([]string{"-n", strconv.Itoa(opts.Nice), name}, args...)
		name = "nice"
	}
	return name, args, nil
}

func buildContainerEvaluatorCommand(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath string, opts evaluatorExecutionOptions) (string, []string, error) {
	args := []string{
		"run",
		"--rm",
		"--network", opts.Sandbox.Network,
		"--volume", sandboxMount(runDir, "rw"),
	}

	if opts.ProjectDir != "" && filepath.Clean(opts.ProjectDir) != filepath.Clean(runDir) {
		args = append(args, "--volume", sandboxMount(opts.ProjectDir, "ro"))
	}

	if opts.CPULimit > 0 {
		args = append(args, "--cpus", strconv.Itoa(opts.CPULimit))
	}
	if opts.Sandbox.MemoryLimit != "" {
		args = append(args, "--memory", opts.Sandbox.MemoryLimit)
	}
	if opts.Sandbox.PIDsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(opts.Sandbox.PIDsLimit))
	}

	if opts.Sandbox.Engine == "podman" {
		args = append(args, "--userns", "keep-id")
	} else if uid := os.Getuid(); uid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, os.Getgid()))
	}

	args = append(args,
		"--workdir", runDir,
		"--env", "HOME="+sandboxHomeDir(runDir),
	)
	for _, env := range opts.Env {
		args = append(args, "--env", env)
	}

	args = append(args,
		opts.Sandbox.Image,
		"bash",
		sandboxResourceWrapperPath(runDir),
		evaluatorPath,
		candidateDir,
		runDir,
		metricsPath,
		resourcePath,
		verdictPath,
	)
	return opts.Sandbox.Engine, args, nil
}

func ensureSandboxResourceWrapper(runDir string) error {
	path := sandboxResourceWrapperPath(runDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sandboxResourceWrapperScript()), 0o755)
}

func sandboxResourceWrapperPath(runDir string) string {
	return filepath.Join(runDir, "evaluator", "resource-wrapper.sh")
}

func sandboxResourceWrapperScript() string {
	return `#!/usr/bin/env bash
set -u

evaluator_path="${1:?evaluator path required}"
candidate_dir="${2:?candidate directory required}"
run_dir="${3:?run directory required}"
metrics_out="${4:?metrics output path required}"
resource_out="${5:?resource metrics output path required}"
verdict_out="${6:?verdict output path required}"

mkdir -p "$(dirname "$resource_out")"
timing_out="${resource_out}.time"
rm -f "$timing_out"

gateway_pid=""
gateway_log="${resource_out}.mock-gateway.log"
cleanup_gateway() {
  if [[ -n "$gateway_pid" ]] && kill -0 "$gateway_pid" 2>/dev/null; then
    kill "$gateway_pid" 2>/dev/null || true
    wait "$gateway_pid" 2>/dev/null || true
  fi
}
trap cleanup_gateway EXIT

if [[ -n "${CRUCIBLE_MOCK_GATEWAY_SOURCE:-}" ]]; then
  if [[ -z "${CRUCIBLE_HTTP_FIXTURES:-}" ]]; then
    echo "CRUCIBLE_HTTP_FIXTURES is required when CRUCIBLE_MOCK_GATEWAY_SOURCE is set" >&2
    exit 126
  fi
  if ! command -v go >/dev/null 2>&1; then
    echo "mock gateway startup requires go in the sandbox image" >&2
    exit 126
  fi
  gateway_addr="${CRUCIBLE_MOCK_GATEWAY_ADDR:-127.0.0.1:18080}"
  gateway_host="${gateway_addr%:*}"
  gateway_port="${gateway_addr##*:}"
  rm -f "$gateway_log"
  gateway_args=("$CRUCIBLE_MOCK_GATEWAY_SOURCE" -fixtures "$CRUCIBLE_HTTP_FIXTURES" -addr "$gateway_addr")
  if [[ "${CRUCIBLE_EXTERNAL_MODE:-}" == "allowlist" ]]; then
    if [[ -z "${CRUCIBLE_ALLOWED_HOSTS:-}" ]]; then
      echo "CRUCIBLE_ALLOWED_HOSTS is required in allowlist mode" >&2
      exit 126
    fi
    gateway_args+=(-allow-hosts "$CRUCIBLE_ALLOWED_HOSTS" -passthrough)
  fi
  if [[ -n "${CRUCIBLE_MOCK_CA_CERT:-}" && -n "${CRUCIBLE_MOCK_CA_KEY:-}" ]]; then
    gateway_args+=(-ca-cert "$CRUCIBLE_MOCK_CA_CERT" -ca-key "$CRUCIBLE_MOCK_CA_KEY")
  fi
  go run "${gateway_args[@]}" >"$gateway_log" 2>&1 &
  gateway_pid=$!
  gateway_ready=0
  for _ in {1..100}; do
    if ! kill -0 "$gateway_pid" 2>/dev/null; then
      echo "mock gateway exited during startup" >&2
      cat "$gateway_log" >&2 2>/dev/null || true
      exit 126
    fi
    if (: >"/dev/tcp/${gateway_host}/${gateway_port}") >/dev/null 2>&1; then
      gateway_ready=1
      break
    fi
    sleep 0.1
  done
  if [[ "$gateway_ready" != "1" ]]; then
    echo "mock gateway did not become ready on ${gateway_addr}" >&2
    cat "$gateway_log" >&2 2>/dev/null || true
    exit 126
  fi
fi

start_ns="$(date +%s%N 2>/dev/null || printf '0')"
exec 3>&2
TIMEFORMAT=$'real_seconds=%3R\nuser_seconds=%3U\nsystem_seconds=%3S'
{ time bash "$evaluator_path" "$candidate_dir" "$run_dir" "$metrics_out" "$verdict_out" 2>&3; } 2>"$timing_out"
status=$?
exec 3>&-
end_ns="$(date +%s%N 2>/dev/null || printf '0')"

real_seconds="$(awk -F= '$1 == "real_seconds" { print $2 }' "$timing_out" 2>/dev/null || printf '0')"
user_seconds="$(awk -F= '$1 == "user_seconds" { print $2 }' "$timing_out" 2>/dev/null || printf '0')"
system_seconds="$(awk -F= '$1 == "system_seconds" { print $2 }' "$timing_out" 2>/dev/null || printf '0')"
real_seconds="${real_seconds:-0}"
user_seconds="${user_seconds:-0}"
system_seconds="${system_seconds:-0}"

wall_time_ms="$(awk -v start="$start_ns" -v end="$end_ns" -v real="$real_seconds" 'BEGIN {
  if (start > 0 && end >= start) {
    printf "%.3f", (end - start) / 1000000
  } else {
    printf "%.3f", real * 1000
  }
}')"
cpu_percent="$(awk -v user="$user_seconds" -v sys="$system_seconds" -v wall_ms="$wall_time_ms" 'BEGIN {
  if (wall_ms > 0) {
    printf "%.6f", (user + sys) / (wall_ms / 1000) * 100
  } else {
    printf "0"
  }
}')"

cat > "$resource_out" <<JSON
{
  "wall_time_ms": $wall_time_ms,
  "cpu_user_seconds": $user_seconds,
  "cpu_system_seconds": $system_seconds,
  "cpu_percent": $cpu_percent,
  "resource_metric_source": "container-wrapper"
}
JSON

exit "$status"
`
}

func wrapTasksetCommand(name string, args []string, cpuLimit int) (string, []string) {
	mask := "0"
	if cpuLimit > 1 {
		mask = fmt.Sprintf("0-%d", cpuLimit-1)
	}
	wrapped := []string{"-c", mask, name}
	wrapped = append(wrapped, args...)
	return "taskset", wrapped
}

func sandboxEnabled(opts SandboxOptions) bool {
	return opts.Engine == "docker" || opts.Engine == "podman"
}

func sandboxMount(path, access string) string {
	clean := filepath.Clean(path)
	return clean + ":" + clean + ":" + access
}

func sandboxHomeDir(runDir string) string {
	return filepath.Join(runDir, "tmp", "sandbox-home")
}
