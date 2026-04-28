package run

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/discovery"
	"github.com/Automattic/code-crucible/internal/evaluator"
)

const defaultSemanticMaxOutputBytes = 16 * 1024

func semanticContractErrors(runDir, baselineSrc, candidateSrc string, baselineExts, candidateExts map[string]bool) []string {
	if !baselineExts[".go"] || !candidateExts[".go"] {
		return nil
	}

	baselineSigs, err := goCallableSignatures(baselineSrc)
	if err != nil {
		return []string{"candidate contract failed: baseline Go source could not be parsed: " + err.Error()}
	}
	candidateSigs, err := goCallableSignatures(candidateSrc)
	if err != nil {
		return []string{"candidate contract failed: candidate Go source could not be parsed: " + err.Error()}
	}

	var errors []string
	checked := map[string]bool{}
	if plan, err := discovery.LoadAgentPlan(filepath.Join(runDir, "docs", "agent-discovery.json")); err == nil {
		functionName := dropInFunctionName(plan.DropInInterface)
		if functionName != "" {
			if key, baselineSig, ok := findGoCallableSignature(baselineSigs, functionName); ok {
				checked[key] = true
				if candidateSig, ok := candidateSigs[key]; !ok {
					errors = append(errors, fmt.Sprintf("candidate contract failed: Go drop-in callable %s is missing", key))
				} else if candidateSig.Signature != baselineSig.Signature {
					errors = append(errors, fmt.Sprintf("candidate contract failed: Go drop-in callable %s signature changed: got %s, want %s", key, candidateSig.Signature, baselineSig.Signature))
				}
			}
		}
	}

	for key, baselineSig := range baselineSigs {
		if checked[key] || !baselineSig.Exported {
			continue
		}
		if candidateSig, ok := candidateSigs[key]; !ok {
			errors = append(errors, fmt.Sprintf("candidate contract failed: exported Go callable %s is missing", key))
		} else if candidateSig.Signature != baselineSig.Signature {
			errors = append(errors, fmt.Sprintf("candidate contract failed: exported Go callable %s signature changed: got %s, want %s", key, candidateSig.Signature, baselineSig.Signature))
		}
	}
	sort.Strings(errors)
	return errors
}

type semanticContractReport struct {
	Version    int                           `json:"version"`
	ConfigPath string                        `json:"config_path"`
	RunDir     string                        `json:"run_dir"`
	Candidate  string                        `json:"candidate_dir"`
	Baseline   string                        `json:"baseline_src"`
	CreatedAt  time.Time                     `json:"created_at"`
	Checks     []semanticContractCheckResult `json:"checks"`
	Errors     []string                      `json:"errors,omitempty"`
}

type semanticContractCheckResult struct {
	Name        string                `json:"name"`
	Command     string                `json:"command"`
	Passed      bool                  `json:"passed"`
	Comparisons []string              `json:"comparisons"`
	Baseline    semanticCommandResult `json:"baseline"`
	Candidate   semanticCommandResult `json:"candidate"`
	Errors      []string              `json:"errors,omitempty"`
}

type semanticCommandResult struct {
	ExitCode        int     `json:"exit_code"`
	Stdout          string  `json:"stdout,omitempty"`
	StdoutTruncated bool    `json:"stdout_truncated,omitempty"`
	Stderr          string  `json:"stderr,omitempty"`
	StderrTruncated bool    `json:"stderr_truncated,omitempty"`
	DurationMS      float64 `json:"duration_ms"`
	TimedOut        bool    `json:"timed_out,omitempty"`
	ExecError       string  `json:"exec_error,omitempty"`
}

func runSemanticContractChecks(runDir, baselineSrc, candidateSrc, candidateDir, resultsPath string, opts evaluatorExecutionOptions) ([]string, string) {
	configPath := filepath.Join(runDir, "evaluator", evaluator.SemanticChecksFilename)
	checks, err := evaluator.LoadSemanticChecks(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ""
		}
		return []string{"candidate contract failed: semantic checks could not be loaded: " + err.Error()}, ""
	}
	if checks == nil || len(checks.Checks) == 0 {
		return nil, ""
	}
	if sandboxEnabled(opts.Sandbox) {
		sandbox, err := NormalizeSandboxOptions(opts.Sandbox)
		if err != nil {
			return []string{"candidate contract failed: semantic checks sandbox is invalid: " + err.Error()}, ""
		}
		opts.Sandbox = sandbox
	}

	report := semanticContractReport{
		Version:    1,
		ConfigPath: filepath.ToSlash(configPath),
		RunDir:     filepath.ToSlash(runDir),
		Candidate:  filepath.ToSlash(candidateDir),
		Baseline:   filepath.ToSlash(baselineSrc),
		CreatedAt:  time.Now().UTC(),
		Checks:     make([]semanticContractCheckResult, 0, len(checks.Checks)),
	}
	var errors []string
	for i, check := range checks.Checks {
		result := runSemanticContractCheck(i, check, runDir, baselineSrc, candidateSrc, candidateDir, opts)
		if !result.Passed {
			for _, err := range result.Errors {
				errors = append(errors, fmt.Sprintf("candidate contract failed: semantic check %q: %s", result.Name, err))
			}
		}
		report.Checks = append(report.Checks, result)
	}
	report.Errors = errors
	if err := archive.SaveJSON(resultsPath, report); err != nil {
		errors = append(errors, "candidate contract failed: save semantic check results: "+err.Error())
	}
	return errors, resultsPath
}

func runSemanticContractCheck(index int, check evaluator.SemanticCheck, runDir, baselineSrc, candidateSrc, candidateDir string, opts evaluatorExecutionOptions) semanticContractCheckResult {
	name := strings.TrimSpace(check.Name)
	if name == "" {
		name = fmt.Sprintf("semantic-%04d", index+1)
	}
	maxOutput := check.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = defaultSemanticMaxOutputBytes
	}
	timeout := opts.Timeout
	if check.TimeoutMS > 0 {
		timeout = time.Duration(check.TimeoutMS) * time.Millisecond
	}
	command := strings.TrimSpace(check.Command)
	result := semanticContractCheckResult{
		Name:        name,
		Command:     command,
		Comparisons: semanticComparisons(check),
	}
	if command == "" {
		result.Errors = []string{"command is required"}
		return result
	}

	result.Baseline = runSemanticCommand(command, "baseline", baselineSrc, runDir, candidateDir, candidateSrc, maxOutput, timeout, opts)
	result.Candidate = runSemanticCommand(command, "candidate", candidateSrc, runDir, candidateDir, baselineSrc, maxOutput, timeout, opts)
	result.Errors = semanticComparisonErrors(check, result.Baseline, result.Candidate)
	result.Passed = len(result.Errors) == 0
	return result
}

func semanticComparisons(check evaluator.SemanticCheck) []string {
	var comparisons []string
	if semanticBool(check.CompareExitCode, true) {
		comparisons = append(comparisons, "exit_code")
	}
	if semanticBool(check.CompareStdout, true) {
		comparisons = append(comparisons, "stdout")
	}
	if semanticBool(check.CompareStderr, false) {
		comparisons = append(comparisons, "stderr")
	}
	return comparisons
}

func semanticComparisonErrors(check evaluator.SemanticCheck, baseline, candidate semanticCommandResult) []string {
	var errors []string
	if baseline.ExecError != "" {
		errors = append(errors, "baseline command could not execute: "+baseline.ExecError)
	}
	if candidate.ExecError != "" {
		errors = append(errors, "candidate command could not execute: "+candidate.ExecError)
	}
	if baseline.TimedOut {
		errors = append(errors, "baseline command timed out")
	}
	if candidate.TimedOut {
		errors = append(errors, "candidate command timed out")
	}
	if len(errors) > 0 {
		return errors
	}
	if semanticBool(check.CompareExitCode, true) && baseline.ExitCode != candidate.ExitCode {
		errors = append(errors, fmt.Sprintf("exit code changed: got %d, want %d", candidate.ExitCode, baseline.ExitCode))
	}
	if semanticBool(check.CompareStdout, true) && baseline.Stdout != candidate.Stdout {
		errors = append(errors, "stdout changed")
	}
	if semanticBool(check.CompareStderr, false) && baseline.Stderr != candidate.Stderr {
		errors = append(errors, "stderr changed")
	}
	return errors
}

func semanticBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func runSemanticCommand(command, role, workDir, runDir, candidateDir, otherSrc string, maxOutput int, timeout time.Duration, opts evaluatorExecutionOptions) semanticCommandResult {
	stdout := &cappedBuffer{limit: maxOutput}
	stderr := &cappedBuffer{limit: maxOutput}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	semanticEnv := semanticCommandEnv(role, runDir, candidateDir, workDir, otherSrc, opts.ProjectDir)
	name, args := buildSemanticCommand(command, workDir, runDir, semanticEnv, opts)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.Env = append(cmd.Env, semanticEnv...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	start := time.Now()
	err := cmd.Run()

	result := semanticCommandResult{
		ExitCode:        0,
		Stdout:          stdout.String(),
		StdoutTruncated: stdout.truncated,
		Stderr:          stderr.String(),
		StderrTruncated: stderr.truncated,
		DurationMS:      float64(time.Since(start).Microseconds()) / 1000,
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.ExitCode = -1
		return result
	}
	if ctx.Err() == context.Canceled {
		result.ExitCode = -1
		if strings.TrimSpace(result.Stderr) != "" {
			result.Stderr += "\n"
		}
		result.Stderr += "semantic contract check canceled"
		return result
	}
	if err == nil {
		return result
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result
	}
	result.ExitCode = -1
	result.ExecError = err.Error()
	return result
}

func buildSemanticCommand(command, workDir, runDir string, semanticEnv []string, opts evaluatorExecutionOptions) (string, []string) {
	if sandboxEnabled(opts.Sandbox) {
		return buildSemanticContainerCommand(command, workDir, runDir, semanticEnv, opts)
	}
	name := "bash"
	args := []string{"-lc", command}
	if opts.CPULimit > 0 {
		name, args = wrapTasksetCommand(name, args, opts.CPULimit)
	}
	if opts.Nice > 0 {
		args = append([]string{"-n", strconv.Itoa(opts.Nice), name}, args...)
		name = "nice"
	}
	return name, args
}

func buildSemanticContainerCommand(command, workDir, runDir string, semanticEnv []string, opts evaluatorExecutionOptions) (string, []string) {
	args := []string{
		"run",
		"--rm",
		"--network", opts.Sandbox.Network,
		"--volume", sandboxMount(runDir, "rw"),
	}
	if opts.ProjectDir != "" && filepath.Clean(opts.ProjectDir) != filepath.Clean(runDir) {
		args = append(args, "--volume", sandboxMount(opts.ProjectDir, "ro"))
	}
	if opts.CPULimit > 0 {
		args = append(args, "--cpus", strconv.Itoa(opts.CPULimit))
	}
	if opts.Sandbox.MemoryLimit != "" {
		args = append(args, "--memory", opts.Sandbox.MemoryLimit)
	}
	if opts.Sandbox.PIDsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(opts.Sandbox.PIDsLimit))
	}
	if opts.Sandbox.Engine == "podman" {
		args = append(args, "--userns", "keep-id")
	} else if uid := os.Getuid(); uid >= 0 {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, os.Getgid()))
	}
	args = append(args,
		"--workdir", workDir,
		"--env", "HOME="+sandboxHomeDir(runDir),
	)
	for _, env := range opts.Env {
		args = append(args, "--env", env)
	}
	for _, env := range semanticEnv {
		args = append(args, "--env", env)
	}
	args = append(args, opts.Sandbox.Image, "bash", "-lc", command)
	return opts.Sandbox.Engine, args
}

func semanticCommandEnv(role, runDir, candidateDir, currentSrc, otherSrc, projectDir string) []string {
	baselineSrc := currentSrc
	candidateSrc := otherSrc
	if role == "candidate" {
		baselineSrc = otherSrc
		candidateSrc = currentSrc
	}
	env := []string{
		"CRUCIBLE_SEMANTIC_ROLE=" + role,
		"CRUCIBLE_RUN_DIR=" + runDir,
		"CRUCIBLE_CANDIDATE_DIR=" + candidateDir,
		"CRUCIBLE_BASELINE_SRC=" + baselineSrc,
		"CRUCIBLE_CANDIDATE_SRC=" + candidateSrc,
	}
	if strings.TrimSpace(projectDir) != "" {
		env = append(env, "CRUCIBLE_PROJECT_DIR="+projectDir)
	}
	return env
}

type cappedBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

type goSignature struct {
	Signature string
	Exported  bool
}

func goCallableSignatures(root string) (map[string]goSignature, error) {
	signatures := map[string]goSignature{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "target", "dist", "build":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			signature, err := goFuncTypeSignature(fset, fn.Type)
			if err != nil {
				return err
			}
			if fn.Recv == nil {
				signatures[fn.Name.Name] = goSignature{
					Signature: signature,
					Exported:  ast.IsExported(fn.Name.Name),
				}
				continue
			}
			receiverType, receiverBase, err := goReceiverType(fset, fn.Recv)
			if err != nil {
				return err
			}
			if receiverBase == "" {
				continue
			}
			signatures[receiverBase+"."+fn.Name.Name] = goSignature{
				Signature: "method " + receiverType + " " + signature,
				Exported:  ast.IsExported(receiverBase) && ast.IsExported(fn.Name.Name),
			}
		}
		return nil
	})
	return signatures, err
}

func findGoCallableSignature(signatures map[string]goSignature, name string) (string, goSignature, bool) {
	if signature, ok := signatures[name]; ok {
		return name, signature, true
	}
	var matches []string
	for key := range signatures {
		if strings.HasSuffix(key, "."+name) {
			matches = append(matches, key)
		}
	}
	if len(matches) != 1 {
		return "", goSignature{}, false
	}
	return matches[0], signatures[matches[0]], true
}

func goFuncTypeSignature(fset *token.FileSet, fn *ast.FuncType) (string, error) {
	params, err := goFieldTypeList(fset, fn.Params)
	if err != nil {
		return "", err
	}
	results, err := goFieldTypeList(fset, fn.Results)
	if err != nil {
		return "", err
	}
	signature := "func(" + strings.Join(params, ", ") + ")"
	if len(results) == 1 {
		signature += " " + results[0]
	} else if len(results) > 1 {
		signature += " (" + strings.Join(results, ", ") + ")"
	}
	return signature, nil
}

func goReceiverType(fset *token.FileSet, fields *ast.FieldList) (string, string, error) {
	if fields == nil || len(fields.List) == 0 {
		return "", "", nil
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, fields.List[0].Type); err != nil {
		return "", "", err
	}
	receiverType := buf.String()
	return receiverType, receiverBaseName(receiverType), nil
}

func receiverBaseName(receiverType string) string {
	receiverType = strings.TrimSpace(strings.TrimPrefix(receiverType, "*"))
	if idx := strings.LastIndex(receiverType, "."); idx >= 0 {
		receiverType = receiverType[idx+1:]
	}
	if idx := strings.Index(receiverType, "["); idx >= 0 {
		receiverType = receiverType[:idx]
	}
	return trimIdentifier(receiverType)
}

func goFieldTypeList(fset *token.FileSet, fields *ast.FieldList) ([]string, error) {
	if fields == nil {
		return nil, nil
	}
	out := make([]string, 0, len(fields.List))
	for _, field := range fields.List {
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, field.Type); err != nil {
			return nil, err
		}
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			out = append(out, buf.String())
		}
	}
	return out, nil
}

func dropInFunctionName(dropInInterface string) string {
	text := strings.TrimSpace(dropInInterface)
	text = strings.TrimPrefix(text, "func ")
	if text == "" {
		return ""
	}
	if idx := strings.Index(text, "("); idx >= 0 {
		text = text[:idx]
	} else if fields := strings.Fields(text); len(fields) > 0 {
		text = fields[0]
	}
	text = strings.TrimSpace(text)
	if idx := strings.LastIndex(text, "."); idx >= 0 {
		text = text[idx+1:]
	}
	return trimIdentifier(text)
}

func trimIdentifier(value string) string {
	value = strings.TrimSpace(value)
	start := -1
	end := -1
	for i, r := range value {
		if start == -1 {
			if r == '_' || unicode.IsLetter(r) {
				start = i
				end = i + len(string(r))
			}
			continue
		}
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			end = i + len(string(r))
			continue
		}
		break
	}
	if start == -1 {
		return ""
	}
	return value[start:end]
}
