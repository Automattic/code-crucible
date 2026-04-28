package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Automattic/code-crucible/internal/model"
)

func TestInitWithOptionsStoresDefaultAgent(t *testing.T) {
	projectDir := t.TempDir()
	cfg, err := InitWithOptions(projectDir, InitOptions{
		Name:         "checkout",
		DefaultAgent: "Codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectName != "checkout" {
		t.Fatalf("ProjectName = %q, want checkout", cfg.ProjectName)
	}
	if cfg.DefaultAgent != "codex" {
		t.Fatalf("DefaultAgent = %q, want codex", cfg.DefaultAgent)
	}

	loaded, err := Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefaultAgent != "codex" {
		t.Fatalf("loaded DefaultAgent = %q, want codex", loaded.DefaultAgent)
	}
}

func TestInitWithOptionsRejectsUnsupportedDefaultAgent(t *testing.T) {
	_, err := InitWithOptions(t.TempDir(), InitOptions{DefaultAgent: "unknown"})
	if err == nil {
		t.Fatal("InitWithOptions succeeded with unsupported default agent")
	}
}

func TestLoadNormalizesLegacyConfigDefaults(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.MkdirAll(WorkDir(projectDir), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "project_name": "legacy"
}
`
	if err := os.WriteFile(filepath.Join(WorkDir(projectDir), "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 {
		t.Fatalf("Version = %d, want 1", cfg.Version)
	}
	if cfg.DefaultAgent != "codex" {
		t.Fatalf("DefaultAgent = %q, want codex", cfg.DefaultAgent)
	}
	if cfg.DefaultExternalPolicy.Mode != model.ExternalModeDeny {
		t.Fatalf("DefaultExternalPolicy.Mode = %q, want deny", cfg.DefaultExternalPolicy.Mode)
	}
	if cfg.ArchiveDir != "runs" {
		t.Fatalf("ArchiveDir = %q, want runs", cfg.ArchiveDir)
	}
}
