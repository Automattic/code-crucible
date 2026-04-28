package external

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Automattic/code-crucible/internal/model"
)

const (
	HTTPFixtureVersion = 1
	HTTPFixturesName   = "http-fixtures.json"
	MockGatewayName    = "mock-gateway.go"
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

func needsHTTPFixtures(mode model.ExternalMode) bool {
	return mode == model.ExternalModeMock ||
		mode == model.ExternalModeReplay ||
		mode == model.ExternalModeRecord
}

func mockGatewaySource() string {
	return `package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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
	Method string ` + "`json:\"method\"`" + `
	URL    string ` + "`json:\"url\"`" + `
}

type response struct {
	Status  int               ` + "`json:\"status\"`" + `
	Headers map[string]string ` + "`json:\"headers,omitempty\"`" + `
	Body    string            ` + "`json:\"body,omitempty\"`" + `
}

func main() {
	fixturesPath := flag.String("fixtures", "http-fixtures.json", "HTTP fixture JSON file")
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	flag.Parse()

	fixtures, err := loadFixtures(*fixturesPath)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + requestURL(r)
		fixture, ok := fixtures[key]
		if !ok {
			http.Error(w, "no fixture for "+key, http.StatusNotFound)
			return
		}
		for name, value := range fixture.Response.Headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(fixture.Response.Status)
		_, _ = w.Write([]byte(fixture.Response.Body))
	})

	server := &http.Server{Addr: *addr, Handler: mux}
	log.Printf("serving %d HTTP fixtures on %s", len(fixtures), *addr)
	log.Fatal(server.ListenAndServe())
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

func requestURL(r *http.Request) string {
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
