package agent

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ProviderCodex = "codex"
	ProviderLocal = "local"
)

type ProviderCapabilities struct {
	SupportsDiscovery  bool `json:"supports_discovery"`
	SupportsGeneration bool `json:"supports_generation"`
	SupportsEvolution  bool `json:"supports_evolution"`
	SupportsJSONOutput bool `json:"supports_json_output"`
	RequiresGitRepo    bool `json:"requires_git_repo"`
}

type ProviderDefinition struct {
	Name         string               `json:"name"`
	Kind         string               `json:"kind"`
	Description  string               `json:"description,omitempty"`
	Command      []string             `json:"command,omitempty"`
	Capabilities ProviderCapabilities `json:"capabilities"`
}

func BuiltinProviders() map[string]ProviderDefinition {
	return map[string]ProviderDefinition{
		ProviderCodex: {
			Name:        ProviderCodex,
			Kind:        "codex",
			Description: "Built-in Codex CLI provider.",
			Capabilities: ProviderCapabilities{
				SupportsDiscovery:  true,
				SupportsGeneration: true,
				SupportsEvolution:  true,
				SupportsJSONOutput: true,
				RequiresGitRepo:    false,
			},
		},
		ProviderLocal: {
			Name:        ProviderLocal,
			Kind:        "local",
			Description: "Local heuristic discovery provider; does not invoke a model.",
			Capabilities: ProviderCapabilities{
				SupportsDiscovery:  true,
				SupportsGeneration: false,
				SupportsEvolution:  false,
				SupportsJSONOutput: false,
				RequiresGitRepo:    false,
			},
		},
	}
}

func Provider(name string) (ProviderDefinition, bool) {
	provider, ok := BuiltinProviders()[NormalizeProviderName(name)]
	return provider, ok
}

func ProviderFromConfig(name string, configured map[string]ProviderDefinition) (ProviderDefinition, bool) {
	name = NormalizeProviderName(name)
	if provider, ok := Provider(name); ok {
		return provider, true
	}
	for key, provider := range configured {
		providerName := NormalizeProviderName(provider.Name)
		if providerName == "" {
			providerName = NormalizeProviderName(key)
		}
		if providerName == name {
			provider.Name = providerName
			provider.Kind = NormalizeProviderName(provider.Kind)
			return provider, true
		}
	}
	return ProviderDefinition{}, false
}

func NormalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func ValidateProvider(name string) error {
	name = NormalizeProviderName(name)
	if name == "" {
		return fmt.Errorf("agent provider is required")
	}
	if _, ok := Provider(name); ok {
		return nil
	}
	return fmt.Errorf("unsupported agent provider %q", name)
}

func ValidateProviderCapability(name, capability string) error {
	provider, ok := Provider(name)
	if !ok {
		return fmt.Errorf("unsupported agent provider %q", NormalizeProviderName(name))
	}
	if provider.Supports(capability) {
		return nil
	}
	return fmt.Errorf("agent provider %q does not support %s", provider.Name, capability)
}

func ValidateProviderDefinition(provider ProviderDefinition) error {
	provider.Name = NormalizeProviderName(provider.Name)
	provider.Kind = NormalizeProviderName(provider.Kind)
	if provider.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	switch provider.Kind {
	case "codex", "local":
		if builtin, ok := Provider(provider.Name); ok && builtin.Kind == provider.Kind {
			return nil
		}
		return fmt.Errorf("provider %q uses reserved kind %q", provider.Name, provider.Kind)
	case "command":
		if len(provider.Command) == 0 {
			return fmt.Errorf("provider %q command is required", provider.Name)
		}
		for i, arg := range provider.Command {
			if strings.TrimSpace(arg) == "" {
				return fmt.Errorf("provider %q command argument %d is empty", provider.Name, i+1)
			}
		}
		if !provider.Capabilities.SupportsDiscovery && !provider.Capabilities.SupportsGeneration && !provider.Capabilities.SupportsEvolution {
			return fmt.Errorf("provider %q must support at least one agent capability", provider.Name)
		}
		return nil
	default:
		return fmt.Errorf("provider %q has unsupported kind %q", provider.Name, provider.Kind)
	}
}

func (p ProviderDefinition) Supports(capability string) bool {
	switch strings.ToLower(strings.TrimSpace(capability)) {
	case "discovery":
		return p.Capabilities.SupportsDiscovery
	case "generation":
		return p.Capabilities.SupportsGeneration
	case "evolution":
		return p.Capabilities.SupportsEvolution
	case "json-output":
		return p.Capabilities.SupportsJSONOutput
	case "git-repo":
		return p.Capabilities.RequiresGitRepo
	default:
		return false
	}
}

func ProviderNames() []string {
	providers := BuiltinProviders()
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
