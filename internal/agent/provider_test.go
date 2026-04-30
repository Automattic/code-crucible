package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestValidateProviderDefinitionRejectsUnsafeProviderName(t *testing.T) {
	provider := ProviderDefinition{
		Name:    "../outside",
		Kind:    "command",
		Command: []string{"provider"},
		Capabilities: ProviderCapabilities{
			SupportsGeneration: true,
		},
	}
	if err := ValidateProviderDefinition(provider); err == nil {
		t.Fatal("ValidateProviderDefinition accepted unsafe provider name")
	}
}

func TestValidateProviderCapability(t *testing.T) {
	if err := ValidateProviderCapability("codex", "generation"); err != nil {
		t.Fatalf("ValidateProviderCapability(codex, generation) returned error: %v", err)
	}
	if err := ValidateProviderCapability("local", "generation"); err == nil {
		t.Fatal("ValidateProviderCapability(local, generation) succeeded")
	}
}

func TestClaudeCodeProviderExampleIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "agent-providers", "claude-code-config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		DefaultAgent   string                        `json:"default_agent"`
		AgentProviders map[string]ProviderDefinition `json:"agent_providers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	provider, ok := ProviderFromConfig(cfg.DefaultAgent, cfg.AgentProviders)
	if !ok {
		t.Fatalf("default agent %q not found in example config", cfg.DefaultAgent)
	}
	if err := ValidateProviderDefinition(provider); err != nil {
		t.Fatalf("example provider is invalid: %v", err)
	}
	if !provider.Supports("discovery") || !provider.Supports("generation") || !provider.Supports("evolution") {
		t.Fatalf("example capabilities = %#v, want discovery/generation/evolution", provider.Capabilities)
	}
	if got := BuildCommandProviderCommand(provider); len(got) == 0 || got[0] != "claude" {
		t.Fatalf("example command = %#v, want claude command", got)
	}
}

func TestClaudeProviderTemplateIsValid(t *testing.T) {
	template, ok := LookupProviderTemplate("CLAUDE")
	if !ok {
		t.Fatal("claude template missing")
	}
	if err := ValidateProviderTemplate(template); err != nil {
		t.Fatalf("claude template is invalid: %v", err)
	}
	provider, ok := ProviderFromConfig(template.Config.DefaultAgent, template.Config.AgentProviders)
	if !ok {
		t.Fatalf("default agent %q not found in template config", template.Config.DefaultAgent)
	}
	if got := BuildCommandProviderCommand(provider); len(got) == 0 || got[0] != "claude" {
		t.Fatalf("template command = %#v, want claude command", got)
	}
}

func TestProviderTemplateNamesSorted(t *testing.T) {
	got := ProviderTemplateNames()
	want := []string{"claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProviderTemplateNames() = %#v, want %#v", got, want)
	}
}
