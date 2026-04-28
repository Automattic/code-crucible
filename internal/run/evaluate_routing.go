package run

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	externalfixtures "github.com/Automattic/code-crucible/internal/external"
	"github.com/Automattic/code-crucible/internal/model"
)

type containerExternalRouting struct {
	Report   *ContainerExternalRoutingReport
	Env      []string
	AddHosts []string
	Sandbox  SandboxOptions
	cleanup  func() []string
}

func setupContainerExternalRouting(policy model.ExternalPolicy, sandbox SandboxOptions, runDir, candidateDir string, env []string) (*containerExternalRouting, error) {
	if sandbox.ExternalRouting == "" {
		return nil, nil
	}
	if sandbox.ExternalRouting != ExternalRoutingGatewayNetwork {
		return nil, fmt.Errorf("unsupported external routing mode %q", sandbox.ExternalRouting)
	}
	if !sandboxEnabled(sandbox) {
		return nil, fmt.Errorf("--external-routing gateway-network requires Docker or Podman sandbox evaluation")
	}
	if !gatewayBackedMode(policy.Mode) {
		return nil, fmt.Errorf("--external-routing gateway-network requires external mode allowlist, mock, replay, or record")
	}

	hosts, err := declaredExternalHosts(policy, env)
	if err != nil {
		return nil, err
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("--external-routing gateway-network requires declared hosts from --allow-hosts or HTTP fixtures")
	}

	env, err = ensureSandboxMockGatewayBinary(env)
	if err != nil {
		return nil, err
	}
	gatewayBin := envValue(env, "CRUCIBLE_MOCK_GATEWAY_BIN")
	if gatewayBin == "" {
		return nil, fmt.Errorf("--external-routing gateway-network requires an archived mock gateway binary")
	}
	fixtures := envValue(env, "CRUCIBLE_HTTP_FIXTURES")
	if fixtures == "" {
		return nil, fmt.Errorf("--external-routing gateway-network requires CRUCIBLE_HTTP_FIXTURES")
	}

	nameBase := archiveSafeName(filepath.Base(candidateDir))
	networkName := "crucible-" + nameBase + "-" + strconvBase36(time.Now().UnixNano())
	gatewayName := networkName + "-gateway"
	report := &ContainerExternalRoutingReport{
		Mode:        sandbox.ExternalRouting,
		Engine:      sandbox.Engine,
		Image:       sandbox.Image,
		NetworkName: networkName,
		GatewayName: gatewayName,
		Hosts:       hosts,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := createRoutingNetwork(ctx, sandbox.Engine, networkName, policy.Mode); err != nil {
		return nil, err
	}
	cleanup := func() []string {
		var notes []string
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if output, err := runEngineCommand(cleanupCtx, sandbox.Engine, "rm", "-f", gatewayName); err != nil {
			message := fmt.Sprintf("%v: %s", err, strings.TrimSpace(string(output)))
			if !containerExists(cleanupCtx, sandbox.Engine, gatewayName) {
				notes = append(notes, fmt.Sprintf("gateway %s was already removed after cleanup error: %s", gatewayName, message))
			} else {
				notes = append(notes, fmt.Sprintf("remove gateway %s failed: %s", gatewayName, message))
			}
		} else {
			notes = append(notes, "removed gateway "+gatewayName)
		}
		if output, err := runEngineCommand(cleanupCtx, sandbox.Engine, "network", "rm", networkName); err != nil {
			notes = append(notes, fmt.Sprintf("remove network %s failed: %v: %s", networkName, err, strings.TrimSpace(string(output))))
		} else {
			notes = append(notes, "removed network "+networkName)
		}
		return notes
	}

	gatewayCommand := buildGatewaySidecarCommand(sandbox, networkName, gatewayName, runDir, env)
	report.GatewayCommand = append([]string{sandbox.Engine}, gatewayCommand...)
	if output, err := runEngineCommand(ctx, sandbox.Engine, gatewayCommand...); err != nil {
		notes := cleanup()
		report.Teardown = append(report.Teardown, notes...)
		return nil, fmt.Errorf("start gateway sidecar: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := waitForGatewaySidecar(ctx, sandbox.Engine, gatewayName); err != nil {
		notes := cleanup()
		report.Teardown = append(report.Teardown, notes...)
		return nil, err
	}
	ip, err := inspectGatewayIP(ctx, sandbox.Engine, gatewayName)
	if err != nil {
		notes := cleanup()
		report.Teardown = append(report.Teardown, notes...)
		return nil, err
	}
	report.GatewayIP = ip

	addHosts := make([]string, 0, len(hosts))
	for _, host := range hosts {
		addHosts = append(addHosts, host+":"+ip)
	}
	report.EvaluatorHostConfig = addHosts

	env = removeEnvKeys(env, "CRUCIBLE_MOCK_GATEWAY_SOURCE", "CRUCIBLE_MOCK_GATEWAY_BIN")
	env = setGatewayAddress(env, net.JoinHostPort(ip, "80"))
	sandbox.Network = networkName

	return &containerExternalRouting{
		Report:   report,
		Env:      env,
		AddHosts: addHosts,
		Sandbox:  sandbox,
		cleanup:  cleanup,
	}, nil
}

func containerExists(ctx context.Context, engine, name string) bool {
	output, err := runEngineCommand(ctx, engine, "inspect", name)
	return err == nil && strings.TrimSpace(string(output)) != ""
}

func createRoutingNetwork(ctx context.Context, engine, networkName string, mode model.ExternalMode) error {
	args := []string{"network", "create"}
	if mode == model.ExternalModeMock || mode == model.ExternalModeReplay {
		args = append(args, "--internal")
	}
	args = append(args, networkName)
	output, err := runEngineCommand(ctx, engine, args...)
	if err != nil {
		return fmt.Errorf("create evaluator network: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func buildGatewaySidecarCommand(sandbox SandboxOptions, networkName, gatewayName, runDir string, env []string) []string {
	args := []string{
		"run",
		"-d",
		"--rm",
		"--name", gatewayName,
		"--network", networkName,
		"--volume", sandboxMount(runDir, "rw"),
		"--workdir", runDir,
		"--cap-add", "NET_BIND_SERVICE",
	}
	if sandbox.Engine == "podman" {
		args = append(args, "--userns", "keep-id")
	} else if uid := os.Getuid(); uid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, os.Getgid()))
	}
	args = append(args, sandbox.Image, envValue(env, "CRUCIBLE_MOCK_GATEWAY_BIN"),
		"-fixtures", envValue(env, "CRUCIBLE_HTTP_FIXTURES"),
		"-addr", "0.0.0.0:80",
	)
	if mode := envValue(env, "CRUCIBLE_EXTERNAL_MODE"); mode != "" {
		args = append(args, "-mode", mode)
	}
	if tracePath := envValue(env, "CRUCIBLE_EXTERNAL_TRACE"); tracePath != "" {
		args = append(args, "-trace", tracePath)
	}
	if allowed := envValue(env, "CRUCIBLE_ALLOWED_HOSTS"); allowed != "" {
		args = append(args, "-allow-hosts", allowed, "-passthrough")
	}
	if recordPath := envValue(env, "CRUCIBLE_RECORD_FIXTURES"); recordPath != "" {
		args = append(args, "-record-fixtures", recordPath, "-passthrough")
	}
	if cert, key := envValue(env, "CRUCIBLE_MOCK_CA_CERT"), envValue(env, "CRUCIBLE_MOCK_CA_KEY"); cert != "" && key != "" {
		args = append(args, "-ca-cert", cert, "-ca-key", key, "-tls-addr", "0.0.0.0:443")
	}
	return args
}

func waitForGatewaySidecar(ctx context.Context, engine, gatewayName string) error {
	deadline := time.Now().Add(20 * time.Second)
	for {
		output, err := runEngineCommand(ctx, engine, "exec", gatewayName, "bash", "-c", ": >/dev/tcp/127.0.0.1/80")
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gateway sidecar did not become ready: %w: %s", err, strings.TrimSpace(string(output)))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func inspectGatewayIP(ctx context.Context, engine, gatewayName string) (string, error) {
	output, err := runEngineCommand(ctx, engine, "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", gatewayName)
	if err != nil {
		return "", fmt.Errorf("inspect gateway sidecar: %w: %s", err, strings.TrimSpace(string(output)))
	}
	ip := strings.TrimSpace(string(output))
	if parsed := net.ParseIP(ip); parsed == nil {
		return "", fmt.Errorf("inspect gateway sidecar returned invalid IP %q", ip)
	}
	return ip, nil
}

func declaredExternalHosts(policy model.ExternalPolicy, env []string) ([]string, error) {
	seen := map[string]bool{}
	var hosts []string
	add := func(host string) {
		host = normalizeRoutingHost(host)
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	for _, host := range policy.Allowlist {
		add(host)
	}
	for _, host := range strings.Split(envValue(env, "CRUCIBLE_ALLOWED_HOSTS"), ",") {
		add(host)
	}
	fixturesPath := envValue(env, "CRUCIBLE_HTTP_FIXTURES")
	if fixturesPath != "" && fileExists(filepath.FromSlash(fixturesPath)) {
		fixtures, err := externalfixtures.LoadHTTPFixtureSet(filepath.FromSlash(fixturesPath))
		if err != nil {
			return nil, err
		}
		for _, fixture := range fixtures.Fixtures {
			if parsed, err := url.Parse(fixture.Request.URL); err == nil {
				add(parsed.Host)
			}
		}
	}
	return hosts, nil
}

func normalizeRoutingHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https://")
	if slash := strings.Index(value, "/"); slash >= 0 {
		value = value[:slash]
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if net.ParseIP(value) != nil {
		return ""
	}
	return value
}

func removeEnvKeys(env []string, keys ...string) []string {
	blocked := map[string]bool{}
	for _, key := range keys {
		blocked[key] = true
	}
	out := env[:0]
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok && blocked[key] {
			continue
		}
		out = append(out, item)
	}
	return out
}

func runEngineCommand(ctx context.Context, engine string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, engine, args...)
	return cmd.CombinedOutput()
}

func archiveSafeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "candidate"
	}
	if len(out) > 32 {
		out = strings.Trim(out[:32], "-")
	}
	return out
}

func strconvBase36(value int64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if value < 0 {
		value = -value
	}
	if value == 0 {
		return "0"
	}
	var out []byte
	for value > 0 {
		out = append(out, digits[value%36])
		value /= 36
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
