package archive

import (
	"path/filepath"
	"testing"
)

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"Reduce p95 latency!":    "reduce-p95-latency",
		"  API cost / retries  ": "api-cost-retries",
		"":                       "optimization",
		"Already-clean-slug":     "already-clean-slug",
		"symbols *** everywhere": "symbols-everywhere",
	}

	for input, want := range tests {
		if got := Slug(input, 64); got != want {
			t.Fatalf("Slug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSlugMaxLength(t *testing.T) {
	got := Slug("make the checkout endpoint significantly faster", 12)
	if got != "make-the" {
		t.Fatalf("Slug max length = %q, want %q", got, "make-the")
	}
}

func TestProjectPath(t *testing.T) {
	projectDir := filepath.Join(string(filepath.Separator), "repo", "project")

	if got := ProjectPath(projectDir, ".crucible/runs/run-1"); got != filepath.Join(projectDir, ".crucible", "runs", "run-1") {
		t.Fatalf("ProjectPath relative = %q", got)
	}
	if got := ProjectPath(projectDir, filepath.Join(projectDir, "ranking", "rank.go")); got != filepath.Join(projectDir, "ranking", "rank.go") {
		t.Fatalf("ProjectPath absolute = %q", got)
	}
	if got := ProjectPath(projectDir, ""); got != "" {
		t.Fatalf("ProjectPath empty = %q, want empty", got)
	}
}

func TestProjectRelativePath(t *testing.T) {
	projectDir := filepath.Join(string(filepath.Separator), "repo", "project")

	if got := ProjectRelativePath(projectDir, filepath.Join(projectDir, ".crucible", "runs", "run-1")); got != ".crucible/runs/run-1" {
		t.Fatalf("ProjectRelativePath inside project = %q", got)
	}
	if got := ProjectRelativePath(projectDir, filepath.Join(string(filepath.Separator), "tmp", "outside")); got != "/tmp/outside" {
		t.Fatalf("ProjectRelativePath outside project = %q", got)
	}
	if got := ProjectRelativePath(projectDir, "ranking/rank.go"); got != "ranking/rank.go" {
		t.Fatalf("ProjectRelativePath relative = %q", got)
	}
	if got := ProjectRelativePath(projectDir, ""); got != "" {
		t.Fatalf("ProjectRelativePath empty = %q, want empty", got)
	}
}
