package cli

import (
	"context"
	"io"
	"strconv"
	"strings"
)

type WorkflowController struct {
	Context    context.Context
	ProjectDir string
	Stdout     io.Writer
	Stderr     io.Writer
}

type RunWorkflowOptions struct {
	Optimize   string
	Auto       bool
	SourcePath string
	Agent      string
	Variants   int
	Generate   bool
	Evaluate   bool
	ExtraArgs  []string
}

type GenerateWorkflowOptions struct {
	RunSelector string
	Agent       string
	ExtraArgs   []string
}

type EvaluatorGenerateWorkflowOptions struct {
	RunSelector string
	Agent       string
	ExtraArgs   []string
}

type EvaluateWorkflowOptions struct {
	RunSelector string
	ExtraArgs   []string
}

type EvolveWorkflowOptions struct {
	RunSelector string
	Rounds      int
	Parents     int
	Agent       string
	ExtraArgs   []string
}

type ReportWorkflowOptions struct {
	RunSelector string
	OutputPath  string
	JSON        bool
}

type InspectWorkflowOptions struct {
	RunSelector string
	CandidateID string
}

type IndexWorkflowOptions struct {
	RunSelector string
	ScopedRun   bool
	JSON        bool
}

type AdoptWorkflowOptions struct {
	RunSelector string
	Model       string
	JSON        bool
}

type PromoteWorkflowOptions struct {
	RunSelector   string
	CandidateID   string
	DryRun        bool
	AllowUnpassed bool
	JSON          bool
}

type NextRoundWorkflowOptions struct {
	RunSelector string
	Parents     int
	JSON        bool
}

type DiscoverWorkflowOptions struct {
	Agent   string
	Request string
}

type QueryWorkflowOptions struct {
	Kind        string
	Limit       int
	RunSelector string
	Status      string
}

func (c WorkflowController) Run(opts RunWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir}
	if strings.TrimSpace(opts.SourcePath) != "" {
		args = append(args, "--source-path", strings.TrimSpace(opts.SourcePath))
	}
	if opts.Auto {
		args = append(args, "--auto")
	}
	if strings.TrimSpace(opts.Agent) != "" {
		args = append(args, "--agent", strings.TrimSpace(opts.Agent))
	}
	if opts.Variants > 0 {
		args = append(args, "--variants", strconv.Itoa(opts.Variants))
	}
	if opts.Generate {
		args = append(args, "--generate")
	}
	if opts.Evaluate {
		args = append(args, "--evaluate")
	}
	args = append(args, opts.ExtraArgs...)
	args = append(args, opts.Optimize)
	return runTournamentWithContext(c.context(), args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Leaderboard(runSelector string) int {
	return runLeaderboard([]string{"--project-dir", c.ProjectDir, "--run", runSelector}, c.Stdout, c.Stderr)
}

func (c WorkflowController) Generate(opts GenerateWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir, "--run", opts.RunSelector}
	if strings.TrimSpace(opts.Agent) != "" {
		args = append(args, "--agent", strings.TrimSpace(opts.Agent))
	}
	args = append(args, opts.ExtraArgs...)
	return runGenerateWithContext(c.context(), args, c.Stdout, c.Stderr)
}

func (c WorkflowController) EvaluatorGenerate(opts EvaluatorGenerateWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir, "--run", opts.RunSelector}
	if strings.TrimSpace(opts.Agent) != "" {
		args = append(args, "--agent", strings.TrimSpace(opts.Agent))
	}
	args = append(args, opts.ExtraArgs...)
	return runEvaluatorGenerateWithContext(c.context(), args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Evaluate(opts EvaluateWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir, "--run", opts.RunSelector}
	args = append(args, opts.ExtraArgs...)
	return runEvaluateWithContext(c.context(), args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Evolve(opts EvolveWorkflowOptions) int {
	args := []string{
		"--project-dir", c.ProjectDir,
		"--run", opts.RunSelector,
		"--rounds", strconv.Itoa(opts.Rounds),
		"--parents", strconv.Itoa(opts.Parents),
	}
	if strings.TrimSpace(opts.Agent) != "" {
		args = append(args, "--agent", strings.TrimSpace(opts.Agent))
	}
	args = append(args, opts.ExtraArgs...)
	return runEvolve(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Report(opts ReportWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir, "--run", opts.RunSelector}
	if strings.TrimSpace(opts.OutputPath) != "" {
		args = append(args, "--output", strings.TrimSpace(opts.OutputPath))
	}
	if opts.JSON {
		args = append(args, "--json")
	}
	return runReport(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Inspect(opts InspectWorkflowOptions) int {
	return runInspect([]string{"--project-dir", c.ProjectDir, "--run", opts.RunSelector, opts.CandidateID}, c.Stdout, c.Stderr)
}

func (c WorkflowController) Index(opts IndexWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir}
	if opts.ScopedRun {
		args = append(args, "--run", opts.RunSelector)
	}
	if opts.JSON {
		args = append(args, "--json")
	}
	return runIndex(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Adopt(opts AdoptWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir, "--run", opts.RunSelector}
	if strings.TrimSpace(opts.Model) != "" {
		args = append(args, "--model", strings.TrimSpace(opts.Model))
	}
	if opts.JSON {
		args = append(args, "--json")
	}
	return runAdopt(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Promote(opts PromoteWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir, "--run", opts.RunSelector}
	if strings.TrimSpace(opts.CandidateID) != "" {
		args = append(args, "--candidate", strings.TrimSpace(opts.CandidateID))
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	if opts.AllowUnpassed {
		args = append(args, "--allow-unpassed")
	}
	if opts.JSON {
		args = append(args, "--json")
	}
	return runPromote(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) NextRound(opts NextRoundWorkflowOptions) int {
	args := []string{
		"--project-dir", c.ProjectDir,
		"--run", opts.RunSelector,
		"--parents", strconv.Itoa(opts.Parents),
	}
	if opts.JSON {
		args = append(args, "--json")
	}
	return runNextRound(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Discover(opts DiscoverWorkflowOptions) int {
	return runDiscoverWithContext(c.context(), []string{"--project-dir", c.ProjectDir, "--agent", opts.Agent, opts.Request}, c.Stdout, c.Stderr)
}

func (c WorkflowController) Query(opts QueryWorkflowOptions) int {
	kind := strings.TrimSpace(opts.Kind)
	if kind == "" {
		kind = "runs"
	}
	args := []string{kind, "--project-dir", c.ProjectDir, "--limit", strconv.Itoa(opts.Limit)}
	if kind == "candidates" {
		args = append(args, "--run", opts.RunSelector)
		if strings.TrimSpace(opts.Status) != "" {
			args = append(args, "--status", strings.TrimSpace(opts.Status))
		}
	}
	return runQuery(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) context() context.Context {
	if c.Context != nil {
		return c.Context
	}
	return context.Background()
}
