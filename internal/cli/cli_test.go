package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
	"github.com/Automattic/code-crucible/internal/run"
)

func TestInteractiveCreatesRun(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)

	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader("\n\nmake checkout pricing faster\n\nn\nskip\nn\nq\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Optimize != "make checkout pricing faster" {
		t.Fatalf("Optimize = %q, want interactive prompt", cfg.Optimize)
	}
	if !strings.Contains(stdout.String(), "Initialized Code Crucible work area") {
		t.Fatalf("stdout did not include init message:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Source path: agent discovery pending") {
		t.Fatalf("stdout did not describe source discovery:\n%s", stdout.String())
	}
}

func TestInteractiveNewRunPromptsForAdvancedOptions(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := "\n\nmake ranking faster\n\nn\ny\nsupply\ngo test ./...\n\ny\n2\n0.80\nallowlist\n\napi.example.com,cache.example.com\nq\n"
	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rounds != 2 || cfg.Exploration != 0.80 || cfg.Evaluator != "go test ./..." {
		t.Fatalf("advanced run fields = rounds %d exploration %.2f evaluator %q", cfg.Rounds, cfg.Exploration, cfg.Evaluator)
	}
	if cfg.External.Mode != model.ExternalModeAllowlist {
		t.Fatalf("external mode = %q", cfg.External.Mode)
	}
	if got := strings.Join(cfg.External.Allowlist, ","); got != "api.example.com,cache.example.com" {
		t.Fatalf("allowlist = %q", got)
	}
	for _, want := range []string{"Rounds: 2", "Exploration: 0.80", "External mode: allowlist"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not contain %q:\n%s", want, stdout.String())
		}
	}
}

func TestInteractiveRunContinuesWhenEvaluatorGenerationProviderMissing(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	if err := os.WriteFile(filepath.Join(projectDir, "rank.php"), []byte("<?php function rank_scores() { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := project.Init(projectDir, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultAgent = "missing"
	cfg.AgentProviders = map[string]agent.ProviderDefinition{
		"missing": {
			Name:    "missing",
			Kind:    "command",
			Command: []string{filepath.Join(projectDir, "missing-agent")},
			Capabilities: agent.ProviderCapabilities{
				SupportsGeneration: true,
			},
		},
	}
	if err := project.Save(projectDir, cfg); err != nil {
		t.Fatal(err)
	}

	input := "\n1\nmake rank faster\n\nn\nn\ngenerate\n\nn\nq\n"
	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s\nstdout:\n%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stderr.String(), "evaluator generate failed: agent provider") {
		t.Fatalf("stderr did not include evaluator generation failure:\n%s", stderr.String())
	}
	for _, want := range []string{
		"Evaluator generation failed; the run remains available.",
		"Retry evaluator generation: crucible evaluator generate",
		"Run:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not contain %q:\n%s", want, stdout.String())
		}
	}
	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one leaderboard, found %d", len(matches))
	}
	board, err := archive.LoadLeaderboard(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if board.Results[0].Status != model.CandidateStatusNeedsEvaluator {
		t.Fatalf("baseline status = %q, want needs-evaluator", board.Results[0].Status)
	}
}

func TestInitStoresDefaultAgent(t *testing.T) {
	projectDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"init",
		"--project", projectDir,
		"--name", "checkout",
		"--default-agent", "codex",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("init returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Default agent: codex") {
		t.Fatalf("stdout did not include default agent:\n%s", stdout.String())
	}
	raw, err := os.ReadFile(filepath.Join(projectDir, ".crucible", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"default_agent": "codex"`) {
		t.Fatalf("config did not store default agent:\n%s", raw)
	}
}

func TestProviderTemplateClaudeJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"provider", "template", "claude", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("provider template returned %d, stderr: %s", code, stderr.String())
	}
	var cfg struct {
		DefaultAgent   string                              `json:"default_agent"`
		AgentProviders map[string]agent.ProviderDefinition `json:"agent_providers"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &cfg); err != nil {
		t.Fatalf("template output is not JSON: %v\n%s", err, stdout.String())
	}
	provider, ok := agent.ProviderFromConfig(cfg.DefaultAgent, cfg.AgentProviders)
	if !ok {
		t.Fatalf("default agent %q not found in template output", cfg.DefaultAgent)
	}
	if err := agent.ValidateProviderDefinition(provider); err != nil {
		t.Fatalf("provider definition is invalid: %v", err)
	}
	if !provider.Supports("discovery") || !provider.Supports("generation") || !provider.Supports("evolution") {
		t.Fatalf("provider capabilities = %#v, want discovery/generation/evolution", provider.Capabilities)
	}
	if len(provider.Command) == 0 || provider.Command[0] != "claude" {
		t.Fatalf("provider command = %#v, want claude command", provider.Command)
	}
}

func TestProviderTemplateUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"provider", "template", "unknown"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("provider template returned %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unsupported provider template") || !strings.Contains(stderr.String(), "claude") {
		t.Fatalf("stderr did not describe available templates:\n%s", stderr.String())
	}
}

func TestInteractiveCodexDiscoveryCreatesRunFromAgentPlan(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	sourceDir := filepath.Join(projectDir, "internal", "checkout")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pricing.go"), []byte("package checkout\n\nfunc PriceCheckout() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeBinDir := t.TempDir()
	fakeCodex := filepath.Join(fakeBinDir, "codex")
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
cat > "$out" <<'MD'
# Discovery

Use checkout pricing.

` + "```json" + `
{
  "source_path": "internal/checkout/pricing.go",
  "source_path_confidence": "high",
  "drop_in_interface": "PriceCheckout",
  "inputs": ["checkout cart fixture"],
  "outputs": ["priced checkout"],
  "external_communications": [],
  "external_mode": "deny",
  "evaluator_strategy": ["golden fixture test", "benchmark pricing"],
  "metrics": ["p95 latency", "cpu user seconds"],
  "clarifying_questions": ["Which percentile should be optimized?"],
  "suggested_next_command": "crucible run \"reduce checkout pricing latency\" --source-path internal/checkout/pricing.go",
  "notes": ["fake interactive plan"]
}
` + "```" + `
MD
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout, stderr bytes.Buffer
	input := "\n\nreduce checkout pricing latency\n\ny\n\np95 of PriceCheckout\n\nskip\nn\nq\n"
	code := RunWithIO(nil, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	for _, want := range []string{
		"Codex discovery complete",
		"Codex requested clarification before generation.",
		"Source path: internal/checkout/pricing.go",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not contain %q:\n%s", want, stdout.String())
		}
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourcePath != "internal/checkout/pricing.go" {
		t.Fatalf("SourcePath = %q", cfg.SourcePath)
	}
	if !strings.Contains(cfg.Optimize, "Clarifications:") || !strings.Contains(cfg.Optimize, "p95 of PriceCheckout") {
		t.Fatalf("Optimize did not include clarifications:\n%s", cfg.Optimize)
	}

	runDir := filepath.Dir(matches[0])
	for _, path := range []string{
		filepath.Join(runDir, "docs", "agent-discovery.json"),
		filepath.Join(runDir, "docs", "agent-discovery.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected discovery handoff %s: %v", path, err)
		}
	}
	interfaceDoc, err := os.ReadFile(filepath.Join(runDir, "docs", "interfaces.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(interfaceDoc), "Agent Discovery Handoff") {
		t.Fatalf("interface doc did not include agent handoff:\n%s", string(interfaceDoc))
	}
}

func TestInteractiveCommandDiscoveryCreatesRunFromAgentPlan(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeProvider := filepath.Join(t.TempDir(), "fake-agent")
	script := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
mkdir -p "$(dirname "$CRUCIBLE_OUTPUT_LAST_MESSAGE")"
cat > "$CRUCIBLE_OUTPUT_LAST_MESSAGE" <<'MD'
# Discovery

Use the search ranking implementation.

` + "```json" + `
{
  "source_path": "internal/search/rank.go",
  "source_path_confidence": "high",
  "drop_in_interface": "Rank",
  "inputs": ["search query fixture"],
  "outputs": ["ranked results"],
  "external_communications": [],
  "external_mode": "deny",
  "evaluator_strategy": ["golden fixture test", "benchmark ranking"],
  "metrics": ["cpu user seconds"],
  "clarifying_questions": [],
  "suggested_next_command": "crucible run \"make ranking faster\" --source-path internal/search/rank.go",
  "notes": ["fake command provider plan"]
}
` + "```" + `
MD
`
	if err := os.WriteFile(fakeProvider, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := project.Init(projectDir, "search")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultAgent = "mock"
	cfg.AgentProviders = map[string]agent.ProviderDefinition{
		"mock": {
			Name:    "mock",
			Kind:    "command",
			Command: []string{fakeProvider},
			Capabilities: agent.ProviderCapabilities{
				SupportsDiscovery:  true,
				SupportsGeneration: true,
				SupportsEvolution:  true,
				SupportsJSONOutput: true,
			},
		},
	}
	if err := project.Save(projectDir, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	input := "\n1\nmake ranking faster\n\ny\n\n\nskip\nn\nq\n"
	code := RunWithIO(nil, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	for _, want := range []string{
		"mock discovery complete",
		"Use mock recommended internal/search/rank.go as the baseline source path?",
		"Source path: internal/search/rank.go",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not contain %q:\n%s", want, stdout.String())
		}
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	runCfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if runCfg.SourcePath != "internal/search/rank.go" {
		t.Fatalf("SourcePath = %q", runCfg.SourcePath)
	}
}

func TestInteractiveExistingProjectShowsLeaderboard(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)

	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(created.RunDir, "leaderboard.json")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader("\n2\nq\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Found Code Crucible work area") {
		t.Fatalf("stdout did not detect existing setup:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Using only run:") {
		t.Fatalf("stdout did not auto-select the only run:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Run [latest]:") {
		t.Fatalf("stdout prompted for a run despite only one run:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "candidate-0000-baseline") {
		t.Fatalf("stdout did not show leaderboard:\n%s", stdout.String())
	}
}

func TestInteractiveReportPromptsForOutputAndJSON(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	}); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(projectDir, "interactive-report.html")
	var stdout, stderr bytes.Buffer
	input := "\n6\n" + outputPath + "\ny\nq\n"
	code := RunWithIO(nil, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"output_path"`) {
		t.Fatalf("stdout did not include report JSON:\n%s", stdout.String())
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("report output missing: %v", err)
	}
}

func TestInteractiveIndexPromptsForRunAndJSON(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader("\n8\ny\ny\nq\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	for _, want := range []string{`"runs": 1`, `"candidates": 1`, `"rebuild_mode": "run"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not include %q:\n%s", want, stdout.String())
		}
	}
}

func TestInteractiveGeneratePromptsForAdvancedOptions(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	}); err != nil {
		t.Fatal(err)
	}

	editor := filepath.Join(t.TempDir(), "editor.sh")
	editorLog := filepath.Join(t.TempDir(), "editor.log")
	editorScript := "#!/usr/bin/env bash\nset -euo pipefail\nprintf '%s\\n' \"$@\" > \"$EDITOR_LOG\"\n"
	if err := os.WriteFile(editor, []byte(editorScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editor)
	t.Setenv("EDITOR_LOG", editorLog)

	finalPath := filepath.Join(projectDir, "codex-final.md")
	input := "\n3\n\nedit\n\ny\ngpt-test\n" + finalPath + "\nprofile-a\nread-only\non-request\ny\nq\n"
	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	for _, want := range []string{
		"Codex command:",
		"--model gpt-test",
		"--profile profile-a",
		"--sandbox read-only",
		"--ask-for-approval on-request",
		"Final message: " + finalPath,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not include %q:\n%s", want, stdout.String())
		}
	}
	editorData, err := os.ReadFile(editorLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"docs/interfaces.md", "evaluator/evaluator.sh", "prompts/generation-round-0001.md"} {
		if !strings.Contains(string(editorData), want) {
			t.Fatalf("editor did not receive %q:\n%s", want, string(editorData))
		}
	}
}

func TestInteractiveAdvancedEvaluateOptionsBuildArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	session := interactiveSession{
		in:     bufio.NewReader(strings.NewReader("y\ncandidate-0001\n30s\n2\n5\n1\nstrict\ndocker\ngolang:1.22\nnone\n\n512m\n128\n")),
		stdout: &stdout,
		stderr: &stderr,
	}
	got, ok := session.askAdvancedEvaluateOptions()
	if !ok {
		t.Fatalf("askAdvancedEvaluateOptions returned false, stderr: %s", stderr.String())
	}
	want := []string{
		"--candidate", "candidate-0001",
		"--timeout", "30s",
		"--jobs", "2",
		"--nice", "5",
		"--cpu-limit", "1",
		"--sandbox-profile", "strict",
		"--sandbox-engine", "docker",
		"--sandbox-image", "golang:1.22",
		"--sandbox-network", "none",
		"--memory-limit", "512m",
		"--pids-limit", "128",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advanced evaluate args = %#v, want %#v", got, want)
	}
}

func TestInteractiveAgentSettingsUpdatesDefaultAgent(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	if err := os.MkdirAll(filepath.Join(projectDir, ".crucible"), 0o755); err != nil {
		t.Fatal(err)
	}
	providerScript := filepath.Join(t.TempDir(), "provider.sh")
	if err := os.WriteFile(providerScript, []byte("#!/usr/bin/env bash\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := struct {
		Version               int                                 `json:"version"`
		ProjectName           string                              `json:"project_name"`
		DefaultAgent          string                              `json:"default_agent"`
		AgentProviders        map[string]agent.ProviderDefinition `json:"agent_providers"`
		DefaultExternalPolicy model.ExternalPolicy                `json:"default_external_policy"`
		ArchiveDir            string                              `json:"archive_dir"`
	}{
		Version:      1,
		ProjectName:  "fixture",
		DefaultAgent: "codex",
		AgentProviders: map[string]agent.ProviderDefinition{
			"custom": {
				Name:    "custom",
				Kind:    "command",
				Command: []string{providerScript},
				Capabilities: agent.ProviderCapabilities{
					SupportsGeneration: true,
				},
			},
		},
		DefaultExternalPolicy: model.ExternalPolicy{Mode: model.ExternalModeDeny},
		ArchiveDir:            "runs",
	}
	configRaw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".crucible", "config.json"), append(configRaw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader("\n9\ncustom\nq\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Default generation agent updated to custom") {
		t.Fatalf("stdout did not include default update:\n%s", stdout.String())
	}
	raw, err := os.ReadFile(filepath.Join(projectDir, ".crucible", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"default_agent": "custom"`) {
		t.Fatalf("config was not updated:\n%s", raw)
	}
}

func TestInteractiveStandaloneDiscoveryCreatesPlan(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	sourceDir := filepath.Join(projectDir, "internal", "checkout")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pricing.go"), []byte("package checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "seed work area",
		Variants:     1,
		ExternalMode: "deny",
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader("\n10\nreduce checkout pricing latency\n\nq\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Discovery-capable agents:") {
		t.Fatalf("stdout did not list discovery agents:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Created discovery plan") {
		t.Fatalf("stdout did not create discovery plan:\n%s", stdout.String())
	}
	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "discoveries", "*", "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one discovery plan, found %d", len(matches))
	}
}

func TestInteractiveAdoptGeneratedCandidate(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidateDir := filepath.Join(created.RunDir, "round-0001", "candidate-0001")
	if err := os.MkdirAll(filepath.Join(candidateDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "src", "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "design.md"), []byte("# Candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	candidate := model.Candidate{
		ID:         "candidate-0001",
		Name:       "generated candidate",
		Round:      1,
		ParentIDs:  []string{"candidate-0000-baseline"},
		Agent:      "codex",
		SourcePath: "src",
	}
	candidateRaw, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "candidate.json"), append(candidateRaw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := RunWithIO(nil, strings.NewReader("\n11\n\nq\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Added: candidate-0001") {
		t.Fatalf("stdout did not include adoption:\n%s", stdout.String())
	}
}

func TestInteractiveQueryRuns(t *testing.T) {
	projectDir := t.TempDir()
	chdir(t, projectDir)
	if _, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		Variants:     1,
		ExternalMode: "deny",
	}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"index", "--project", projectDir}, &stdout, &stderr); code != 0 {
		t.Fatalf("index returned %d, stderr: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := RunWithIO(nil, strings.NewReader("\n13\n\n\nq\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunWithIO returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "make ranking faster") {
		t.Fatalf("stdout did not include query result:\n%s", stdout.String())
	}
}

func TestDiscoverCreatesPlan(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "checkout")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pricing.go"), []byte("package checkout\n\nfunc PriceCheckout() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"discover",
		"reduce checkout pricing latency",
		"--project-dir", projectDir,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("discover returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Created discovery plan") {
		t.Fatalf("stdout did not include discovery creation:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "internal/checkout/pricing.go") {
		t.Fatalf("stdout did not include source suggestion:\n%s", stdout.String())
	}
	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "discoveries", "*", "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one discovery plan, found %d", len(matches))
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "internal/checkout/pricing.go") {
		t.Fatalf("plan did not include source suggestion:\n%s", string(raw))
	}
}

func TestDiscoverCodexDryRunPrintsCommand(t *testing.T) {
	projectDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"discover",
		"reduce checkout pricing latency",
		"--project-dir", projectDir,
		"--agent", "codex",
		"--dry-run",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("discover returned %d, stderr: %s", code, stderr.String())
	}
	for _, want := range []string{"Created discovery plan", "Codex command:", "Discovery archive:", "Agent plan:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not contain %q:\n%s", want, stdout.String())
		}
	}
}

func TestDiscoverCodexCapturesStructuredAgentPlan(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "checkout")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pricing.go"), []byte("package checkout\n\nfunc PriceCheckout() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeCodex := filepath.Join(t.TempDir(), "codex")
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
cat > "$out" <<'MD'
# Discovery

Use the checkout pricing source.

` + "```json" + `
{
  "source_path": "internal/checkout/pricing.go",
  "source_path_confidence": "high",
  "drop_in_interface": "PriceCheckout",
  "inputs": ["checkout cart fixture"],
  "outputs": ["priced checkout"],
  "external_communications": [],
  "external_mode": "deny",
  "evaluator_strategy": ["golden fixture test", "benchmark pricing"],
  "metrics": ["p95 latency", "cpu user seconds"],
  "clarifying_questions": [],
  "suggested_next_command": "crucible run \"reduce checkout pricing latency\" --source-path internal/checkout/pricing.go",
  "notes": ["fake plan"]
}
` + "```" + `
MD
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"discover",
		"reduce checkout pricing latency",
		"--project-dir", projectDir,
		"--agent", "codex",
		"--codex-bin", fakeCodex,
		"--event-json=false",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("discover returned %d, stderr: %s", code, stderr.String())
	}
	for _, want := range []string{"Codex discovery complete", "Structured plan:", "Recommended source path: internal/checkout/pricing.go"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not contain %q:\n%s", want, stdout.String())
		}
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "discoveries", "*", "agent-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one structured agent plan, found %d", len(matches))
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		SourcePath string `json:"source_path"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.SourcePath != "internal/checkout/pricing.go" {
		t.Fatalf("SourcePath = %q", plan.SourcePath)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}

func TestRunGenerateShortcutInvokesCodex(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeCodex := filepath.Join(t.TempDir(), "codex")
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
printf 'fake codex complete\n' > "$out"
round="$(find .crucible/runs -name round-0001 -type d | sort | tail -n1)"
mkdir -p "$round/candidate-0001/src"
printf 'package search\n\nfunc Rank() int { return 2 }\n' > "$round/candidate-0001/src/rank.go"
printf '# Candidate\n' > "$round/candidate-0001/design.md"
cat > "$round/candidate-0001/candidate.json" <<'JSON'
{
  "id": "candidate-0001",
  "name": "fake generated candidate",
  "round": 1,
  "parent_ids": ["candidate-0000-baseline"],
  "agent": "codex",
  "source_path": "src",
  "baseline": false
}
JSON
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
		"--variants", "2",
		"--generate",
		"--codex-bin", fakeCodex,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d, stderr: %s", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "Codex generation complete") {
		t.Fatalf("stdout did not include generation completion:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Added: candidate-0001") {
		t.Fatalf("stdout did not include adoption result:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Evaluator warning: this run uses the placeholder evaluator scaffold.") {
		t.Fatalf("stdout did not include evaluator warning:\n%s", stdout.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "agents", "codex-final.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one Codex final message artifact, found %d", len(matches))
	}
}

func TestGenerateDoesNotWarnAboutEvaluatorWhenRunHasPassedResults(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "internal/search/rank.go",
		Variants:     1,
		ExternalMode: "deny",
		Agent:        "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	boardPath := filepath.Join(created.RunDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(boardPath)
	if err != nil {
		t.Fatal(err)
	}
	board.Results[0].Status = model.CandidateStatusPassed
	board.Results[0].Verdict = model.Verdict{
		CorrectnessPassed:    true,
		BenchmarkPassed:      true,
		ExternalPolicyPassed: true,
	}
	board.Results[0].Metrics.RuntimeMeanMS = 1
	if err := archive.SaveJSON(boardPath, board); err != nil {
		t.Fatal(err)
	}

	fakeCodex := filepath.Join(t.TempDir(), "codex")
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
printf 'fake codex complete\n' > "$out"
round="$(find .crucible/runs -name round-0001 -type d | sort | tail -n1)"
mkdir -p "$round/candidate-0001/src"
printf 'package search\n\nfunc Rank() int { return 2 }\n' > "$round/candidate-0001/src/rank.go"
printf '# Candidate\n' > "$round/candidate-0001/design.md"
cat > "$round/candidate-0001/candidate.json" <<'JSON'
{
  "id": "candidate-0001",
  "name": "fake generated candidate",
  "round": 1,
  "parent_ids": ["candidate-0000-baseline"],
  "agent": "codex",
  "source_path": "src",
  "baseline": false
}
JSON
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"generate",
		"--project-dir", projectDir,
		"--run", created.ID,
		"--codex-bin", fakeCodex,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate returned %d, stderr: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "Evaluator warning: this run uses the placeholder evaluator scaffold.") {
		t.Fatalf("stdout included stale evaluator warning:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "Generate evaluator:") {
		t.Fatalf("stdout included stale evaluator generation guidance:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Evaluate candidates:") {
		t.Fatalf("stdout did not include evaluate next step:\n%s", stdout.String())
	}
}

func TestRunGenerateUsesConfiguredCommandProvider(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	providerScript := filepath.Join(t.TempDir(), "provider.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail
prompt="$(cat)"
bt=$'\140'
round_dir="$(awk -v bt="$bt" 'index($0, "Current round directory:") { split($0, a, bt); print a[2]; exit }' <<< "$prompt")"
candidate="$(awk -v bt="$bt" 'index($0, "Create candidates starting at") { split($0, a, bt); print a[2]; exit }' <<< "$prompt")"
if [[ -z "$round_dir" || -z "$candidate" ]]; then
  echo "failed to parse prompt" >&2
  exit 9
fi
mkdir -p "$round_dir/$candidate/src"
printf 'package search\n\nfunc Rank() int { return 3 }\n' > "$round_dir/$candidate/src/rank.go"
printf '# Candidate\n' > "$round_dir/$candidate/design.md"
cat > "$round_dir/$candidate/candidate.json" <<JSON
{
  "id": "$candidate",
  "name": "command $candidate",
  "round": 1,
  "parent_ids": ["candidate-0000-baseline"],
  "agent": "$CRUCIBLE_PROVIDER_NAME",
  "source_path": "src",
  "baseline": false
}
JSON
printf 'command provider complete\n'
`
	if err := os.WriteFile(providerScript, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(projectDir, ".crucible"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := struct {
		Version               int                                 `json:"version"`
		ProjectName           string                              `json:"project_name"`
		DefaultAgent          string                              `json:"default_agent"`
		AgentProviders        map[string]agent.ProviderDefinition `json:"agent_providers"`
		DefaultExternalPolicy model.ExternalPolicy                `json:"default_external_policy"`
		ArchiveDir            string                              `json:"archive_dir"`
	}{
		Version:      1,
		ProjectName:  "fixture",
		DefaultAgent: "custom",
		AgentProviders: map[string]agent.ProviderDefinition{
			"custom": {
				Name:    "custom",
				Kind:    "command",
				Command: []string{providerScript},
				Capabilities: agent.ProviderCapabilities{
					SupportsGeneration: true,
					SupportsEvolution:  true,
				},
			},
		},
		DefaultExternalPolicy: model.ExternalPolicy{Mode: model.ExternalModeDeny},
		ArchiveDir:            "runs",
	}
	configRaw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".crucible", "config.json"), append(configRaw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
		"--variants", "1",
		"--generate",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s\nstdout:\n%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "custom generation complete") {
		t.Fatalf("stdout did not include custom provider completion:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Added: candidate-0001") {
		t.Fatalf("stdout did not include adoption result:\n%s", stdout.String())
	}
	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "agents", "custom-*-invocation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one command provider invocation, found %d", len(matches))
	}
}

func TestEvaluatorGenerateUsesConfiguredCommandProvider(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	providerScript := filepath.Join(t.TempDir(), "provider.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
cat > "$CRUCIBLE_RUN_DIR/evaluator/evaluator.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
metrics_out="${3:?metrics path required}"
verdict_out="${4:?verdict path required}"
cat > "$metrics_out" <<'JSON'
{
  "runtime_mean_ms": 1,
  "p95_latency_ms": 1,
  "memory_peak_bytes": 1024
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
SH
cat > "$CRUCIBLE_RUN_DIR/evaluator/evaluator.md" <<'MD'
# Evaluator

Runs a deterministic smoke benchmark for the archived candidate.
MD
printf 'evaluator provider complete\n'
`
	if err := os.WriteFile(providerScript, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := project.Init(projectDir, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultAgent = "custom"
	cfg.AgentProviders = map[string]agent.ProviderDefinition{
		"custom": {
			Name:    "custom",
			Kind:    "command",
			Command: []string{providerScript},
			Capabilities: agent.ProviderCapabilities{
				SupportsGeneration: true,
			},
		},
	}
	if err := project.Save(projectDir, cfg); err != nil {
		t.Fatal(err)
	}

	created, err := run.Create(run.Options{
		ProjectDir: projectDir,
		Optimize:   "make ranking faster",
		SourcePath: "internal/search/rank.go",
		Variants:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if board.Results[0].Status != model.CandidateStatusNeedsEvaluator {
		t.Fatalf("baseline status = %q, want needs-evaluator", board.Results[0].Status)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"evaluator",
		"generate",
		"--project", projectDir,
		"--run", created.ID,
		"--agent", "custom",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("evaluator generate returned %d, stderr: %s\nstdout:\n%s", code, stderr.String(), stdout.String())
	}
	for _, want := range []string{
		"evaluator provider complete",
		"Validation:",
		"Evaluator generation complete",
		"Ready for evaluation: candidate-0000-baseline",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not include %q:\n%s", want, stdout.String())
		}
	}

	runCfg, err := archive.LoadRunConfig(filepath.Join(created.RunDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !runCfg.EvaluatorGenerated {
		t.Fatal("run config was not marked evaluator_generated")
	}
	var validation struct {
		Status      string `json:"status"`
		CandidateID string `json:"candidate_id"`
	}
	validationRaw, err := os.ReadFile(filepath.Join(created.RunDir, "evaluator", "validation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(validationRaw, &validation); err != nil {
		t.Fatal(err)
	}
	if validation.Status != "passed" || validation.CandidateID != "candidate-0000-baseline" {
		t.Fatalf("validation = %#v, want passed baseline", validation)
	}
	board, err = archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if board.Results[0].Status != "pending" {
		t.Fatalf("baseline status = %q, want pending", board.Results[0].Status)
	}
	prompt, err := os.ReadFile(filepath.Join(created.RunDir, "prompts", "evaluator-generation.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "Code Crucible Evaluator Generation Prompt") {
		t.Fatalf("evaluator prompt missing expected heading:\n%s", prompt)
	}
	evaluatorScript, err := os.ReadFile(filepath.Join(created.RunDir, "evaluator", "evaluator.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(evaluatorScript), "runtime_mean_ms") {
		t.Fatalf("generated evaluator did not contain expected metric:\n%s", evaluatorScript)
	}
	matches, err := filepath.Glob(filepath.Join(created.RunDir, "agents", "custom-*-invocation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one command provider invocation, found %d", len(matches))
	}
}

func TestEvaluatorGenerateRejectsEvaluatorThatFailsBaseline(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	providerScript := filepath.Join(t.TempDir(), "provider.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
cat > "$CRUCIBLE_RUN_DIR/evaluator/evaluator.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
metrics_out="${3:?metrics path required}"
verdict_out="${4:?verdict path required}"
cat > "$metrics_out" <<'JSON'
{"runtime_mean_ms": 1}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": false,
  "benchmark_passed": true,
  "external_policy_passed": true,
  "errors": ["baseline failed intentionally"]
}
JSON
SH
printf 'bad evaluator provider complete\n'
`
	if err := os.WriteFile(providerScript, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := project.Init(projectDir, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultAgent = "custom"
	cfg.AgentProviders = map[string]agent.ProviderDefinition{
		"custom": {
			Name:    "custom",
			Kind:    "command",
			Command: []string{providerScript},
			Capabilities: agent.ProviderCapabilities{
				SupportsGeneration: true,
			},
		},
	}
	if err := project.Save(projectDir, cfg); err != nil {
		t.Fatal(err)
	}
	created, err := run.Create(run.Options{
		ProjectDir: projectDir,
		Optimize:   "make ranking faster",
		SourcePath: "internal/search/rank.go",
		Variants:   1,
	})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"evaluator",
		"generate",
		"--project", projectDir,
		"--run", created.ID,
		"--agent", "custom",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("evaluator generate returned %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "generated evaluator validation failed") {
		t.Fatalf("stderr did not include validation failure:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Validation:") {
		t.Fatalf("stdout did not include validation report:\n%s", stdout.String())
	}

	runCfg, err := archive.LoadRunConfig(filepath.Join(created.RunDir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runCfg.EvaluatorGenerated {
		t.Fatal("run config was marked evaluator_generated despite validation failure")
	}
	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if board.Results[0].Status != model.CandidateStatusNeedsEvaluator {
		t.Fatalf("baseline status = %q, want needs-evaluator", board.Results[0].Status)
	}
	var validation struct {
		Status string   `json:"status"`
		Errors []string `json:"errors"`
	}
	validationRaw, err := os.ReadFile(filepath.Join(created.RunDir, "evaluator", "validation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(validationRaw, &validation); err != nil {
		t.Fatal(err)
	}
	if validation.Status != "failed" || !strings.Contains(strings.Join(validation.Errors, "\n"), "baseline candidate did not pass the generated evaluator") {
		t.Fatalf("validation = %#v, want failed baseline error", validation)
	}
}

func TestEvaluatorGenerateReportsMissingCodexBinaryBeforeInvocation(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n\nfunc Rank() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := run.Create(run.Options{
		ProjectDir: projectDir,
		Optimize:   "make ranking faster",
		SourcePath: "internal/search/rank.go",
		Variants:   1,
	})
	if err != nil {
		t.Fatal(err)
	}

	missingCodex := filepath.Join(t.TempDir(), "missing-codex")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"evaluator",
		"generate",
		"--project", projectDir,
		"--run", created.ID,
		"--agent", "codex",
		"--codex-bin", missingCodex,
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("evaluator generate returned %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "executable") || !strings.Contains(stderr.String(), "--codex-bin") {
		t.Fatalf("stderr did not explain missing Codex binary:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "Running codex") {
		t.Fatalf("stdout showed provider execution despite preflight failure:\n%s", stdout.String())
	}
	matches, err := filepath.Glob(filepath.Join(created.RunDir, "agents", "codex-*-invocation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no Codex invocation archive, found %d", len(matches))
	}
	board, err := archive.LoadLeaderboard(filepath.Join(created.RunDir, "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if board.Results[0].Status != model.CandidateStatusNeedsEvaluator {
		t.Fatalf("baseline status = %q, want needs-evaluator", board.Results[0].Status)
	}
}

func TestRunGenerateRejectsUnsupportedAgentBeforeCreatingRun(t *testing.T) {
	projectDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--generate",
		"--agent", "prompt",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("Run returned %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unsupported agent provider "prompt"`) {
		t.Fatalf("stderr did not explain unsupported agent:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".crucible", "runs")); !os.IsNotExist(err) {
		t.Fatalf("expected no run archive to be created, stat err: %v", err)
	}
}

func TestGenerateDryRunUsesRunAgentDefault(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
		"--agent", "codex",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"generate",
		"--project", projectDir,
		"--dry-run",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("generate returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Agent provider: codex") {
		t.Fatalf("stdout did not include resolved provider:\n%s", stdout.String())
	}
}

func TestRunPositionalOptimizeCreatesRun(t *testing.T) {
	projectDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"make checkout pricing faster",
		"--project-dir", projectDir,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Optimize != "make checkout pricing faster" {
		t.Fatalf("Optimize = %q, want positional prompt", cfg.Optimize)
	}
	if !strings.Contains(stdout.String(), "Created run") {
		t.Fatalf("stdout did not include run creation:\n%s", stdout.String())
	}
	for _, want := range []string{
		"Run setup complete; no generation or evaluation process is still running.",
		"Generate candidates: crucible generate --project-dir",
		"Evaluate candidates: crucible evaluate --project-dir",
		"Evaluator warning: this run uses the placeholder evaluator scaffold.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout did not include %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunRequiresOptimizationRequest(t *testing.T) {
	projectDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "optimization request is required") {
		t.Fatalf("stderr did not explain missing request:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".crucible")); !os.IsNotExist(err) {
		t.Fatalf("expected no work area to be created, stat err: %v", err)
	}
}

func TestRunTaskFileCreatesRun(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	task := "# Optimization Task\n\nMake ranking faster.\n"
	taskPath := filepath.Join(projectDir, "task.md")
	if err := os.WriteFile(taskPath, []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--task-file", filepath.Base(taskPath),
		"--source-path", "internal/search/rank.go",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Optimize != strings.TrimSpace(task) {
		t.Fatalf("Optimize = %q, want %q", cfg.Optimize, strings.TrimSpace(task))
	}
}

func TestRunAgentPlanSeedsSourcePathAndDocs(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "checkout")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pricing.go"), []byte("package checkout\n\nfunc PriceCheckout() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(projectDir, "agent-plan.json")
	planJSON := `{
  "source_path": "internal/checkout/pricing.go",
  "source_path_confidence": "high",
  "drop_in_interface": "PriceCheckout(cart) Money",
  "inputs": ["cart fixture"],
  "outputs": ["priced total"],
  "evaluator_strategy": ["golden fixture comparison"],
  "metrics": ["p95 latency"],
  "external_mode": "deny"
}
`
	if err := os.WriteFile(planPath, []byte(planJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"reduce checkout pricing latency",
		"--project-dir", projectDir,
		"--agent-plan", filepath.Base(planPath),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourcePath != "internal/checkout/pricing.go" {
		t.Fatalf("SourcePath = %q", cfg.SourcePath)
	}
	runDir := filepath.Dir(matches[0])
	interfaceDoc, err := os.ReadFile(filepath.Join(runDir, "docs", "interfaces.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(interfaceDoc), "PriceCheckout(cart) Money") {
		t.Fatalf("interfaces.md did not include agent plan:\n%s", string(interfaceDoc))
	}
	if _, err := os.Stat(filepath.Join(runDir, "docs", "agent-discovery.json")); err != nil {
		t.Fatalf("agent-discovery.json missing: %v", err)
	}
}

func TestRunRejectsOptimizeAndTaskFileTogether(t *testing.T) {
	projectDir := t.TempDir()
	taskPath := filepath.Join(projectDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("make ranking faster\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--task-file", taskPath,
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "provide the optimization request only once") {
		t.Fatalf("stderr did not explain task conflict:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".crucible", "runs")); !os.IsNotExist(err) {
		t.Fatalf("expected no run archive to be created, stat err: %v", err)
	}
}

func TestEvaluateCommandUpdatesLeaderboard(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
		"--evaluator", "true",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"evaluate",
		"--project", projectDir,
		"--candidate", "candidate-0000-baseline",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("evaluate returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "candidate-0000-baseline") {
		t.Fatalf("stdout did not include evaluated candidate:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "passed") {
		t.Fatalf("stdout did not include passed status:\n%s", stdout.String())
	}
}

func TestEvaluateRequirePassedFailsWhenCandidateFails(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
		"--evaluator", "false",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"evaluate",
		"--project", projectDir,
		"--candidate", "candidate-0000-baseline",
		"--require-passed",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("evaluate returned %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "failed") {
		t.Fatalf("stdout did not include failed status:\n%s", stdout.String())
	}
}

func TestIndexCommandRebuildsSQLiteIndex(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"index",
		"--project", projectDir,
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("index returned %d, stderr: %s", code, stderr.String())
	}

	var report struct {
		IndexPath  string `json:"index_path"`
		Runs       int    `json:"runs"`
		Candidates int    `json:"candidates"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("index output was not JSON: %v\n%s", err, stdout.String())
	}
	if report.Runs != 1 || report.Candidates != 1 {
		t.Fatalf("report counts = runs %d candidates %d, want 1 and 1", report.Runs, report.Candidates)
	}
	if _, err := os.Stat(report.IndexPath); err != nil {
		t.Fatalf("index file missing: %v", err)
	}
}

func TestReportCommandWritesHTMLReport(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	outputPath := filepath.Join(projectDir, "report.html")
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"report",
		"--project", projectDir,
		"--output", outputPath,
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("report returned %d, stderr: %s", code, stderr.String())
	}

	var report struct {
		OutputPath string `json:"output_path"`
		Candidates int    `json:"candidates"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("report output was not JSON: %v\n%s", err, stdout.String())
	}
	if report.OutputPath != filepath.ToSlash(outputPath) || report.Candidates != 1 {
		t.Fatalf("report metadata = path %q candidates %d", report.OutputPath, report.Candidates)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Code Crucible Report") {
		t.Fatalf("HTML report missing title:\n%s", string(data))
	}
}

func TestQueryCommandReadsSQLiteIndex(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"index", "--project", projectDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("index returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"query", "candidates", "--project", projectDir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("query returned %d, stderr: %s", code, stderr.String())
	}

	var candidates []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &candidates); err != nil {
		t.Fatalf("query output was not JSON: %v\n%s", err, stdout.String())
	}
	if len(candidates) != 1 || candidates[0].ID != "candidate-0000-baseline" {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestNextRoundCommandPreparesActiveRound(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
		"--evaluator", "true",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"evaluate",
		"--project", projectDir,
		"--candidate", "candidate-0000-baseline",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("evaluate returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"next-round",
		"--project", projectDir,
		"--parents", "1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("next-round returned %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Prepared round 2") {
		t.Fatalf("stdout did not describe prepared round:\n%s", stdout.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one run config, found %d", len(matches))
	}
	cfg, err := archive.LoadRunConfig(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.FromSlash(cfg.RoundDir), "round-0002") {
		t.Fatalf("RoundDir = %q, want round-0002", cfg.RoundDir)
	}
}

func TestEvolveCommandRunsGenerateEvaluateCycles(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "internal", "search")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package search\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evaluatorPath := filepath.Join(projectDir, "evaluator.sh")
	evaluator := `#!/usr/bin/env bash
set -euo pipefail
candidate_dir="$1"
metrics_out="$3"
verdict_out="$4"
id="$(basename "$candidate_dir")"
runtime=30
if [[ "$id" == "candidate-0001" ]]; then
  runtime=10
elif [[ "$id" == "candidate-0002" ]]; then
  runtime=5
fi
cat > "$metrics_out" <<JSON
{
  "runtime_mean_ms": $runtime,
  "p95_latency_ms": $runtime,
  "memory_peak_bytes": 1024
}
JSON
cat > "$verdict_out" <<'JSON'
{
  "correctness_passed": true,
  "benchmark_passed": true,
  "external_policy_passed": true
}
JSON
`
	if err := os.WriteFile(evaluatorPath, []byte(evaluator), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeCodex := filepath.Join(t.TempDir(), "codex")
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
prompt="$(cat)"
bt=$'\140'
round_dir="$(awk -v bt="$bt" 'index($0, "Current round directory:") { split($0, a, bt); print a[2]; exit }' <<< "$prompt")"
candidate="$(awk -v bt="$bt" 'index($0, "Create candidates starting at") { split($0, a, bt); print a[2]; exit }' <<< "$prompt")"
round="$(awk '/Generate .* round / { for (i = 1; i <= NF; i++) if ($i == "round") { gsub(/[^0-9]/, "", $(i+1)); print $(i+1); exit } }' <<< "$prompt")"
parent="$(awk -v bt="$bt" 'index($0, "Preferred parent candidates:") { split($0, a, bt); print a[2]; exit }' <<< "$prompt")"
if [[ -z "$round_dir" || -z "$candidate" || -z "$round" || -z "$parent" ]]; then
  echo "failed to parse prompt" >&2
  exit 9
fi
mkdir -p "$(dirname "$out")"
printf 'fake codex complete\n' > "$out"
mkdir -p "$round_dir/$candidate/src"
printf 'package search\n' > "$round_dir/$candidate/src/rank.go"
printf '# Candidate\n' > "$round_dir/$candidate/design.md"
cat > "$round_dir/$candidate/candidate.json" <<JSON
{
  "id": "$candidate",
  "name": "fake $candidate",
  "round": $round,
  "parent_ids": ["$parent"],
  "agent": "codex",
  "source_path": "src",
  "baseline": false
}
JSON
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"run",
		"--project", projectDir,
		"--optimize", "make ranking faster",
		"--source-path", "internal/search/rank.go",
		"--evaluator-script", filepath.Base(evaluatorPath),
		"--variants", "1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run returned %d, stderr: %s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{
		"evolve",
		"--project", projectDir,
		"--rounds", "2",
		"--parents", "1",
		"--codex-bin", fakeCodex,
		"--nice", "0",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("evolve returned %d, stderr: %s\nstdout:\n%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "Evolution cycle 2 of 2") {
		t.Fatalf("stdout did not include second cycle:\n%s", stdout.String())
	}

	matches, err := filepath.Glob(filepath.Join(projectDir, ".crucible", "runs", "*", "leaderboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one leaderboard, found %d", len(matches))
	}
	board, err := archive.LoadLeaderboard(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, result := range board.Results {
		seen[result.Candidate.ID] = true
	}
	for _, want := range []string{"candidate-0001", "candidate-0002"} {
		if !seen[want] {
			t.Fatalf("leaderboard missing %s: %#v", want, board.Results)
		}
	}
	cfg, err := archive.LoadRunConfig(filepath.Join(filepath.Dir(matches[0]), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.FromSlash(cfg.RoundDir), "round-0002") {
		t.Fatalf("RoundDir = %q, want round-0002", cfg.RoundDir)
	}
}

func TestEvaluateRejectsInvalidResourceOptions(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "jobs",
			args: []string{"evaluate", "--jobs", "0"},
			want: "--jobs must be at least 1",
		},
		{
			name: "nice",
			args: []string{"evaluate", "--nice", "20"},
			want: "--nice must be between 0 and 19",
		},
		{
			name: "cpu limit",
			args: []string{"evaluate", "--cpu-limit", "-1"},
			want: "--cpu-limit must be at least 0",
		},
		{
			name: "pids limit",
			args: []string{"evaluate", "--pids-limit", "-1"},
			want: "--pids-limit must be at least 0",
		},
		{
			name: "sandbox engine",
			args: []string{"evaluate", "--sandbox-engine", "jail"},
			want: "--sandbox-engine must be local, docker, or podman",
		},
		{
			name: "sandbox image",
			args: []string{"evaluate", "--sandbox-engine", "docker"},
			want: "--sandbox-image is required when --sandbox-engine is docker",
		},
		{
			name: "local sandbox image",
			args: []string{"evaluate", "--sandbox-image", "golang:1.25"},
			want: "--sandbox-image requires --sandbox-engine docker or podman",
		},
		{
			name: "local memory limit",
			args: []string{"evaluate", "--memory-limit", "1g"},
			want: "--memory-limit requires --sandbox-engine docker or podman",
		},
		{
			name: "local sandbox profile",
			args: []string{"evaluate", "--sandbox-profile", "strict"},
			want: "--sandbox-profile strict requires --sandbox-engine docker or podman",
		},
		{
			name: "invalid sandbox profile",
			args: []string{"evaluate", "--sandbox-profile", "locked"},
			want: "--sandbox-profile must be default, strict, or networked",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tt.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("Run returned %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestRankedResultsPrioritizesPassedScoreThenLatency(t *testing.T) {
	results := []model.CandidateResult{
		{
			Candidate: model.Candidate{ID: "candidate-0003"},
			Status:    "generated",
			Score:     9999,
		},
		{
			Candidate: model.Candidate{ID: "candidate-0002"},
			Status:    "passed",
			Score:     900,
			Metrics:   model.Metrics{P95LatencyMS: 30},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0001"},
			Status:    "passed",
			Score:     900,
			Metrics:   model.Metrics{P95LatencyMS: 20},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0004"},
			Status:    "failed",
			Score:     1000,
		},
	}

	ranked := rankedResults(results)
	got := []string{
		ranked[0].Candidate.ID,
		ranked[1].Candidate.ID,
		ranked[2].Candidate.ID,
		ranked[3].Candidate.ID,
	}
	want := []string{"candidate-0001", "candidate-0002", "candidate-0004", "candidate-0003"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ranked order = %#v, want %#v", got, want)
		}
	}
}

func TestDynamicFloatFormatterPreservesSmallDifferences(t *testing.T) {
	formatter := newDynamicFloatFormatter([]float64{0.003245, 0.003246, 11.85616}, 2, 6)

	if got := formatter.format(0.003245); got != "0.003245" {
		t.Fatalf("formatted tiny value = %q, want enough precision to distinguish values", got)
	}
	if got := formatter.format(11.85616); got != "11.85616" {
		t.Fatalf("formatted larger value = %q, want shared precision in column", got)
	}
}

func TestDynamicFloatFormatterUsesScientificForVeryWideColumns(t *testing.T) {
	formatter := newDynamicFloatFormatter([]float64{0.00000012, 4_200_000}, 2, 6)

	if got := formatter.format(0.00000012); got != "1.200000e-07" {
		t.Fatalf("formatted wide-range tiny value = %q, want scientific notation", got)
	}
	if got := formatter.format(4_200_000); got != "4.200000e+06" {
		t.Fatalf("formatted wide-range large value = %q, want scientific notation", got)
	}
}

func TestDynamicFloatFormatterUsesSamePresentationWithinColumn(t *testing.T) {
	formatter := newDynamicFloatFormatter([]float64{1910, 166832, 8193865}, 0, 4)

	for _, value := range []float64{1910, 166832, 8193865} {
		if got := formatter.format(value); !strings.Contains(got, "e") {
			t.Fatalf("formatted %f as %q, want scientific notation for every value in scientific column", value, got)
		}
	}
}

func TestLeaderboardTableShowsScoringDrivers(t *testing.T) {
	results := []model.CandidateResult{
		{
			Candidate: model.Candidate{ID: "candidate-0002"},
			Status:    "passed",
			Score:     1000,
			Metrics: model.Metrics{
				P95LatencyMS:     0.001914,
				BenchmarkNsPerOp: 1910,
				MemoryPeakBytes:  208,
				CPUUserSeconds:   6,
				CPUSystemSeconds: 0.18,
			},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0001"},
			Status:    "passed",
			Score:     282.2153,
			Metrics: model.Metrics{
				P95LatencyMS:     0.16709,
				BenchmarkNsPerOp: 166832,
				MemoryPeakBytes:  32768,
				CPUUserSeconds:   5.9,
				CPUSystemSeconds: 0.1,
			},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0000-baseline", Baseline: true},
			Status:    "passed",
			Score:     0,
			Metrics: model.Metrics{
				P95LatencyMS:     8.206311,
				BenchmarkNsPerOp: 8193865,
				MemoryPeakBytes:  32976,
				CPUUserSeconds:   10,
				CPUSystemSeconds: 0.4,
			},
		},
	}

	var out bytes.Buffer
	printLeaderboardTable(&out, results)
	table := out.String()

	for _, want := range []string{"Speedup", "Memory", "Mem/Base", "Eval CPU s", "ns/op", "1.9100e+03", "1.6683e+05", "8.1939e+06", "4288x", "49.1x", "0.0063x", "0.99x", "1x", "208 B", "32 KiB"} {
		if !strings.Contains(table, want) {
			t.Fatalf("leaderboard table missing %q:\n%s", want, table)
		}
	}
	if strings.Contains(table, "Ext Calls") {
		t.Fatalf("leaderboard table should omit external call column when all counts are zero:\n%s", table)
	}
	if !strings.Contains(table, "candidate-0000-baseline") || !strings.Contains(table, "  0 ") {
		t.Fatalf("leaderboard table should show zero score for baseline:\n%s", table)
	}
}

func TestLeaderboardTableShowsExternalCallsWhenPresent(t *testing.T) {
	results := []model.CandidateResult{
		{
			Candidate: model.Candidate{ID: "candidate-0000-baseline", Baseline: true},
			Status:    "passed",
			Metrics: model.Metrics{
				P95LatencyMS:     10,
				BenchmarkNsPerOp: 10_000_000,
				MemoryPeakBytes:  1024,
			},
		},
		{
			Candidate: model.Candidate{ID: "candidate-0001"},
			Status:    "passed",
			Metrics: model.Metrics{
				P95LatencyMS:      5,
				BenchmarkNsPerOp:  5_000_000,
				MemoryPeakBytes:   512,
				ExternalCallCount: 3,
			},
		},
	}

	var out bytes.Buffer
	printLeaderboardTable(&out, results)
	table := out.String()

	for _, want := range []string{"Ext Calls", "candidate-0000-baseline", "candidate-0001", "  0", "  3"} {
		if !strings.Contains(table, want) {
			t.Fatalf("leaderboard table missing %q:\n%s", want, table)
		}
	}
}

func TestLeaderboardExternalCallCountFallsBackToTraceCount(t *testing.T) {
	result := model.CandidateResult{
		Metrics:  model.Metrics{ExternalCallCount: 0},
		External: model.ExternalCallTrace{RequestCount: 4},
	}

	if got := leaderboardExternalCallCount(result); got != 4 {
		t.Fatalf("external call count = %d, want trace request count fallback", got)
	}
}

func TestFormatMultiplier(t *testing.T) {
	for _, tt := range []struct {
		name     string
		baseline float64
		value    float64
		want     string
	}{
		{name: "same", baseline: 100, value: 100, want: "1x"},
		{name: "large improvement", baseline: 32976, value: 208, want: "159x"},
		{name: "small improvement", baseline: 32976, value: 32768, want: "1.01x"},
		{name: "regression", baseline: 100, value: 200, want: "0.50x"},
		{name: "missing", baseline: 0, value: 200, want: "-"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatMultiplier(tt.baseline, tt.value); got != tt.want {
				t.Fatalf("formatMultiplier(%f, %f) = %q, want %q", tt.baseline, tt.value, got, tt.want)
			}
		})
	}
}

func TestFormatRelativeUsage(t *testing.T) {
	for _, tt := range []struct {
		name     string
		value    float64
		baseline float64
		want     string
	}{
		{name: "same", value: 100, baseline: 100, want: "1x"},
		{name: "much less", value: 208, baseline: 32976, want: "0.0063x"},
		{name: "slightly less", value: 32768, baseline: 32976, want: "0.99x"},
		{name: "regression", value: 200, baseline: 100, want: "2.00x"},
		{name: "missing", value: 200, baseline: 0, want: "-"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRelativeUsage(tt.value, tt.baseline); got != tt.want {
				t.Fatalf("formatRelativeUsage(%f, %f) = %q, want %q", tt.value, tt.baseline, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	for _, tt := range []struct {
		value int64
		want  string
	}{
		{value: 0, want: "-"},
		{value: 208, want: "208 B"},
		{value: 32768, want: "32 KiB"},
		{value: 1048576, want: "1 MiB"},
	} {
		if got := formatBytes(tt.value); got != tt.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}
