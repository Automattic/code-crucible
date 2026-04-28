package external

func mockGatewaySource() string {
	return `package main

import (
	"bytes"
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
	"sort"
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

type traceSummary struct {
	Mode          string       ` + "`json:\"mode,omitempty\"`" + `
	RequestCount  int          ` + "`json:\"request_count\"`" + `
	UniqueHosts   []string     ` + "`json:\"unique_hosts,omitempty\"`" + `
	BytesSent     int64        ` + "`json:\"bytes_sent,omitempty\"`" + `
	BytesReceived int64        ` + "`json:\"bytes_received,omitempty\"`" + `
	FailureCount  int          ` + "`json:\"failure_count,omitempty\"`" + `
	Events        []traceEvent ` + "`json:\"events,omitempty\"`" + `
}

type traceEvent struct {
	Method        string ` + "`json:\"method\"`" + `
	URL           string ` + "`json:\"url\"`" + `
	Host          string ` + "`json:\"host,omitempty\"`" + `
	Status        int    ` + "`json:\"status\"`" + `
	BytesSent     int64  ` + "`json:\"bytes_sent,omitempty\"`" + `
	BytesReceived int64  ` + "`json:\"bytes_received,omitempty\"`" + `
	Error         string ` + "`json:\"error,omitempty\"`" + `
	Recorded      bool   ` + "`json:\"recorded,omitempty\"`" + `
}

type traceRecorder struct {
	mu     sync.Mutex
	path   string
	mode   string
	events []traceEvent
	hosts  map[string]bool
}

type gateway struct {
	fixtures    map[string]fixture
	signer      *certSigner
	allowlist  []string
	passthrough bool
	transport   http.RoundTripper
	trace       *traceRecorder
	recordPath  string
	recordMu    sync.Mutex
	recorded    []fixture
}

func main() {
	fixturesPath := flag.String("fixtures", "http-fixtures.json", "HTTP fixture JSON file")
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	mode := flag.String("mode", "", "external policy mode for trace output")
	tracePath := flag.String("trace", "", "external trace summary output path")
	recordFixturesPath := flag.String("record-fixtures", "", "recorded HTTP fixtures output path")
	caCertPath := flag.String("ca-cert", "", "CA certificate PEM for HTTPS CONNECT replay")
	caKeyPath := flag.String("ca-key", "", "CA private key PEM for HTTPS CONNECT replay")
	tlsAddr := flag.String("tls-addr", "", "HTTPS listen address for transparent container routing")
	allowHosts := flag.String("allow-hosts", "", "comma-separated host allowlist for passthrough proxy mode")
	passthrough := flag.Bool("passthrough", false, "forward allowlisted proxy traffic to live upstream hosts")
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

	allowlist := parseAllowlist(*allowHosts)
	gateway := gateway{
		fixtures:    fixtures,
		signer:      signer,
		allowlist:  allowlist,
		passthrough: *passthrough,
		transport:   http.DefaultTransport,
		trace:       newTraceRecorder(*tracePath, *mode),
		recordPath:  strings.TrimSpace(*recordFixturesPath),
	}
	server := &http.Server{Addr: *addr, Handler: http.HandlerFunc(gateway.handle)}
	if *tlsAddr != "" {
		if signer == nil {
			log.Fatal("-tls-addr requires -ca-cert and -ca-key")
		}
		tlsServer := &http.Server{
			Addr:    *tlsAddr,
			Handler: http.HandlerFunc(gateway.serveFixture),
			TLSConfig: &tls.Config{
				GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
					host := hello.ServerName
					if host == "" {
						host = "localhost"
					}
					return signer.certificate(host)
				},
				MinVersion: tls.VersionTLS12,
			},
		}
		go func() {
			log.Printf("serving HTTPS fixtures on %s", *tlsAddr)
			log.Fatal(tlsServer.ListenAndServeTLS("", ""))
		}()
	}
	log.Printf("serving %d HTTP fixtures on %s", len(fixtures), *addr)
	log.Fatal(server.ListenAndServe())
}

func (g *gateway) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		g.handleConnect(w, r)
		return
	}
	g.serveFixture(w, r)
}

func (g *gateway) handleConnect(w http.ResponseWriter, r *http.Request) {
	if g.passthrough {
		g.proxyConnect(w, r)
		return
	}
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

func (g *gateway) serveFixture(w http.ResponseWriter, r *http.Request) {
	key := strings.ToUpper(r.Method) + " " + requestURL(r)
	fixture, ok := g.fixtures[key]
	if !ok {
		if g.passthrough {
			g.proxyHTTP(w, r)
			return
		}
		g.recordTrace(traceEvent{
			Method: r.Method,
			URL:    requestURL(r),
			Host:   requestHost(r),
			Status: http.StatusNotFound,
			Error:  "no fixture for " + key,
		})
		http.Error(w, "no fixture for "+key, http.StatusNotFound)
		return
	}
	reqBody, err := readRequestBody(r)
	if err != nil {
		g.recordTrace(traceEvent{
			Method: r.Method,
			URL:    requestURL(r),
			Host:   requestHost(r),
			Status: http.StatusBadRequest,
			Error:  err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateRequest(r, fixture, reqBody); err != nil {
		g.recordTrace(traceEvent{
			Method:    r.Method,
			URL:       requestURL(r),
			Host:      requestHost(r),
			Status:    http.StatusBadRequest,
			BytesSent: int64(len(reqBody)),
			Error:     err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for name, value := range fixture.Response.Headers {
		w.Header().Set(name, value)
	}
	w.WriteHeader(fixture.Response.Status)
	_, _ = w.Write([]byte(fixture.Response.Body))
	g.recordTrace(traceEvent{
		Method:        r.Method,
		URL:           requestURL(r),
		Host:          requestHost(r),
		Status:        fixture.Response.Status,
		BytesSent:     int64(len(reqBody)),
		BytesReceived: int64(len(fixture.Response.Body)),
	})
}

func (g *gateway) proxyHTTP(w http.ResponseWriter, r *http.Request) {
	host := requestHost(r)
	if !g.hostAllowed(host) {
		g.recordTrace(traceEvent{
			Method: r.Method,
			URL:    requestURL(r),
			Host:   host,
			Status: http.StatusForbidden,
			Error:  "host not in allowlist",
		})
		http.Error(w, "host not in allowlist: "+host, http.StatusForbidden)
		return
	}
	reqBody, err := readRequestBody(r)
	if err != nil {
		g.recordTrace(traceEvent{
			Method: r.Method,
			URL:    requestURL(r),
			Host:   host,
			Status: http.StatusBadRequest,
			Error:  err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	if target, ok := directTargetURL(r); ok {
		parsed, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
		if err != nil {
			g.recordTrace(traceEvent{
				Method:    r.Method,
				URL:       target,
				Host:      host,
				Status:    http.StatusBadRequest,
				BytesSent: int64(len(reqBody)),
				Error:     err.Error(),
			})
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out.URL = parsed.URL
		out.Host = parsed.Host
	} else if out.URL != nil && !out.URL.IsAbs() {
		out.URL.Scheme = "http"
		out.URL.Host = r.Host
	}
	out.Body = io.NopCloser(bytes.NewReader(reqBody))
	out.ContentLength = int64(len(reqBody))
	out.Header = cloneHeader(r.Header)
	out.Header.Del("Proxy-Connection")
	out.Header.Del("X-Crucible-Target-URL")
	resp, err := g.transport.RoundTrip(out)
	if err != nil {
		g.recordTrace(traceEvent{
			Method:    r.Method,
			URL:       requestURL(r),
			Host:      host,
			Status:    http.StatusBadGateway,
			BytesSent: int64(len(reqBody)),
			Error:     err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		g.recordTrace(traceEvent{
			Method:    r.Method,
			URL:       requestURL(r),
			Host:      host,
			Status:    http.StatusBadGateway,
			BytesSent: int64(len(reqBody)),
			Error:     err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	recorded := false
	if g.recordPath != "" {
		if err := g.recordFixture(r, reqBody, resp, respBody); err != nil {
			log.Printf("record fixture %s %s: %v", r.Method, requestURL(r), err)
		} else {
			recorded = true
		}
	}
	g.recordTrace(traceEvent{
		Method:        r.Method,
		URL:           requestURL(r),
		Host:          host,
		Status:        resp.StatusCode,
		BytesSent:     int64(len(reqBody)),
		BytesReceived: int64(len(respBody)),
		Recorded:      recorded,
	})
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

func (g *gateway) proxyConnect(w http.ResponseWriter, r *http.Request) {
	host := connectHost(r.Host)
	if !g.hostAllowed(host) {
		g.recordTrace(traceEvent{
			Method: http.MethodConnect,
			URL:    "https://" + r.Host,
			Host:   host,
			Status: http.StatusForbidden,
			Error:  "host not in allowlist",
		})
		http.Error(w, "host not in allowlist: "+host, http.StatusForbidden)
		return
	}
	target := r.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(host, "443")
	}
	upstream, err := net.DialTimeout("tcp", target, 30*time.Second)
	if err != nil {
		g.recordTrace(traceEvent{
			Method: http.MethodConnect,
			URL:    "https://" + r.Host,
			Host:   host,
			Status: http.StatusBadGateway,
			Error:  err.Error(),
		})
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		g.recordTrace(traceEvent{
			Method: http.MethodConnect,
			URL:    "https://" + r.Host,
			Host:   host,
			Status: http.StatusInternalServerError,
			Error:  "response writer does not support hijacking",
		})
		http.Error(w, "response writer does not support hijacking", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		log.Printf("hijack CONNECT %s: %v", r.Host, err)
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = conn.Close()
		_ = upstream.Close()
		g.recordTrace(traceEvent{
			Method: http.MethodConnect,
			URL:    "https://" + r.Host,
			Host:   host,
			Status: http.StatusBadGateway,
			Error:  err.Error(),
		})
		log.Printf("ack CONNECT %s: %v", r.Host, err)
		return
	}
	g.recordTrace(traceEvent{
		Method: http.MethodConnect,
		URL:    "https://" + r.Host,
		Host:   host,
		Status: http.StatusOK,
	})
	go copyAndClose(upstream, conn)
	go copyAndClose(conn, upstream)
}

func (g *gateway) hostAllowed(host string) bool {
	if len(g.allowlist) == 0 {
		return true
	}
	host = normalizeHost(host)
	for _, allowed := range g.allowlist {
		if allowed == host {
			return true
		}
		if strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, strings.TrimPrefix(allowed, "*")) {
			return true
		}
	}
	return false
}

func (g *gateway) recordTrace(event traceEvent) {
	if g.trace != nil {
		g.trace.record(event)
	}
}

func newTraceRecorder(path, mode string) *traceRecorder {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	recorder := &traceRecorder{
		path:  path,
		mode:  strings.TrimSpace(mode),
		hosts: map[string]bool{},
	}
	_ = recorder.flush()
	return recorder
}

func (r *traceRecorder) record(event traceEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event.Host = normalizeHost(event.Host)
	r.events = append(r.events, event)
	if event.Host != "" {
		r.hosts[event.Host] = true
	}
	_ = r.flushLocked()
}

func (r *traceRecorder) flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushLocked()
}

func (r *traceRecorder) flushLocked() error {
	summary := traceSummary{
		Mode:         r.mode,
		RequestCount: len(r.events),
		Events:       append([]traceEvent(nil), r.events...),
	}
	for host := range r.hosts {
		summary.UniqueHosts = append(summary.UniqueHosts, host)
	}
	sort.Strings(summary.UniqueHosts)
	for _, event := range r.events {
		summary.BytesSent += event.BytesSent
		summary.BytesReceived += event.BytesReceived
		if event.Error != "" || event.Status >= 400 {
			summary.FailureCount++
		}
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(r.path, data, 0o644)
}

func (g *gateway) recordFixture(r *http.Request, reqBody []byte, resp *http.Response, respBody []byte) error {
	g.recordMu.Lock()
	defer g.recordMu.Unlock()
	id := fmt.Sprintf("recorded-%04d", len(g.recorded)+1)
	recorded := fixture{
		ID: id,
		Request: request{
			Method: strings.ToUpper(r.Method),
			URL:    requestURL(r),
		},
		Response: response{
			Status:  resp.StatusCode,
			Headers: firstHeaderValues(resp.Header),
			Body:    string(respBody),
		},
	}
	if len(reqBody) > 0 {
		sum := sha256.Sum256(reqBody)
		recorded.Request.BodySHA256 = hex.EncodeToString(sum[:])
	}
	g.recorded = append(g.recorded, recorded)
	set := fixtureSet{Version: 1, Fixtures: append([]fixture(nil), g.recorded...)}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(g.recordPath, data, 0o644)
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

func parseAllowlist(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		host := normalizeHost(part)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	return out
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
		return normalizeHost(host)
	}
	return normalizeHost(authority)
}

func requestHost(r *http.Request) string {
	if target, ok := directTargetURL(r); ok {
		if parsed, err := http.NewRequest(r.Method, target, nil); err == nil {
			return normalizeHost(parsed.URL.Host)
		}
	}
	if r.URL != nil && r.URL.Host != "" {
		return normalizeHost(r.URL.Host)
	}
	return normalizeHost(r.Host)
}

func directTargetURL(r *http.Request) (string, bool) {
	if value := strings.TrimSpace(r.Header.Get("X-Crucible-Target-URL")); value != "" {
		return value, true
	}
	if r.URL == nil {
		return "", false
	}
	if value := strings.TrimSpace(r.URL.Query().Get("crucible_url")); value != "" {
		return value, true
	}
	const prefix = "/__crucible/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return "", false
	}
	scheme := parts[0]
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := parts[1]
	path := "/"
	if len(parts) == 3 && parts[2] != "" {
		path += parts[2]
	}
	query := r.URL.Query()
	query.Del("crucible_url")
	rawQuery := query.Encode()
	target := scheme + "://" + host + path
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return target, true
}

func normalizeHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https://")
	if slash := strings.Index(value, "/"); slash >= 0 {
		value = value[:slash]
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
}

func cloneHeader(header http.Header) http.Header {
	out := make(http.Header, len(header))
	for key, values := range header {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func copyHeader(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyAndClose(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
	_ = src.Close()
}

func readRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func firstHeaderValues(header http.Header) map[string]string {
	if len(header) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, values := range header {
		if len(values) > 0 {
			out[key] = values[0]
		}
	}
	return out
}

func validateRequest(r *http.Request, fixture fixture, body []byte) error {
	for name, value := range fixture.Request.Headers {
		if got := r.Header.Get(name); got != value {
			return fmt.Errorf("request header %s = %q, want %q", name, got, value)
		}
	}
	if fixture.Request.BodySHA256 == "" {
		return nil
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, fixture.Request.BodySHA256) {
		return fmt.Errorf("request body_sha256 = %q, want %q", got, fixture.Request.BodySHA256)
	}
	return nil
}

func requestURL(r *http.Request) string {
	if target, ok := directTargetURL(r); ok {
		return target
	}
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
