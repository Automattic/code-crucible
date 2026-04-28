package agent

import (
	"fmt"
	"sort"
)

type ProviderTemplateConfig struct {
	DefaultAgent   string                        `json:"default_agent,omitempty"`
	AgentProviders map[string]ProviderDefinition `json:"agent_providers"`
}

type ProviderSetupTemplate struct {
	Name        string
	Description string
	Config      ProviderTemplateConfig
}

func LookupProviderTemplate(name string) (ProviderSetupTemplate, bool) {
	templates := providerTemplates()
	template, ok := templates[NormalizeProviderName(name)]
	return template, ok
}

func ProviderTemplateNames() []string {
	templates := providerTemplates()
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ValidateProviderTemplate(template ProviderSetupTemplate) error {
	if template.Name == "" {
		return fmt.Errorf("provider template name is required")
	}
	if len(template.Config.AgentProviders) == 0 {
		return fmt.Errorf("provider template %q must include at least one provider", template.Name)
	}
	if template.Config.DefaultAgent != "" {
		if _, ok := template.Config.AgentProviders[template.Config.DefaultAgent]; !ok {
			return fmt.Errorf("provider template %q default_agent %q is not included in agent_providers", template.Name, template.Config.DefaultAgent)
		}
	}
	for key, provider := range template.Config.AgentProviders {
		if provider.Name == "" {
			provider.Name = key
		}
		if err := ValidateProviderDefinition(provider); err != nil {
			return err
		}
	}
	return nil
}

func providerTemplates() map[string]ProviderSetupTemplate {
	claude := ProviderDefinition{
		Name:        "claude",
		Kind:        "command",
		Description: "Claude Code CLI provider using print mode with the Code Crucible prompt supplied on stdin.",
		Command: []string{
			"claude",
			"--bare",
			"--output-format",
			"text",
			"--permission-mode",
			"acceptEdits",
			"-p",
			"Read the Code Crucible prompt from stdin and complete the requested optimization-agent task. Keep generated artifacts inside the run archive unless the prompt explicitly says otherwise.",
		},
		Capabilities: ProviderCapabilities{
			SupportsDiscovery:  true,
			SupportsGeneration: true,
			SupportsEvolution:  true,
			SupportsJSONOutput: false,
			RequiresGitRepo:    false,
		},
	}
	return map[string]ProviderSetupTemplate{
		"claude": {
			Name:        "claude",
			Description: "Claude Code CLI command-provider configuration.",
			Config: ProviderTemplateConfig{
				DefaultAgent: "claude",
				AgentProviders: map[string]ProviderDefinition{
					"claude": claude,
				},
			},
		},
	}
}
