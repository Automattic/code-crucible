package run

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	externalfixtures "github.com/Automattic/code-crucible/internal/external"
	"github.com/Automattic/code-crucible/internal/model"
)

const (
	sandboxProfileDefault   = "default"
	sandboxProfileStrict    = "strict"
	sandboxProfileNetworked = "networked"

	defaultSandboxMemoryLimit = "1g"
	defaultSandboxPIDsLimit   = 256
)

func NormalizeSandboxOptions(opts SandboxOptions) (SandboxOptions, error) {
	opts.Profile = strings.TrimSpace(opts.Profile)
	opts.Engine = strings.TrimSpace(opts.Engine)
	opts.Image = strings.TrimSpace(opts.Image)
	opts.Network = strings.TrimSpace(opts.Network)
	opts.MemoryLimit = strings.TrimSpace(opts.MemoryLimit)

	if opts.Profile == "" {
		opts.Profile = sandboxProfileDefault
	}
	switch opts.Profile {
	case sandboxProfileDefault, sandboxProfileStrict, sandboxProfileNetworked:
	default:
		return SandboxOptions{}, fmt.Errorf("--sandbox-profile must be default, strict, or networked")
	}

	if opts.Engine == "" {
		opts.Engine = "local"
	}
	if opts.PIDsLimit < 0 {
		return SandboxOptions{}, fmt.Errorf("--pids-limit must be at least 0")
	}
	if strings.IndexFunc(opts.MemoryLimit, unicode.IsSpace) >= 0 {
		return SandboxOptions{}, fmt.Errorf("--memory-limit cannot contain whitespace")
	}

	switch opts.Engine {
	case "local":
		if opts.Image != "" {
			return SandboxOptions{}, fmt.Errorf("--sandbox-image requires --sandbox-engine docker or podman")
		}
		if opts.Network != "" {
			return SandboxOptions{}, fmt.Errorf("--sandbox-network requires --sandbox-engine docker or podman")
		}
		if opts.MemoryLimit != "" {
			return SandboxOptions{}, fmt.Errorf("--memory-limit requires --sandbox-engine docker or podman")
		}
		if opts.PIDsLimit > 0 {
			return SandboxOptions{}, fmt.Errorf("--pids-limit requires --sandbox-engine docker or podman")
		}
		if opts.Profile != sandboxProfileDefault {
			return SandboxOptions{}, fmt.Errorf("--sandbox-profile %s requires --sandbox-engine docker or podman", opts.Profile)
		}
		return SandboxOptions{Profile: sandboxProfileDefault, Engine: "local"}, nil
	case "docker", "podman":
		if opts.Image == "" {
			return SandboxOptions{}, fmt.Errorf("--sandbox-image is required when --sandbox-engine is %s", opts.Engine)
		}
		applySandboxProfileDefaults(&opts)
		if opts.Profile == sandboxProfileStrict && opts.Network != "none" {
			return SandboxOptions{}, fmt.Errorf("--sandbox-profile strict requires --sandbox-network none")
		}
		if opts.Network == "" {
			opts.Network = "none"
		}
		return opts, nil
	default:
		return SandboxOptions{}, fmt.Errorf("--sandbox-engine must be local, docker, or podman")
	}
}

func applySandboxProfileDefaults(opts *SandboxOptions) {
	switch opts.Profile {
	case sandboxProfileStrict:
		if opts.Network == "" {
			opts.Network = "none"
		}
		if opts.MemoryLimit == "" {
			opts.MemoryLimit = defaultSandboxMemoryLimit
		}
		if opts.PIDsLimit == 0 {
			opts.PIDsLimit = defaultSandboxPIDsLimit
		}
	case sandboxProfileNetworked:
		if opts.Network == "" {
			opts.Network = "bridge"
		}
		if opts.MemoryLimit == "" {
			opts.MemoryLimit = defaultSandboxMemoryLimit
		}
		if opts.PIDsLimit == 0 {
			opts.PIDsLimit = defaultSandboxPIDsLimit
		}
	}
}

func externalPolicyEnforcement(policy model.ExternalPolicy, sandbox SandboxOptions) model.ExternalPolicyEnforcement {
	enforcement := model.ExternalPolicyEnforcement{
		Mode:   policy.Mode,
		Status: "advisory",
	}

	if sandboxEnabled(sandbox) {
		enforcement.Mechanism = sandbox.Engine + "-network-" + sandbox.Network
	} else {
		enforcement.Mechanism = "evaluator-contract"
	}

	switch policy.Mode {
	case model.ExternalModeDeny:
		if sandboxEnabled(sandbox) {
			if sandbox.Network == "none" {
				enforcement.Status = "enforced"
				return enforcement
			}
			enforcement.Status = "failed"
			enforcement.Errors = append(enforcement.Errors, "external policy deny requires --sandbox-network none when using a container sandbox")
			return enforcement
		}
		enforcement.Warnings = append(enforcement.Warnings, "external policy deny is advisory in local mode; use --sandbox-engine docker or podman with --sandbox-network none to enforce network isolation")
	case model.ExternalModeMock, model.ExternalModeReplay:
		if sandboxEnabled(sandbox) && sandbox.Network == "none" {
			enforcement.Status = "partial"
			enforcement.Warnings = append(enforcement.Warnings, "live network is blocked and proxy-based fixture replay is available inside the sandbox; clients that ignore proxy or trust environment variables still require evaluator configuration")
			return enforcement
		}
		enforcement.Warnings = append(enforcement.Warnings, "fixture gateway and proxy environment are available when fixtures are archived, but network isolation requires a container sandbox with --sandbox-network none")
	case model.ExternalModeAllowlist:
		if len(normalizedAllowlist(policy.Allowlist)) == 0 {
			enforcement.Status = "failed"
			enforcement.Errors = append(enforcement.Errors, "external policy allowlist requires at least one --allow-hosts entry")
			return enforcement
		}
		enforcement.Status = "partial"
		enforcement.Mechanism = "proxy-allowlist"
		enforcement.Warnings = append(enforcement.Warnings, "allowlist policy is enforced for HTTP and HTTPS clients that honor proxy environment variables; clients that ignore proxy variables still require evaluator-specific isolation")
		if sandboxEnabled(sandbox) && sandbox.Network == "none" {
			enforcement.Warnings = append(enforcement.Warnings, "allowlisted live hosts may be unreachable when the container sandbox network is none")
		}
	case model.ExternalModeRecord:
		enforcement.Status = "partial"
		enforcement.Mechanism = "proxy-record"
		enforcement.Warnings = append(enforcement.Warnings, "record mode captures HTTP proxy traffic and CONNECT metadata; clients that ignore proxy variables or opaque HTTPS tunnels still require evaluator-specific recording support")
	default:
		enforcement.Warnings = append(enforcement.Warnings, "external policy mode is not recognized by the enforcement layer")
	}

	return enforcement
}

func externalEvaluationEnv(policy model.ExternalPolicy, runDir string, env []string) []string {
	out := append([]string(nil), env...)
	out = appendEnvDefault(out, "CRUCIBLE_EXTERNAL_MODE", string(policy.Mode))

	if !fixtureBackedMode(policy.Mode) && !gatewayBackedMode(policy.Mode) {
		return out
	}

	fixturesPath := strings.TrimSpace(policy.Fixtures)
	if fixturesPath == "" {
		candidatePath := filepath.Join(runDir, "external", externalfixtures.HTTPFixturesName)
		if fileExists(candidatePath) {
			fixturesPath = candidatePath
		}
	}
	if fixturesPath != "" {
		out = appendEnvDefault(out, "CRUCIBLE_HTTP_FIXTURES", filepath.ToSlash(fixturesPath))
	}
	if !gatewayBackedMode(policy.Mode) {
		return out
	}

	if policy.Mode == model.ExternalModeAllowlist {
		out = appendEnvDefault(out, "CRUCIBLE_ALLOWED_HOSTS", strings.Join(normalizedAllowlist(policy.Allowlist), ","))
	}
	gatewaySource := filepath.Join(runDir, "external", externalfixtures.MockGatewayName)
	if fileExists(gatewaySource) {
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_GATEWAY_SOURCE", filepath.ToSlash(gatewaySource))
	}
	caCertPath := filepath.Join(runDir, "external", externalfixtures.MockCACertName)
	caKeyPath := filepath.Join(runDir, "external", externalfixtures.MockCAKeyName)
	if fileExists(caCertPath) && fileExists(caKeyPath) {
		caCertPath = filepath.ToSlash(caCertPath)
		caKeyPath = filepath.ToSlash(caKeyPath)
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_CA_CERT", caCertPath)
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_CA_KEY", caKeyPath)
		out = appendEnvDefault(out, "SSL_CERT_FILE", caCertPath)
		out = appendEnvDefault(out, "REQUESTS_CA_BUNDLE", caCertPath)
		out = appendEnvDefault(out, "CURL_CA_BUNDLE", caCertPath)
		out = appendEnvDefault(out, "NODE_EXTRA_CA_CERTS", caCertPath)
		out = appendEnvDefault(out, "GIT_SSL_CAINFO", caCertPath)
	}

	gatewayAddr := envValue(out, "CRUCIBLE_MOCK_GATEWAY_ADDR")
	if gatewayAddr == "" {
		gatewayAddr = "127.0.0.1:18080"
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_GATEWAY_ADDR", gatewayAddr)
	}
	gatewayURL := envValue(out, "CRUCIBLE_MOCK_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://" + gatewayAddr
		out = appendEnvDefault(out, "CRUCIBLE_MOCK_GATEWAY_URL", gatewayURL)
	}
	out = appendEnvDefault(out, "HTTP_PROXY", gatewayURL)
	out = appendEnvDefault(out, "http_proxy", gatewayURL)
	out = appendEnvDefault(out, "HTTPS_PROXY", gatewayURL)
	out = appendEnvDefault(out, "https_proxy", gatewayURL)
	out = appendEnvDefault(out, "NO_PROXY", "localhost,127.0.0.1,::1")
	out = appendEnvDefault(out, "no_proxy", "localhost,127.0.0.1,::1")
	return out
}

func fixtureBackedMode(mode model.ExternalMode) bool {
	return mode == model.ExternalModeMock ||
		mode == model.ExternalModeReplay ||
		mode == model.ExternalModeRecord
}

func gatewayBackedMode(mode model.ExternalMode) bool {
	return mode == model.ExternalModeAllowlist ||
		mode == model.ExternalModeMock ||
		mode == model.ExternalModeReplay ||
		mode == model.ExternalModeRecord
}

func externalCandidateEnv(policy model.ExternalPolicy, sandbox SandboxOptions, candidateDir string, env []string) ([]string, string, string) {
	out := append([]string(nil), env...)
	if !gatewayBackedMode(policy.Mode) {
		return out, "", ""
	}

	tracePath := filepath.Join(candidateDir, "external-trace.json")
	out = upsertEnv(out, "CRUCIBLE_EXTERNAL_TRACE", filepath.ToSlash(tracePath))

	recordFixturesPath := ""
	if policy.Mode == model.ExternalModeRecord {
		recordFixturesPath = filepath.Join(candidateDir, "recorded-http-fixtures.json")
		out = upsertEnv(out, "CRUCIBLE_RECORD_FIXTURES", filepath.ToSlash(recordFixturesPath))
	}

	if !sandboxEnabled(sandbox) && envValue(out, "CRUCIBLE_MOCK_GATEWAY_SOURCE") != "" {
		addr := envValue(out, "CRUCIBLE_MOCK_GATEWAY_ADDR")
		if addr == "" || addr == "127.0.0.1:18080" {
			if candidateAddr, err := freeLocalGatewayAddr(); err == nil {
				out = setGatewayAddress(out, candidateAddr)
			}
		}
	}
	return out, tracePath, recordFixturesPath
}

func normalizedAllowlist(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	return out
}

func appendEnvDefault(env []string, key, value string) []string {
	if envValue(env, key) != "" {
		return env
	}
	return append(env, key+"="+value)
}

func upsertEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func setGatewayAddress(env []string, addr string) []string {
	gatewayURL := "http://" + addr
	env = upsertEnv(env, "CRUCIBLE_MOCK_GATEWAY_ADDR", addr)
	env = upsertEnv(env, "CRUCIBLE_MOCK_GATEWAY_URL", gatewayURL)
	env = upsertEnv(env, "HTTP_PROXY", gatewayURL)
	env = upsertEnv(env, "http_proxy", gatewayURL)
	env = upsertEnv(env, "HTTPS_PROXY", gatewayURL)
	env = upsertEnv(env, "https_proxy", gatewayURL)
	return env
}

func freeLocalGatewayAddr() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	return listener.Addr().String(), nil
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func startLocalMockGateway(ctx context.Context, env []string, logPath string) (func(), error) {
	source := envValue(env, "CRUCIBLE_MOCK_GATEWAY_SOURCE")
	binary := envValue(env, "CRUCIBLE_MOCK_GATEWAY_BIN")
	if source == "" && binary == "" {
		return func() {}, nil
	}
	fixtures := envValue(env, "CRUCIBLE_HTTP_FIXTURES")
	if fixtures == "" {
		return func() {}, fmt.Errorf("CRUCIBLE_HTTP_FIXTURES is required when the mock gateway is enabled")
	}
	addr := envValue(env, "CRUCIBLE_MOCK_GATEWAY_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18080"
	}

	name := binary
	args := []string{"-fixtures", fixtures, "-addr", addr}
	if name == "" {
		name = "go"
		args = append([]string{"run", source}, args...)
	}
	mode := envValue(env, "CRUCIBLE_EXTERNAL_MODE")
	if mode != "" {
		args = append(args, "-mode", mode)
	}
	if tracePath := envValue(env, "CRUCIBLE_EXTERNAL_TRACE"); tracePath != "" {
		args = append(args, "-trace", tracePath)
	}
	if mode == string(model.ExternalModeAllowlist) {
		allowedHosts := envValue(env, "CRUCIBLE_ALLOWED_HOSTS")
		if allowedHosts == "" {
			return func() {}, fmt.Errorf("CRUCIBLE_ALLOWED_HOSTS is required in allowlist mode")
		}
		args = append(args, "-allow-hosts", allowedHosts, "-passthrough")
	}
	if mode == string(model.ExternalModeRecord) {
		recordPath := envValue(env, "CRUCIBLE_RECORD_FIXTURES")
		if recordPath == "" {
			return func() {}, fmt.Errorf("CRUCIBLE_RECORD_FIXTURES is required in record mode")
		}
		args = append(args, "-record-fixtures", recordPath, "-passthrough")
	}
	caCert := envValue(env, "CRUCIBLE_MOCK_CA_CERT")
	caKey := envValue(env, "CRUCIBLE_MOCK_CA_KEY")
	if caCert != "" || caKey != "" {
		if caCert == "" || caKey == "" {
			return func() {}, fmt.Errorf("CRUCIBLE_MOCK_CA_CERT and CRUCIBLE_MOCK_CA_KEY must be provided together")
		}
		args = append(args, "-ca-cert", caCert, "-ca-key", caKey)
	}
	if tcpAddressOpen(addr) {
		return func() {}, fmt.Errorf("local mock gateway address is already in use: %s", addr)
	}

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return func() {}, err
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return func() {}, err
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return func() {}, fmt.Errorf("start local mock gateway: %w", err)
	}

	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		_ = logFile.Close()
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			cleanup()
			return func() {}, err
		}
		if tcpAddressOpen(addr) {
			return cleanup, nil
		}
		if time.Now().After(deadline) {
			cleanup()
			return func() {}, fmt.Errorf("local mock gateway did not become ready on %s; see %s", addr, logPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func tcpAddressOpen(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
