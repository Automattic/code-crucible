package archive

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRunDirSelectors(t *testing.T) {
	projectDir := t.TempDir()
	runsDir := filepath.Join(projectDir, ".crucible", "runs")
	for _, name := range []string{
		"20260428-100000-first",
		"20260428-110000-second",
		"20260428-120000-third",
	} {
		if err := os.MkdirAll(filepath.Join(runsDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for selector, want := range map[string]string{
		"":                      "20260428-120000-third",
		"latest":                "20260428-120000-third",
		"previous":              "20260428-110000-second",
		"20260428-110000":       "20260428-110000-second",
		"20260428-100000-first": "20260428-100000-first",
	} {
		got, err := RunDir(projectDir, selector)
		if err != nil {
			t.Fatalf("RunDir(%q) returned error: %v", selector, err)
		}
		if filepath.Base(got) != want {
			t.Fatalf("RunDir(%q) = %q, want %q", selector, filepath.Base(got), want)
		}
	}
}

func TestRunDirAmbiguousPrefix(t *testing.T) {
	projectDir := t.TempDir()
	runsDir := filepath.Join(projectDir, ".crucible", "runs")
	for _, name := range []string{
		"20260428-100000-first",
		"20260428-100500-second",
	} {
		if err := os.MkdirAll(filepath.Join(runsDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := RunDir(projectDir, "20260428-10"); err == nil {
		t.Fatal("RunDir succeeded for ambiguous prefix")
	}
}

func TestCopyPathRejectsSymlinkSource(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	err := CopyPath(link, filepath.Join(dir, "out.txt"))
	if err == nil {
		t.Fatal("CopyPath succeeded for symlink source")
	}
	if !strings.Contains(err.Error(), "refusing to copy symlink") {
		t.Fatalf("CopyPath error = %v, want symlink refusal", err)
	}
}

func TestCopyPathRejectsSymlinkInsideDirectory(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "regular.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(sourceDir, "link.txt")); err != nil {
		t.Fatal(err)
	}

	err := CopyPath(sourceDir, filepath.Join(dir, "out"))
	if err == nil {
		t.Fatal("CopyPath succeeded for directory containing symlink")
	}
	if !strings.Contains(err.Error(), "refusing to copy symlink") {
		t.Fatalf("CopyPath error = %v, want symlink refusal", err)
	}
}
