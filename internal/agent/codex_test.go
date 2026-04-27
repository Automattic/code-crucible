package agent

import (
	"reflect"
	"testing"
)

func TestBuildCodexExecCommandDefaults(t *testing.T) {
	got := BuildCodexExecCommand(CodexOptions{
		ProjectDir:       "/repo",
		SkipGitRepoCheck: true,
	})
	want := []string{
		"codex",
		"exec",
		"--cd", "/repo",
		"--sandbox", "workspace-write",
		"--ask-for-approval", "never",
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
		"exec",
		"--cd", "/repo",
		"--sandbox", "read-only",
		"--ask-for-approval", "on-request",
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
