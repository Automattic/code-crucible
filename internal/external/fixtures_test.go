package external

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Automattic/code-crucible/internal/model"
)

func TestPrepareHTTPFixturesCreatesTemplate(t *testing.T) {
	projectDir := t.TempDir()
	runDir := filepath.Join(projectDir, ".crucible", "runs", "run")

	policy, err := PrepareHTTPFixtures(projectDir, runDir, model.ExternalPolicy{
		Mode: model.ExternalModeReplay,
	})
	if err != nil {
		t.Fatal(err)
	}

	wantFixtures := filepath.Join(runDir, "external", HTTPFixturesName)
	if policy.Fixtures != filepath.ToSlash(wantFixtures) {
		t.Fatalf("fixtures path = %q, want %q", policy.Fixtures, filepath.ToSlash(wantFixtures))
	}
	fixtures, err := LoadHTTPFixtureSet(wantFixtures)
	if err != nil {
		t.Fatal(err)
	}
	if fixtures.Version != HTTPFixtureVersion || len(fixtures.Fixtures) != 0 {
		t.Fatalf("fixtures = %#v, want empty v%d set", fixtures, HTTPFixtureVersion)
	}
	if _, err := os.Stat(filepath.Join(runDir, "external", MockGatewayName)); err != nil {
		t.Fatalf("mock gateway was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "external", MockCACertName)); err != nil {
		t.Fatalf("mock CA certificate was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "external", MockCAKeyName)); err != nil {
		t.Fatalf("mock CA key was not written: %v", err)
	}
}

func TestPrepareHTTPFixturesCopiesAndValidatesExistingFixtures(t *testing.T) {
	projectDir := t.TempDir()
	sourcePath := filepath.Join(projectDir, "fixtures.json")
	if err := SaveHTTPFixtureSet(sourcePath, HTTPFixtureSet{
		Version: HTTPFixtureVersion,
		Fixtures: []HTTPFixture{
			{
				ID: "users",
				Request: HTTPFixtureRequest{
					Method: "GET",
					URL:    "https://api.example.com/users",
				},
				Response: HTTPFixtureResponse{
					Status: 200,
					Headers: map[string]string{
						"content-type": "application/json",
					},
					Body: "[]",
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	runDir := filepath.Join(projectDir, ".crucible", "runs", "run")
	policy, err := PrepareHTTPFixtures(projectDir, runDir, model.ExternalPolicy{
		Mode:     model.ExternalModeMock,
		Fixtures: filepath.Base(sourcePath),
	})
	if err != nil {
		t.Fatal(err)
	}

	fixtures, err := LoadHTTPFixtureSet(policy.Fixtures)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Fixtures) != 1 || fixtures.Fixtures[0].ID != "users" {
		t.Fatalf("fixtures = %#v, want copied users fixture", fixtures)
	}
}

func TestValidateHTTPFixtureSetRejectsInvalidFixture(t *testing.T) {
	err := ValidateHTTPFixtureSet(HTTPFixtureSet{
		Version: HTTPFixtureVersion,
		Fixtures: []HTTPFixture{
			{
				ID: "bad",
				Request: HTTPFixtureRequest{
					Method: "GET",
				},
				Response: HTTPFixtureResponse{
					Status: 200,
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRequestBodySHA256(t *testing.T) {
	got := RequestBodySHA256([]byte("body"))
	want := "230d8358dc8e8890b4c58deeb62912ee2f20357ae92a5cc861b98e68fe31acb5"
	if got != want {
		t.Fatalf("sha256 = %q, want %q", got, want)
	}
}

func TestWriteMockCA(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, MockCACertName)
	keyPath := filepath.Join(dir, MockCAKeyName)
	if err := WriteMockCA(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("certificate PEM did not decode")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.IsCA {
		t.Fatalf("IsCA = false, want true")
	}
	if time.Until(cert.NotAfter) < 365*24*time.Hour {
		t.Fatalf("certificate expires too soon: %s", cert.NotAfter)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("key permissions = %o, want 600", got)
	}
}

func TestMockGatewaySourceParses(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, MockGatewayName, mockGatewaySource(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&types.Config{Importer: importer.Default()}).Check("mockgateway", fset, []*ast.File{file}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMockGatewaySourceIncludesProxyRouting(t *testing.T) {
	source := mockGatewaySource()
	for _, want := range []string{
		"r.URL.IsAbs()",
		"http.MethodConnect",
		"tls.Server",
		"loadCertSigner",
		"singleConnListener",
		"tls-addr",
		"allow-hosts",
		"proxyConnect",
		"traceSummary",
		"record-fixtures",
		"X-Crucible-Target-URL",
		"/__crucible/",
		"BodySHA256",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("mock gateway source missing %q", want)
		}
	}
}

func TestGeneratedMockGatewayServesDirectRoutedFixture(t *testing.T) {
	dir := t.TempDir()
	gatewayPath := filepath.Join(dir, MockGatewayName)
	if err := WriteMockGateway(gatewayPath); err != nil {
		t.Fatal(err)
	}
	fixturesPath := filepath.Join(dir, HTTPFixturesName)
	if err := SaveHTTPFixtureSet(fixturesPath, HTTPFixtureSet{
		Version: HTTPFixtureVersion,
		Fixtures: []HTTPFixture{
			{
				ID: "direct-users",
				Request: HTTPFixtureRequest{
					Method: "GET",
					URL:    "http://api.example.test/users?active=1",
				},
				Response: HTTPFixtureResponse{
					Status: 200,
					Headers: map[string]string{
						"content-type": "application/json",
					},
					Body: "[]",
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	addr := freeLocalAddress(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", gatewayPath, "-fixtures", fixturesPath, "-addr", addr)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	waitForTCP(t, addr, &stderr)

	resp, err := http.Get("http://" + addr + "/__crucible/http/api.example.test/users?active=1")
	if err != nil {
		t.Fatalf("direct routed GET failed: %v\nstderr:\n%s", err, stderr.String())
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(body) != "[]" {
		t.Fatalf("direct response = %d %q, want 200 []\nstderr:\n%s", resp.StatusCode, body, stderr.String())
	}
}

func TestGeneratedMockGatewayRecordsHTTPFixtureAndTrace(t *testing.T) {
	dir := t.TempDir()
	gatewayPath := filepath.Join(dir, MockGatewayName)
	if err := WriteMockGateway(gatewayPath); err != nil {
		t.Fatal(err)
	}
	fixturesPath := filepath.Join(dir, HTTPFixturesName)
	if err := SaveHTTPFixtureSet(fixturesPath, EmptyHTTPFixtureSet()); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	tracePath := filepath.Join(dir, "external-trace.json")
	recordPath := filepath.Join(dir, "recorded-http-fixtures.json")
	addr := freeLocalAddress(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", gatewayPath,
		"-fixtures", fixturesPath,
		"-addr", addr,
		"-mode", "record",
		"-trace", tracePath,
		"-record-fixtures", recordPath,
		"-passthrough",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	waitForTCP(t, addr, &stderr)

	proxyURL, err := url.Parse("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
	resp, err := client.Get(upstream.URL + "/record-me")
	if err != nil {
		t.Fatalf("record GET through proxy failed: %v\nstderr:\n%s", err, stderr.String())
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("record response = %d %q, want 200 JSON\nstderr:\n%s", resp.StatusCode, body, stderr.String())
	}

	recorded, err := LoadHTTPFixtureSet(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded.Fixtures) != 1 {
		t.Fatalf("recorded fixtures = %d, want 1", len(recorded.Fixtures))
	}
	if recorded.Fixtures[0].Request.URL != upstream.URL+"/record-me" {
		t.Fatalf("recorded URL = %q, want upstream URL", recorded.Fixtures[0].Request.URL)
	}
	if recorded.Fixtures[0].Response.Body != `{"ok":true}` {
		t.Fatalf("recorded body = %q, want JSON", recorded.Fixtures[0].Response.Body)
	}

	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	var trace struct {
		Mode          string   `json:"mode"`
		RequestCount  int      `json:"request_count"`
		UniqueHosts   []string `json:"unique_hosts"`
		BytesReceived int64    `json:"bytes_received"`
	}
	if err := json.Unmarshal(traceData, &trace); err != nil {
		t.Fatal(err)
	}
	if trace.Mode != "record" || trace.RequestCount != 1 || trace.BytesReceived == 0 {
		t.Fatalf("trace = %#v, want one recorded request", trace)
	}
	if len(trace.UniqueHosts) != 1 || trace.UniqueHosts[0] != upstreamURL.Hostname() {
		t.Fatalf("trace hosts = %#v, want %s", trace.UniqueHosts, upstreamURL.Hostname())
	}
}

func TestGeneratedMockGatewayAllowlistProxyForHTTP(t *testing.T) {
	dir := t.TempDir()
	gatewayPath := filepath.Join(dir, MockGatewayName)
	if err := WriteMockGateway(gatewayPath); err != nil {
		t.Fatal(err)
	}
	fixturesPath := filepath.Join(dir, HTTPFixturesName)
	if err := SaveHTTPFixtureSet(fixturesPath, EmptyHTTPFixtureSet()); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/allowed" {
			t.Fatalf("path = %q, want /allowed", r.URL.Path)
		}
		w.Header().Set("content-type", "text/plain")
		_, _ = w.Write([]byte("allowed"))
	}))
	t.Cleanup(upstream.Close)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	addr := freeLocalAddress(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", gatewayPath, "-fixtures", fixturesPath, "-addr", addr, "-allow-hosts", upstreamURL.Hostname(), "-passthrough")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	waitForTCP(t, addr, &stderr)

	proxyURL, err := url.Parse("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
	resp, err := client.Get(upstream.URL + "/allowed")
	if err != nil {
		t.Fatalf("allowed GET through proxy failed: %v\nstderr:\n%s", err, stderr.String())
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(body) != "allowed" {
		t.Fatalf("allowed response = %d %q, want 200 allowed\nstderr:\n%s", resp.StatusCode, body, stderr.String())
	}

	resp, err = client.Get("http://blocked.example.test/")
	if err != nil {
		t.Fatalf("blocked GET through proxy failed unexpectedly: %v\nstderr:\n%s", err, stderr.String())
	}
	body, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "host not in allowlist") {
		t.Fatalf("blocked response = %d %q, want allowlist denial", resp.StatusCode, body)
	}
}

func TestGeneratedMockGatewayServesHTTPSFixtureThroughConnect(t *testing.T) {
	dir := t.TempDir()
	gatewayPath := filepath.Join(dir, MockGatewayName)
	if err := WriteMockGateway(gatewayPath); err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, MockCACertName)
	keyPath := filepath.Join(dir, MockCAKeyName)
	if err := WriteMockCA(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	fixturesPath := filepath.Join(dir, HTTPFixturesName)
	if err := SaveHTTPFixtureSet(fixturesPath, HTTPFixtureSet{
		Version: HTTPFixtureVersion,
		Fixtures: []HTTPFixture{
			{
				ID: "users",
				Request: HTTPFixtureRequest{
					Method: "GET",
					URL:    "https://api.example.test/users",
				},
				Response: HTTPFixtureResponse{
					Status: 200,
					Headers: map[string]string{
						"content-type": "application/json",
					},
					Body: "[]",
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	addr := freeLocalAddress(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", gatewayPath, "-fixtures", fixturesPath, "-addr", addr, "-ca-cert", certPath, "-ca-key", keyPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	waitForTCP(t, addr, &stderr)

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("mock CA did not append to root pool")
	}
	proxyURL, err := url.Parse("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs:    roots,
				MinVersion: tls.VersionTLS12,
			},
		},
	}
	resp, err := client.Get("https://api.example.test/users")
	if err != nil {
		t.Fatalf("GET through CONNECT failed: %v\nstderr:\n%s", err, stderr.String())
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(body) != "[]" {
		t.Fatalf("response = %d %q, want 200 []\nstderr:\n%s", resp.StatusCode, body, stderr.String())
	}
}

func freeLocalAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func waitForTCP(t *testing.T, addr string, stderr *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("gateway did not listen on %s\nstderr:\n%s", addr, stderr.String())
}
