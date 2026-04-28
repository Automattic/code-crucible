package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/model"
)

const DirName = ".crucible"

type Config struct {
	Version               int                                 `json:"version"`
	ProjectName           string                              `json:"project_name"`
	CreatedAt             time.Time                           `json:"created_at"`
	DefaultAgent          string                              `json:"default_agent"`
	AgentProviders        map[string]agent.ProviderDefinition `json:"agent_providers,omitempty"`
	DefaultExternalPolicy model.ExternalPolicy                `json:"default_external_policy"`
	ArchiveDir            string                              `json:"archive_dir"`
}

func WorkDir(projectDir string) string {
	return filepath.Join(projectDir, DirName)
}

func ConfigPath(projectDir string) string {
	return filepath.Join(WorkDir(projectDir), "config.json")
}

func Init(projectDir, name string) (*Config, error) {
	return InitWithOptions(projectDir, InitOptions{Name: name})
}

type InitOptions struct {
	Name         string
	DefaultAgent string
}

func InitWithOptions(projectDir string, opts InitOptions) (*Config, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	if existing, err := Load(abs); err == nil {
		return existing, nil
	}

	if strings.TrimSpace(opts.Name) == "" {
		opts.Name = filepath.Base(abs)
	}
	opts.DefaultAgent = agent.NormalizeProviderName(opts.DefaultAgent)
	if opts.DefaultAgent == "" {
		opts.DefaultAgent = agent.ProviderCodex
	}
	if err := agent.ValidateProviderCapability(opts.DefaultAgent, "generation"); err != nil {
		return nil, err
	}

	cfg := &Config{
		Version:      1,
		ProjectName:  opts.Name,
		CreatedAt:    time.Now().UTC(),
		DefaultAgent: opts.DefaultAgent,
		DefaultExternalPolicy: model.ExternalPolicy{
			Mode: model.ExternalModeDeny,
		},
		ArchiveDir: "runs",
	}

	dirs := []string{
		WorkDir(abs),
		filepath.Join(WorkDir(abs), "agents"),
		filepath.Join(WorkDir(abs), "competitors"),
		filepath.Join(WorkDir(abs), "discoveries"),
		filepath.Join(WorkDir(abs), "evaluators"),
		filepath.Join(WorkDir(abs), "fixtures", "http"),
		filepath.Join(WorkDir(abs), "interfaces"),
		filepath.Join(WorkDir(abs), "runs"),
		filepath.Join(WorkDir(abs), "tasks"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	if err := writeJSON(ConfigPath(abs), cfg, 0o644); err != nil {
		return nil, err
	}

	readmePath := filepath.Join(WorkDir(abs), "README.md")
	if err := writeIfMissing(readmePath, workAreaReadme(opts.Name), 0o644); err != nil {
		return nil, err
	}

	return cfg, nil
}

func Ensure(projectDir string) (*Config, error) {
	cfg, err := Load(projectDir)
	if err == nil {
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	return Init(projectDir, "")
}

func Load(projectDir string) (*Config, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(ConfigPath(abs))
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("read crucible config: %w", err)
	}
	NormalizeConfig(&cfg)
	return &cfg, nil
}

func Save(projectDir string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	NormalizeConfig(cfg)
	return writeJSON(ConfigPath(projectDir), cfg, 0o644)
}

func NormalizeConfig(cfg *Config) {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	cfg.DefaultAgent = agent.NormalizeProviderName(cfg.DefaultAgent)
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = agent.ProviderCodex
	}
	if cfg.DefaultExternalPolicy.Mode == "" {
		cfg.DefaultExternalPolicy.Mode = model.ExternalModeDeny
	}
	if strings.TrimSpace(cfg.ArchiveDir) == "" {
		cfg.ArchiveDir = "runs"
	}
	if len(cfg.AgentProviders) > 0 {
		normalized := make(map[string]agent.ProviderDefinition, len(cfg.AgentProviders))
		for key, provider := range cfg.AgentProviders {
			provider.Name = agent.NormalizeProviderName(provider.Name)
			if provider.Name == "" {
				provider.Name = agent.NormalizeProviderName(key)
			}
			provider.Kind = agent.NormalizeProviderName(provider.Kind)
			normalized[provider.Name] = provider
		}
		cfg.AgentProviders = normalized
	}
}

func writeJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, mode)
}

func writeIfMissing(path, body string, mode os.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(body), mode)
}

func workAreaReadme(name string) string {
	return fmt.Sprintf(`# Code Crucible Work Area

This directory stores optimization tournaments for %s.

Important paths:

- tasks/ stores reusable optimization task specs.
- discoveries/ stores optimization discovery plans and agent discovery prompts.
- interfaces/ stores discovered input, output, and external communication contracts.
- evaluators/ stores reusable test and benchmark harnesses.
- fixtures/ stores mock and replay data for external calls.
- runs/ stores immutable run archives, candidates, metrics, logs, prompts, and verdicts.
- index.sqlite is a rebuildable summary created by crucible index.

The contents of runs/ are intended to be reproducible artifacts. Avoid editing completed run output by hand.
`, name)
}
