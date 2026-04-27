package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultCodexBinary    = "codex"
	DefaultCodexSandbox   = "workspace-write"
	DefaultApprovalPolicy = "never"
)

type CodexOptions struct {
	Binary            string
	ProjectDir        string
	RunDir            string
	PromptPath        string
	Model             string
	Profile           string
	Sandbox           string
	ApprovalPolicy    string
	OutputLastMessage string
	JSONEvents        bool
	SkipGitRepoCheck  bool
}

type CodexInvocation struct {
	Command           []string  `json:"command"`
	ProjectDir        string    `json:"project_dir"`
	RunDir            string    `json:"run_dir"`
	PromptPath        string    `json:"prompt_path"`
	StdoutPath        string    `json:"stdout_path"`
	StderrPath        string    `json:"stderr_path"`
	OutputLastMessage string    `json:"output_last_message"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at,omitempty"`
	ExitCode          int       `json:"exit_code"`
}

type CodexResult struct {
	InvocationPath string
	StdoutPath     string
	StderrPath     string
	FinalPath      string
	Command        []string
	ExitCode       int
}

func BuildCodexExecCommand(opts CodexOptions) []string {
	binary := opts.Binary
	if strings.TrimSpace(binary) == "" {
		binary = DefaultCodexBinary
	}
	sandbox := opts.Sandbox
	if strings.TrimSpace(sandbox) == "" {
		sandbox = DefaultCodexSandbox
	}
	approval := opts.ApprovalPolicy
	if strings.TrimSpace(approval) == "" {
		approval = DefaultApprovalPolicy
	}

	args := []string{
		binary,
		"exec",
		"--cd", opts.ProjectDir,
		"--sandbox", sandbox,
		"--ask-for-approval", approval,
	}
	if opts.SkipGitRepoCheck {
		args = append(args, "--skip-git-repo-check")
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Profile != "" {
		args = append(args, "--profile", opts.Profile)
	}
	if opts.JSONEvents {
		args = append(args, "--json")
	}
	if opts.OutputLastMessage != "" {
		args = append(args, "--output-last-message", opts.OutputLastMessage)
	}
	return append(args, "-")
}

func FormatCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '/' || r == '.' || r == '=' || r == ':' || r == ',' || r == '+' || r == '@' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
	}) == -1 {
		return value
	}
	return strconv.Quote(value)
}

func RunCodex(ctx context.Context, opts CodexOptions, stdout, stderr io.Writer) (*CodexResult, error) {
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
	stdoutPath := filepath.Join(agentsDir, "codex-"+stamp+"-stdout.log")
	if opts.JSONEvents {
		stdoutPath = filepath.Join(agentsDir, "codex-"+stamp+"-events.jsonl")
	}
	stderrPath := filepath.Join(agentsDir, "codex-"+stamp+"-stderr.log")
	finalPath := opts.OutputLastMessage
	if finalPath == "" {
		finalPath = filepath.Join(agentsDir, "codex-"+stamp+"-final.md")
	}
	opts.OutputLastMessage = finalPath

	command := BuildCodexExecCommand(opts)
	invocation := CodexInvocation{
		Command:           command,
		ProjectDir:        opts.ProjectDir,
		RunDir:            opts.RunDir,
		PromptPath:        opts.PromptPath,
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
	cmd.Stdout = io.MultiWriter(stdoutFile, stdout)
	cmd.Stderr = io.MultiWriter(stderrFile, stderr)

	err = cmd.Run()
	invocation.FinishedAt = time.Now().UTC()
	if cmd.ProcessState != nil {
		invocation.ExitCode = cmd.ProcessState.ExitCode()
	}

	invocationPath := filepath.Join(agentsDir, "codex-"+stamp+"-invocation.json")
	if saveErr := saveInvocation(invocationPath, invocation); saveErr != nil && err == nil {
		err = saveErr
	}

	result := &CodexResult{
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

func saveInvocation(path string, value CodexInvocation) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
