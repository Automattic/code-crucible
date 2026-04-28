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
	return mode == model.ExternalModeMock ||
		mode == model.ExternalModeReplay ||
		mode == model.ExternalModeRecord
}

func mockGatewaySource() string {
	return `package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type fixtureSet struct {
	Version  int       ` + "`json:\"version\"`" + `
	Fixtures []fixture ` + "`json:\"fixtures\"`" + `
}

type fixture struct {
	ID       string   ` + "`json:\"id\"`" + `
	Request  request  ` + "`json:\"request\"`" + `
	Response response ` + "`json:\"response\"`" + `
}

type request struct {
	Method     string            ` + "`json:\"method\"`" + `
	URL        string            ` + "`json:\"url\"`" + `
	Headers    map[string]string ` + "`json:\"headers,omitempty\"`" + `
	BodySHA256 string            ` + "`json:\"body_sha256,omitempty\"`" + `
}

type response struct {
	Status  int               ` + "`json:\"status\"`" + `
	Headers map[string]string ` + "`json:\"headers,omitempty\"`" + `
	Body    string            ` + "`json:\"body,omitempty\"`" + `
}

type gateway struct {
	fixtures map[string]fixture
	signer   *certSigner
}

func main() {
	fixturesPath := flag.String("fixtures", "http-fixtures.json", "HTTP fixture JSON file")
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	caCertPath := flag.String("ca-cert", "", "CA certificate PEM for HTTPS CONNECT replay")
	caKeyPath := flag.String("ca-key", "", "CA private key PEM for HTTPS CONNECT replay")
	flag.Parse()

	fixtures, err := loadFixtures(*fixturesPath)
	if err != nil {
		log.Fatal(err)
	}
	var signer *certSigner
	if *caCertPath != "" || *caKeyPath != "" {
		signer, err = loadCertSigner(*caCertPath, *caKeyPath)
		if err != nil {
			log.Fatal(err)
		}
	}

	gateway := gateway{fixtures: fixtures, signer: signer}
	server := &http.Server{Addr: *addr, Handler: http.HandlerFunc(gateway.handle)}
	log.Printf("serving %d HTTP fixtures on %s", len(fixtures), *addr)
	log.Fatal(server.ListenAndServe())
}

func (g gateway) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		g.handleConnect(w, r)
		return
	}
	g.serveFixture(w, r)
}

func (g gateway) handleConnect(w http.ResponseWriter, r *http.Request) {
	if g.signer == nil {
		http.Error(w, "HTTPS CONNECT replay requires -ca-cert and -ca-key", http.StatusNotImplemented)
		return
	}
	targetHost := connectHost(r.Host)
	defaultCert, err := g.signer.certificate(targetHost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "response writer does not support hijacking", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("hijack CONNECT %s: %v", r.Host, err)
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = conn.Close()
		log.Printf("ack CONNECT %s: %v", r.Host, err)
		return
	}
	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{*defaultCert},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := hello.ServerName
			if host == "" {
				host = targetHost
			}
			return g.signer.certificate(host)
		},
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.Handshake(); err != nil {
		_ = tlsConn.Close()
		log.Printf("TLS handshake for CONNECT %s: %v", r.Host, err)
		return
	}
	go func() {
		listener := &singleConnListener{conn: tlsConn}
		server := &http.Server{Handler: http.HandlerFunc(g.serveFixture)}
		if err := server.Serve(listener); err != nil && err != io.EOF {
			log.Printf("serve CONNECT %s: %v", r.Host, err)
		}
	}()
}

func (g gateway) serveFixture(w http.ResponseWriter, r *http.Request) {
	key := strings.ToUpper(r.Method) + " " + requestURL(r)
	fixture, ok := g.fixtures[key]
	if !ok {
		http.Error(w, "no fixture for "+key, http.StatusNotFound)
		return
	}
	if err := validateRequest(r, fixture); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for name, value := range fixture.Response.Headers {
		w.Header().Set(name, value)
	}
	w.WriteHeader(fixture.Response.Status)
	_, _ = w.Write([]byte(fixture.Response.Body))
}

func loadFixtures(path string) (map[string]fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var set fixtureSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, err
	}
	out := map[string]fixture{}
	for _, fixture := range set.Fixtures {
		out[strings.ToUpper(fixture.Request.Method)+" "+fixture.Request.URL] = fixture
	}
	return out, nil
}

type certSigner struct {
	ca    *x509.Certificate
	key   *rsa.PrivateKey
	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func loadCertSigner(certPath, keyPath string) (*certSigner, error) {
	if certPath == "" || keyPath == "" {
		return nil, fmt.Errorf("-ca-cert and -ca-key must be provided together")
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid CA certificate PEM")
	}
	ca, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "RSA PRIVATE KEY" {
		return nil, fmt.Errorf("invalid CA private key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	return &certSigner{ca: ca, key: key, cache: map[string]*tls.Certificate{}}, nil
}

func (s *certSigner) certificate(host string) (*tls.Certificate, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "localhost"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cert, ok := s.cache[host]; ok {
		return cert, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, s.ca, &key.PublicKey, s.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{
		Certificate: [][]byte{der, s.ca.Raw},
		PrivateKey:  key,
		Leaf:        leaf,
	}
	s.cache[host] = cert
	return cert, nil
}

type singleConnListener struct {
	conn net.Conn
	used bool
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.used {
		return nil, io.EOF
	}
	l.used = true
	return l.conn, nil
}

func (l *singleConnListener) Close() error {
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

func connectHost(authority string) string {
	if host, _, err := net.SplitHostPort(authority); err == nil {
		return host
	}
	return strings.Trim(authority, "[]")
}

func validateRequest(r *http.Request, fixture fixture) error {
	for name, value := range fixture.Request.Headers {
		if got := r.Header.Get(name); got != value {
			return fmt.Errorf("request header %s = %q, want %q", name, got, value)
		}
	}
	if fixture.Request.BodySHA256 == "" {
		return nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, fixture.Request.BodySHA256) {
		return fmt.Errorf("request body_sha256 = %q, want %q", got, fixture.Request.BodySHA256)
	}
	return nil
}

func requestURL(r *http.Request) string {
	if r.URL != nil && r.URL.IsAbs() {
		return r.URL.String()
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, r.URL.RequestURI())
}
`
}
