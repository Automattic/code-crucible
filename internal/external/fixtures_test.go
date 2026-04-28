package external

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

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

func TestMockGatewaySourceParses(t *testing.T) {
	if _, err := parser.ParseFile(token.NewFileSet(), MockGatewayName, mockGatewaySource(), 0); err != nil {
		t.Fatal(err)
	}
}
