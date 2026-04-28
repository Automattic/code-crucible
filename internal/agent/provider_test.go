package agent

import (
	"reflect"
	"testing"
)

func TestBuiltinProviderCapabilities(t *testing.T) {
	codex, ok := Provider("CODEX")
	if !ok {
		t.Fatal("codex provider missing")
	}
	if !codex.Capabilities.SupportsDiscovery || !codex.Capabilities.SupportsGeneration || !codex.Capabilities.SupportsEvolution {
		t.Fatalf("codex capabilities = %#v, want discovery/generation/evolution", codex.Capabilities)
	}
	if !codex.Capabilities.SupportsJSONOutput {
		t.Fatalf("codex capabilities = %#v, want JSON output support", codex.Capabilities)
	}

	local, ok := Provider("local")
	if !ok {
		t.Fatal("local provider missing")
	}
	if !local.Capabilities.SupportsDiscovery || local.Capabilities.SupportsGeneration || local.Capabilities.SupportsEvolution {
		t.Fatalf("local capabilities = %#v, want discovery-only", local.Capabilities)
	}
}

func TestProviderNamesSorted(t *testing.T) {
	got := ProviderNames()
	want := []string{"codex", "local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProviderNames() = %#v, want %#v", got, want)
	}
}

func TestValidateProvider(t *testing.T) {
	if err := ValidateProvider("codex"); err != nil {
		t.Fatalf("ValidateProvider(codex) returned error: %v", err)
	}
	if err := ValidateProvider("unknown"); err == nil {
		t.Fatal("ValidateProvider(unknown) succeeded")
	}
}
