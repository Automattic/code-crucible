package cli

import (
	"io"
	"strconv"
	"strings"
)

type WorkflowController struct {
	ProjectDir string
	Stdout     io.Writer
	Stderr     io.Writer
}

type RunWorkflowOptions struct {
	Optimize   string
	SourcePath string
	Agent      string
	Variants   int
	Generate   bool
	ExtraArgs  []string
}

type GenerateWorkflowOptions struct {
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
}

type NextRoundWorkflowOptions struct {
	RunSelector string
	Parents     int
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
	if strings.TrimSpace(opts.Agent) != "" {
		args = append(args, "--agent", strings.TrimSpace(opts.Agent))
	}
	if opts.Variants > 0 {
		args = append(args, "--variants", strconv.Itoa(opts.Variants))
	}
	if opts.Generate {
		args = append(args, "--generate")
	}
	args = append(args, opts.ExtraArgs...)
	args = append(args, opts.Optimize)
	return runTournament(args, c.Stdout, c.Stderr)
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
	return runGenerate(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) Evaluate(opts EvaluateWorkflowOptions) int {
	args := []string{"--project-dir", c.ProjectDir, "--run", opts.RunSelector}
	args = append(args, opts.ExtraArgs...)
	return runEvaluate(args, c.Stdout, c.Stderr)
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
	return runAdopt(args, c.Stdout, c.Stderr)
}

func (c WorkflowController) NextRound(opts NextRoundWorkflowOptions) int {
	return runNextRound([]string{
		"--project-dir", c.ProjectDir,
		"--run", opts.RunSelector,
		"--parents", strconv.Itoa(opts.Parents),
	}, c.Stdout, c.Stderr)
}

func (c WorkflowController) Discover(opts DiscoverWorkflowOptions) int {
	return runDiscover([]string{"--project-dir", c.ProjectDir, "--agent", opts.Agent, opts.Request}, c.Stdout, c.Stderr)
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
