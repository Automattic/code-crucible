package external

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/model"
)

const (
	HTTPFixtureVersion = 1
	HTTPFixturesName   = "http-fixtures.json"
	MockGatewayName    = "mock-gateway.go"
	MockCACertName     = "mock-ca.pem"
	MockCAKeyName      = "mock-ca-key.pem"
)

type HTTPFixtureSet struct {
	Version  int           `json:"version"`
	Fixtures []HTTPFixture `json:"fixtures"`
}

type HTTPFixture struct {
	ID       string              `json:"id"`
	Request  HTTPFixtureRequest  `json:"request"`
	Response HTTPFixtureResponse `json:"response"`
}

type HTTPFixtureRequest struct {
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Headers    map[string]string `json:"headers,omitempty"`
	BodySHA256 string            `json:"body_sha256,omitempty"`
}

type HTTPFixtureResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

func PrepareHTTPFixtures(projectDir, runDir string, policy model.ExternalPolicy) (model.ExternalPolicy, error) {
	if !needsHTTPFixtures(policy.Mode) && strings.TrimSpace(policy.Fixtures) == "" {
		return policy, nil
	}

	fixturesPath := filepath.Join(runDir, "external", HTTPFixturesName)
	if strings.TrimSpace(policy.Fixtures) == "" {
		if err := SaveHTTPFixtureSet(fixturesPath, EmptyHTTPFixtureSet()); err != nil {
			return policy, err
		}
	} else {
		source := policy.Fixtures
		if !filepath.IsAbs(source) {
			source = filepath.Join(projectDir, source)
		}
		fixtures, err := LoadHTTPFixtureSet(source)
		if err != nil {
			return policy, err
		}
		if err := SaveHTTPFixtureSet(fixturesPath, fixtures); err != nil {
			return policy, err
		}
	}

	if err := WriteMockGateway(filepath.Join(runDir, "external", MockGatewayName)); err != nil {
		return policy, err
	}
	if err := WriteMockCA(filepath.Join(runDir, "external", MockCACertName), filepath.Join(runDir, "external", MockCAKeyName)); err != nil {
		return policy, err
	}
	policy.Fixtures = filepath.ToSlash(fixturesPath)
	return policy, nil
}

func EmptyHTTPFixtureSet() HTTPFixtureSet {
	return HTTPFixtureSet{
		Version:  HTTPFixtureVersion,
		Fixtures: []HTTPFixture{},
	}
}

func LoadHTTPFixtureSet(path string) (HTTPFixtureSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return HTTPFixtureSet{}, err
	}
	var fixtures HTTPFixtureSet
	if err := json.Unmarshal(data, &fixtures); err != nil {
		return HTTPFixtureSet{}, fmt.Errorf("invalid HTTP fixture file %s: %w", path, err)
	}
	if err := ValidateHTTPFixtureSet(fixtures); err != nil {
		return HTTPFixtureSet{}, fmt.Errorf("invalid HTTP fixture file %s: %w", path, err)
	}
	return fixtures, nil
}

func SaveHTTPFixtureSet(path string, fixtures HTTPFixtureSet) error {
	if err := ValidateHTTPFixtureSet(fixtures); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fixtures, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func ValidateHTTPFixtureSet(fixtures HTTPFixtureSet) error {
	if fixtures.Version != HTTPFixtureVersion {
		return fmt.Errorf("version = %d, want %d", fixtures.Version, HTTPFixtureVersion)
	}
	seen := map[string]bool{}
	for i, fixture := range fixtures.Fixtures {
		if strings.TrimSpace(fixture.ID) == "" {
			return fmt.Errorf("fixtures[%d].id is required", i)
		}
		if seen[fixture.ID] {
			return fmt.Errorf("duplicate fixture id %q", fixture.ID)
		}
		seen[fixture.ID] = true
		if strings.TrimSpace(fixture.Request.Method) == "" {
			return fmt.Errorf("fixtures[%d].request.method is required", i)
		}
		if strings.TrimSpace(fixture.Request.URL) == "" {
			return fmt.Errorf("fixtures[%d].request.url is required", i)
		}
		if fixture.Response.Status < 100 || fixture.Response.Status > 599 {
			return fmt.Errorf("fixtures[%d].response.status must be an HTTP status code", i)
		}
	}
	return nil
}

func RequestBodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func WriteMockGateway(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(mockGatewaySource()), 0o644)
}

func WriteMockCA(certPath, keyPath string) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "Code Crucible Mock Replay CA",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, keyPEM, 0o600)
}

func needsHTTPFixtures(mode model.ExternalMode) bool {
	return mode == model.ExternalModeAllowlist ||
		mode == model.ExternalModeMock ||
		mode == model.ExternalModeReplay ||
		mode == model.ExternalModeRecord
}
