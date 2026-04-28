package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommandProviderArchivesInvocationAndFinalOutput(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello provider\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "provider.sh")
	body := `#!/usr/bin/env bash
set -euo pipefail
prompt="$(cat)"
printf 'provider=%s\n' "$CRUCIBLE_PROVIDER_NAME"
printf 'model=%s\n' "${CRUCIBLE_MODEL:-}"
printf 'prompt=%s\n' "$prompt"
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := RunCommandProvider(context.Background(), CommandProviderOptions{
		Provider: ProviderDefinition{
			Name:    "custom",
			Kind:    "command",
			Command: []string{script},
			Capabilities: ProviderCapabilities{
				SupportsGeneration: true,
			},
		},
		ProjectDir: root,
		RunDir:     root,
		PromptPath: promptPath,
		Model:      "model-x",
	}, &strings.Builder{}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	final, err := os.ReadFile(result.FinalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(final), "provider=custom") || !strings.Contains(string(final), "model=model-x") {
		t.Fatalf("final output missing provider/model:\n%s", final)
	}

	raw, err := os.ReadFile(result.InvocationPath)
	if err != nil {
		t.Fatal(err)
	}
	var invocation CommandProviderInvocation
	if err := json.Unmarshal(raw, &invocation); err != nil {
		t.Fatalf("decode invocation: %v\n%s", err, raw)
	}
	if invocation.ProviderName != "custom" || invocation.ProviderKind != "command" {
		t.Fatalf("provider metadata = %s/%s", invocation.ProviderName, invocation.ProviderKind)
	}
	if invocation.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", invocation.ExitCode)
	}
	if len(invocation.Command) != 1 || invocation.Command[0] != script {
		t.Fatalf("Command = %#v, want %s", invocation.Command, script)
	}
}
