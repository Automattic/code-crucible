package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type CommandProviderOptions struct {
	Provider          ProviderDefinition
	ProjectDir        string
	RunDir            string
	PromptPath        string
	Model             string
	OutputLastMessage string
}

type CommandProviderInvocation struct {
	ProviderName      string                 `json:"provider_name"`
	ProviderKind      string                 `json:"provider_kind"`
	Model             string                 `json:"model,omitempty"`
	EnvironmentPolicy AgentEnvironmentPolicy `json:"environment_policy"`
	Command           []string               `json:"command"`
	ProjectDir        string                 `json:"project_dir"`
	RunDir            string                 `json:"run_dir"`
	PromptPath        string                 `json:"prompt_path"`
	ScratchDir        string                 `json:"scratch_dir,omitempty"`
	StdoutPath        string                 `json:"stdout_path"`
	StderrPath        string                 `json:"stderr_path"`
	OutputLastMessage string                 `json:"output_last_message"`
	StartedAt         time.Time              `json:"started_at"`
	FinishedAt        time.Time              `json:"finished_at,omitempty"`
	ExitCode          int                    `json:"exit_code"`
}

type CommandProviderResult struct {
	InvocationPath string
	StdoutPath     string
	StderrPath     string
	FinalPath      string
	Command        []string
	ExitCode       int
}

func BuildCommandProviderCommand(provider ProviderDefinition) []string {
	return append([]string(nil), provider.Command...)
}

func RunCommandProvider(ctx context.Context, opts CommandProviderOptions, stdout, stderr io.Writer) (*CommandProviderResult, error) {
	if err := ValidateProviderDefinition(opts.Provider); err != nil {
		return nil, err
	}
	opts.Provider.Name = NormalizeProviderName(opts.Provider.Name)
	opts.Provider.Kind = NormalizeProviderName(opts.Provider.Kind)
	if opts.Provider.Kind != "command" {
		return nil, fmt.Errorf("provider %q is kind %q, want command", opts.Provider.Name, opts.Provider.Kind)
	}
	if strings.TrimSpace(opts.ProjectDir) == "" {
		return nil, fmt.Errorf("project directory is required")
	}
	if strings.TrimSpace(opts.RunDir) == "" {
		return nil, fmt.Errorf("run directory is required")
	}
	if strings.TrimSpace(opts.PromptPath) == "" {
		return nil, fmt.Errorf("prompt path is required")
	}

	prompt, err := os.ReadFile(opts.PromptPath)
	if err != nil {
		return nil, err
	}

	agentsDir := filepath.Join(opts.RunDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return nil, err
	}

	stamp := time.Now().UTC().Format("20060102-150405")
	providerName := NormalizeProviderName(opts.Provider.Name)
	scratchDir := filepath.Join(opts.RunDir, "tmp", "agents", providerName+"-"+stamp)
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return nil, err
	}
	stdoutPath := filepath.Join(agentsDir, providerName+"-"+stamp+"-stdout.log")
	stderrPath := filepath.Join(agentsDir, providerName+"-"+stamp+"-stderr.log")
	finalPath := opts.OutputLastMessage
	if finalPath == "" {
		finalPath = filepath.Join(agentsDir, providerName+"-"+stamp+"-final.md")
	}

	command := BuildCommandProviderCommand(opts.Provider)
	invocation := CommandProviderInvocation{
		ProviderName:      providerName,
		ProviderKind:      opts.Provider.Kind,
		Model:             opts.Model,
		EnvironmentPolicy: AgentEnvironmentPolicy{},
		Command:           command,
		ProjectDir:        opts.ProjectDir,
		RunDir:            opts.RunDir,
		PromptPath:        opts.PromptPath,
		ScratchDir:        scratchDir,
		StdoutPath:        stdoutPath,
		StderrPath:        stderrPath,
		OutputLastMessage: finalPath,
		StartedAt:         time.Now().UTC(),
		ExitCode:          -1,
	}

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return nil, err
	}
	defer stderrFile.Close()

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = opts.ProjectDir
	cmd.Stdin = strings.NewReader(string(prompt))
	cmd.Env = append(os.Environ(),
		"TMPDIR="+scratchDir,
		"CRUCIBLE_PROVIDER_NAME="+providerName,
		"CRUCIBLE_PROVIDER_KIND="+opts.Provider.Kind,
		"CRUCIBLE_PROJECT_DIR="+opts.ProjectDir,
		"CRUCIBLE_RUN_DIR="+opts.RunDir,
		"CRUCIBLE_PROMPT_PATH="+opts.PromptPath,
		"CRUCIBLE_OUTPUT_LAST_MESSAGE="+finalPath,
	)
	if strings.TrimSpace(opts.Model) != "" {
		cmd.Env = append(cmd.Env, "CRUCIBLE_MODEL="+opts.Model)
	}
	cmd.Stdout = io.MultiWriter(stdoutFile, stdout)
	cmd.Stderr = io.MultiWriter(stderrFile, stderr)

	err = cmd.Run()
	invocation.FinishedAt = time.Now().UTC()
	if cmd.ProcessState != nil {
		invocation.ExitCode = cmd.ProcessState.ExitCode()
	}
	if syncErr := stdoutFile.Sync(); syncErr != nil && err == nil {
		err = syncErr
	}

	if copyErr := copyStdoutToFinalIfMissing(stdoutPath, finalPath); copyErr != nil && err == nil {
		err = copyErr
	}

	invocationPath := filepath.Join(agentsDir, providerName+"-"+stamp+"-invocation.json")
	if saveErr := saveCommandInvocation(invocationPath, invocation); saveErr != nil && err == nil {
		err = saveErr
	}

	result := &CommandProviderResult{
		InvocationPath: invocationPath,
		StdoutPath:     stdoutPath,
		StderrPath:     stderrPath,
		FinalPath:      finalPath,
		Command:        command,
		ExitCode:       invocation.ExitCode,
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func saveCommandInvocation(path string, value CommandProviderInvocation) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func copyStdoutToFinalIfMissing(stdoutPath, finalPath string) error {
	if _, err := os.Stat(finalPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(stdoutPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(finalPath, data, 0o644)
}
