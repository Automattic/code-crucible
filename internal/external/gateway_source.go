package external

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
