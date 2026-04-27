package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gaarai/code-crucible/internal/agent"
	"github.com/gaarai/code-crucible/internal/archive"
	"github.com/gaarai/code-crucible/internal/discovery"
	"github.com/gaarai/code-crucible/internal/evaluator"
	"github.com/gaarai/code-crucible/internal/model"
	"github.com/gaarai/code-crucible/internal/project"
)

type Options struct {
	ProjectDir      string
	Optimize        string
	TargetPath      string
	Agent           string
	Variants        int
	Rounds          int
	Exploration     float64
	Evaluator       string
	EvaluatorScript string
	ExternalMode    string
	Fixtures        string
	AllowHosts      []string
}

type CreatedRun struct {
	ID                string
	RunDir            string
	InterfaceDocPath  string
	PromptPath        string
	BaselineSourceDir string
}

func Create(opts Options) (*CreatedRun, error) {
	if strings.TrimSpace(opts.ProjectDir) == "" {
		opts.ProjectDir = "."
	}
	absProject, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	cfg, err := project.Ensure(absProject)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(opts.Optimize) == "" {
		return nil, fmt.Errorf("--optimize is required")
	}
	if opts.Variants <= 0 {
		opts.Variants = 3
	}
	if opts.Rounds <= 0 {
		opts.Rounds = 1
	}
	if opts.Agent == "" {
		opts.Agent = cfg.DefaultAgent
	}
	if opts.Exploration < 0 {
		opts.Exploration = 0
	}
	if opts.Exploration > 1 {
		opts.Exploration = 1
	}

	mode := model.ExternalMode(opts.ExternalMode)
	if mode == "" {
		mode = cfg.DefaultExternalPolicy.Mode
	}
	if !mode.Valid() {
		return nil, fmt.Errorf("invalid external mode %q", opts.ExternalMode)
	}

	now := time.Now().UTC()
	runID := fmt.Sprintf("%s-%s", now.Format("20060102-150405"), archive.Slug(opts.Optimize, 48))
	runDir := filepath.Join(project.WorkDir(absProject), "runs", runID)
	roundDir := filepath.Join(runDir, "round-0001")
	baselineDir := filepath.Join(roundDir, "candidate-0000-baseline")
	baselineSrc := filepath.Join(baselineDir, "src")

	for _, dir := range []string{
		runDir,
		roundDir,
		baselineSrc,
		filepath.Join(runDir, "docs"),
		filepath.Join(runDir, "agents"),
		filepath.Join(runDir, "evaluator"),
		filepath.Join(runDir, "external"),
		filepath.Join(runDir, "prompts"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	discovered, err := discovery.Analyze(absProject, opts.Optimize, opts.TargetPath)
	if err != nil {
		return nil, err
	}

	if err := writeBaseline(absProject, opts.TargetPath, baselineSrc); err != nil {
		return nil, err
	}

	interfacePath := filepath.Join(runDir, "docs", "interfaces.md")
	if err := os.WriteFile(interfacePath, []byte(discovered.InterfaceMarkdown()), 0o644); err != nil {
		return nil, err
	}

	external := model.ExternalPolicy{
		Mode:      mode,
		Allowlist: opts.AllowHosts,
		Fixtures:  opts.Fixtures,
	}
	promptPath := filepath.Join(runDir, "prompts", "generation-round-0001.md")
	runConfig := model.RunConfig{
		ID:              runID,
		ProjectDir:      absProject,
		RunDir:          runDir,
		RoundDir:        roundDir,
		Optimize:        opts.Optimize,
		TargetPath:      opts.TargetPath,
		Agent:           opts.Agent,
		Variants:        opts.Variants,
		Rounds:          opts.Rounds,
		Exploration:     opts.Exploration,
		Evaluator:       opts.Evaluator,
		EvaluatorScript: opts.EvaluatorScript,
		External:        external,
		CreatedAt:       now,
		InterfaceDocs:   filepath.ToSlash(interfacePath),
		PromptPath:      filepath.ToSlash(promptPath),
	}

	if err := archive.SaveJSON(filepath.Join(runDir, "run.json"), runConfig); err != nil {
		return nil, err
	}
	if err := archive.SaveJSON(filepath.Join(runDir, "external", "policy.json"), external); err != nil {
		return nil, err
	}

	baseline := model.Candidate{
		ID:         "candidate-0000-baseline",
		Name:       "baseline",
		Round:      1,
		Agent:      "source",
		SourcePath: filepath.ToSlash(baselineSrc),
		Baseline:   true,
		CreatedAt:  now,
	}
	if err := archive.SaveJSON(filepath.Join(baselineDir, "candidate.json"), baseline); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(baselineDir, "design.md"), []byte(baselineDesign(opts)), 0o644); err != nil {
		return nil, err
	}

	board := model.Leaderboard{
		RunID:     runID,
		Optimize:  opts.Optimize,
		CreatedAt: now,
		Results: []model.CandidateResult{
			{
				Candidate: baseline,
				External: model.ExternalCallTrace{
					Mode:         mode,
					PolicyPassed: false,
				},
				Verdict: model.Verdict{
					CorrectnessPassed:    false,
					BenchmarkPassed:      false,
					ExternalPolicyPassed: false,
					Notes: []string{
						"Baseline has been extracted but not evaluated yet.",
					},
				},
				Status: "pending",
			},
		},
	}
	if err := archive.SaveJSON(filepath.Join(runDir, "leaderboard.json"), board); err != nil {
		return nil, err
	}

	evaluatorPath := filepath.Join(runDir, "evaluator", "evaluator.sh")
	if opts.EvaluatorScript != "" {
		source := opts.EvaluatorScript
		if !filepath.IsAbs(source) {
			source = filepath.Join(absProject, source)
		}
		if err := archive.CopyPath(source, evaluatorPath); err != nil {
			return nil, fmt.Errorf("copy evaluator script: %w", err)
		}
		if err := os.Chmod(evaluatorPath, 0o755); err != nil {
			return nil, err
		}
	} else {
		evaluatorScript := evaluator.DefaultScript(opts.Evaluator)
		if err := os.WriteFile(evaluatorPath, []byte(evaluatorScript), 0o755); err != nil {
			return nil, err
		}
	}

	prompt := agent.BuildGenerationPrompt(agent.GenerationPromptRequest{
		RunConfig:         runConfig,
		InterfaceDocPath:  filepath.ToSlash(interfacePath),
		RunDir:            filepath.ToSlash(runDir),
		RoundDir:          filepath.ToSlash(roundDir),
		BaselineSourceDir: filepath.ToSlash(baselineSrc),
		History:           board.Results,
	})
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return nil, err
	}

	if err := os.WriteFile(filepath.Join(runDir, "README.md"), []byte(runReadme(runConfig)), 0o644); err != nil {
		return nil, err
	}

	return &CreatedRun{
		ID:                runID,
		RunDir:            runDir,
		InterfaceDocPath:  interfacePath,
		PromptPath:        promptPath,
		BaselineSourceDir: baselineSrc,
	}, nil
}

func writeBaseline(projectDir, targetPath, dest string) error {
	if strings.TrimSpace(targetPath) == "" {
		body := `# Baseline Source Pending

No target path was provided. The selected agent should inspect the host project, identify the code involved in the optimization request, and copy the first drop-in baseline implementation into this directory before competitor generation begins.
`
		return os.WriteFile(filepath.Join(dest, "README.md"), []byte(body), 0o644)
	}

	src := targetPath
	if !filepath.IsAbs(src) {
		src = filepath.Join(projectDir, targetPath)
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		dest = filepath.Join(dest, filepath.Base(src))
	}
	return archive.CopyPath(src, dest)
}

func baselineDesign(opts Options) string {
	var b strings.Builder
	b.WriteString("# Baseline Candidate\n\n")
	b.WriteString("This candidate represents the original project code selected for optimization.\n\n")
	if opts.TargetPath == "" {
		b.WriteString("The baseline source still needs to be discovered because no target path was provided.\n")
	} else {
		fmt.Fprintf(&b, "Original target path: `%s`\n", opts.TargetPath)
	}
	return b.String()
}

func runReadme(cfg model.RunConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Crucible Run\n\n")
	fmt.Fprintf(&b, "Run ID: `%s`\n\n", cfg.ID)
	fmt.Fprintf(&b, "Optimization request:\n\n%s\n\n", cfg.Optimize)
	fmt.Fprintf(&b, "Important files:\n\n")
	fmt.Fprintf(&b, "- `run.json` stores the immutable run configuration.\n")
	fmt.Fprintf(&b, "- `docs/interfaces.md` stores the drop-in replacement contract.\n")
	fmt.Fprintf(&b, "- `round-0001/candidate-0000-baseline/` stores the extracted baseline.\n")
	fmt.Fprintf(&b, "- `evaluator/evaluator.sh` stores the generated evaluator scaffold.\n")
	fmt.Fprintf(&b, "- `external/policy.json` stores external call policy.\n")
	fmt.Fprintf(&b, "- `prompts/generation-round-0001.md` stores the prompt package for the selected agent.\n")
	fmt.Fprintf(&b, "- `agents/` stores Codex invocation logs and final messages.\n")
	fmt.Fprintf(&b, "- `leaderboard.json` stores current candidate standings.\n")
	return b.String()
}
