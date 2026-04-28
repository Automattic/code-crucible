package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildCodexExecCommandDefaults(t *testing.T) {
	got := BuildCodexExecCommand(CodexOptions{
		ProjectDir:       "/repo",
		SkipGitRepoCheck: true,
	})
	want := []string{
		"codex",
		"--ask-for-approval", "never",
		"exec",
		"--cd", "/repo",
		"--sandbox", "workspace-write",
		"--skip-git-repo-check",
		"-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCodexExecCommand() = %#v, want %#v", got, want)
	}
}

func TestBuildCodexExecCommandWithOverrides(t *testing.T) {
	got := BuildCodexExecCommand(CodexOptions{
		Binary:            "/opt/bin/codex",
		ProjectDir:        "/repo",
		Model:             "gpt-5.5",
		Profile:           "work",
		Sandbox:           "read-only",
		ApprovalPolicy:    "on-request",
		OutputLastMessage: "/repo/.crucible/runs/run/agents/final.md",
		JSONEvents:        true,
	})
	want := []string{
		"/opt/bin/codex",
		"--ask-for-approval", "on-request",
		"exec",
		"--cd", "/repo",
		"--sandbox", "read-only",
		"--model", "gpt-5.5",
		"--profile", "work",
		"--json",
		"--output-last-message", "/repo/.crucible/runs/run/agents/final.md",
		"-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildCodexExecCommand() = %#v, want %#v", got, want)
	}
}

func TestRunCodexArchivesProviderMetadata(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("generate variants\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeCodex := filepath.Join(root, "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
out=""
while [[ $# -gt 0 ]]; do
  if [[ "$1" == "--output-last-message" ]]; then
    out="$2"
    shift 2
    continue
  fi
  shift
done
cat >/dev/null
mkdir -p "$(dirname "$out")"
printf 'done\n' > "$out"
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := RunCodex(context.Background(), CodexOptions{
		ProviderName:      ProviderCodex,
		Binary:            fakeCodex,
		ProjectDir:        root,
		RunDir:            root,
		PromptPath:        promptPath,
		Model:             "gpt-test",
		Profile:           "work",
		Sandbox:           "workspace-write",
		ApprovalPolicy:    "never",
		JSONEvents:        true,
		SkipGitRepoCheck:  true,
		OutputLastMessage: filepath.Join(root, "final.md"),
	}, &strings.Builder{}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(result.InvocationPath)
	if err != nil {
		t.Fatal(err)
	}
	var invocation CodexInvocation
	if err := json.Unmarshal(raw, &invocation); err != nil {
		t.Fatalf("decode invocation: %v\n%s", err, raw)
	}
	if invocation.ProviderName != ProviderCodex {
		t.Fatalf("ProviderName = %q, want codex", invocation.ProviderName)
	}
	if invocation.Model != "gpt-test" || invocation.Profile != "work" {
		t.Fatalf("model/profile = %q/%q, want gpt-test/work", invocation.Model, invocation.Profile)
	}
	if invocation.EnvironmentPolicy.Sandbox != "workspace-write" || invocation.EnvironmentPolicy.ApprovalPolicy != "never" {
		t.Fatalf("environment policy = %#v", invocation.EnvironmentPolicy)
	}
	if invocation.PromptPath != promptPath || invocation.StdoutPath == "" || invocation.StderrPath == "" || invocation.OutputLastMessage == "" {
		t.Fatalf("invocation paths incomplete: %#v", invocation)
	}
	if invocation.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", invocation.ExitCode)
	}
}
