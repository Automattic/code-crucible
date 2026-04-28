package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/project"
)

const (
	DefaultSuggestionLimit = 5
	maxDiscoveryFileBytes  = 256 * 1024
)

type PlanOptions struct {
	ProjectDir string
	Optimize   string
	Limit      int
}

type Plan struct {
	ID          string             `json:"id"`
	ProjectDir  string             `json:"project_dir"`
	Optimize    string             `json:"optimize"`
	PlanDir     string             `json:"plan_dir"`
	PlanPath    string             `json:"plan_path"`
	PromptPath  string             `json:"prompt_path"`
	Suggestions []SourceSuggestion `json:"suggestions,omitempty"`
	Notes       []string           `json:"notes,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
}

type SourceSuggestion struct {
	Path   string  `json:"path"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

func CreatePlan(opts PlanOptions) (*Plan, error) {
	if strings.TrimSpace(opts.ProjectDir) == "" {
		opts.ProjectDir = "."
	}
	if strings.TrimSpace(opts.Optimize) == "" {
		return nil, fmt.Errorf("optimization request is required")
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultSuggestionLimit
	}

	absProject, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	if _, err := project.Ensure(absProject); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	planID := fmt.Sprintf("%s-%09d-%s", now.Format("20060102-150405"), now.Nanosecond(), archive.Slug(opts.Optimize, 48))
	planDir := filepath.Join(project.WorkDir(absProject), "discoveries", planID)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return nil, err
	}

	suggestions, notes, err := suggestSourcePaths(absProject, opts.Optimize, opts.Limit)
	if err != nil {
		return nil, err
	}
	planPath := filepath.Join(planDir, "plan.md")
	promptPath := filepath.Join(planDir, "prompt.md")
	plan := &Plan{
		ID:          planID,
		ProjectDir:  ".",
		Optimize:    opts.Optimize,
		PlanDir:     archive.ProjectRelativePath(absProject, planDir),
		PlanPath:    archive.ProjectRelativePath(absProject, planPath),
		PromptPath:  archive.ProjectRelativePath(absProject, promptPath),
		Suggestions: suggestions,
		Notes:       notes,
		CreatedAt:   now,
	}

	if err := writeDiscoveryFile(filepath.Join(planDir, "request.md"), []byte("# Optimization Request\n\n"+opts.Optimize+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := saveDiscoveryJSON(filepath.Join(planDir, "plan.json"), plan); err != nil {
		return nil, err
	}
	if err := writeDiscoveryFile(planPath, []byte(plan.Markdown()), 0o644); err != nil {
		return nil, err
	}
	if err := writeDiscoveryFile(promptPath, []byte(plan.AgentPrompt()), 0o644); err != nil {
		return nil, err
	}
	return plan, nil
}

func (p Plan) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Crucible Discovery Plan\n\n")
	fmt.Fprintf(&b, "Optimization request: %s\n\n", p.Optimize)
	fmt.Fprintf(&b, "## Suggested Source Paths\n\n")
	if len(p.Suggestions) == 0 {
		fmt.Fprintf(&b, "No source path suggestions were found. Leave `--source-path` unset and let the agent discover the involved code.\n\n")
	} else {
		for _, suggestion := range p.Suggestions {
			fmt.Fprintf(&b, "- `%s` (score %.1f): %s\n", suggestion.Path, suggestion.Score, suggestion.Reason)
		}
		fmt.Fprintf(&b, "\n")
	}
	if len(p.Notes) > 0 {
		fmt.Fprintf(&b, "## Notes\n\n")
		for _, note := range p.Notes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, `## Review Checklist

- Confirm the source path, or leave it unset for agent discovery.
- Confirm the drop-in interface boundary that competitors must preserve.
- Confirm the evaluator strategy and any required fixtures.
- Confirm external communication policy: deny, allowlist, mock, replay, or record.
- Add clarifying questions before generation if the optimization target is ambiguous.
`)
	return b.String()
}

func (p Plan) AgentPrompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code Crucible Discovery Prompt\n\n")
	fmt.Fprintf(&b, "Optimization request: %s\n\n", p.Optimize)
	fmt.Fprintf(&b, "Discovery archive: `%s`\n", p.PlanDir)
	fmt.Fprintf(&b, "Structured handoff path: `%s`\n\n", filepath.ToSlash(filepath.Join(p.PlanDir, AgentPlanFilename)))
	fmt.Fprintf(&b, "Inspect the host project and produce a discovery plan for a Code Crucible optimization tournament.\n\n")
	fmt.Fprintf(&b, "Do not modify host project source files. Write no files outside `.crucible`. If the request is ambiguous, ask concise clarifying questions instead of guessing.\n\n")
	fmt.Fprintf(&b, "## Local Source Path Suggestions\n\n")
	if len(p.Suggestions) == 0 {
		fmt.Fprintf(&b, "The local heuristic scan did not find a confident source path suggestion.\n\n")
	} else {
		for _, suggestion := range p.Suggestions {
			fmt.Fprintf(&b, "- `%s`: %s\n", suggestion.Path, suggestion.Reason)
		}
		fmt.Fprintf(&b, "\n")
	}
	fmt.Fprintf(&b, `## Output

Return a Markdown discovery plan with these sections:

1. Recommendation: the source path to use, or "none yet" if discovery should happen during generation.
2. Drop-in interface: public function, command, class, handler, route, or module boundary.
3. Required inputs and outputs.
4. External communications and recommended external mode.
5. Evaluator strategy: stable tests, benchmarks, fixtures, and metrics to collect.
6. Clarifying questions, if any.
7. Suggested next command.

End the final response with a fenced JSON block using this exact machine-readable shape. Use empty strings or empty arrays for unknown values; do not add comments or trailing commas.

`)
	b.WriteString("```json\n")
	fmt.Fprintf(&b, `{
  "source_path": "internal/example/path.go",
  "source_path_confidence": "high|medium|low|none",
  "drop_in_interface": "function, command, class, handler, route, or module boundary",
  "inputs": ["required input values or fixtures"],
  "outputs": ["required output values or side effects"],
  "external_communications": [
    {
      "service": "service or host",
      "protocol": "http|https|grpc|raw-socket|filesystem|none",
      "request": "request shape",
      "response": "response shape",
      "auth": "auth requirements",
      "mode": "deny|allowlist|mock|replay|record"
    }
  ],
  "external_mode": "deny|allowlist|mock|replay|record",
  "evaluator_strategy": ["deterministic checks, benchmarks, fixture needs"],
  "metrics": ["primary and secondary metrics"],
  "clarifying_questions": ["question to ask before generation"],
  "suggested_next_command": "crucible run \"...\" --source-path ...",
  "notes": ["important implementation or risk notes"]
}
`)
	b.WriteString("```\n")
	return b.String()
}

func suggestSourcePaths(projectDir, optimize string, limit int) ([]SourceSuggestion, []string, error) {
	tokens := importantTokens(optimize)
	if len(tokens) == 0 {
		return nil, []string{"No useful search tokens were found in the optimization request."}, nil
	}

	var candidates []SourceSuggestion
	err := filepath.WalkDir(projectDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipFile(name) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxDiscoveryFileBytes {
			return nil
		}
		rel, err := filepath.Rel(projectDir, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		score, reason := scoreSourcePath(path, rel, tokens)
		if score > 0 {
			candidates = append(candidates, SourceSuggestion{
				Path:   rel,
				Score:  score,
				Reason: reason,
			})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Path < candidates[j].Path
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	notes := []string{"Suggestions are from a local heuristic scan. Confirm before using one as the baseline source."}
	return candidates, notes, nil
}

func scoreSourcePath(absPath, relPath string, tokens []string) (float64, string) {
	pathText := strings.ToLower(relPath)
	pathScore := 0.0
	var pathMatches []string
	for _, token := range tokens {
		if strings.Contains(pathText, token) {
			pathScore += 8
			pathMatches = append(pathMatches, token)
		}
	}

	contentScore := 0.0
	data, err := os.ReadFile(absPath)
	if err == nil && looksText(data) {
		content := strings.ToLower(string(data))
		for _, token := range tokens {
			count := strings.Count(content, token)
			if count > 6 {
				count = 6
			}
			contentScore += float64(count)
		}
	}
	score := pathScore + contentScore
	if score == 0 {
		return 0, ""
	}
	reasons := make([]string, 0, 2)
	if len(pathMatches) > 0 {
		reasons = append(reasons, "path matches "+strings.Join(pathMatches, ", "))
	}
	if contentScore > 0 {
		reasons = append(reasons, fmt.Sprintf("content match score %.1f", contentScore))
	}
	return score, strings.Join(reasons, "; ")
}

func importantTokens(value string) []string {
	words := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	seen := map[string]bool{}
	var tokens []string
	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) < 3 || stopWords[word] || seen[word] {
			continue
		}
		seen[word] = true
		tokens = append(tokens, word)
	}
	return tokens
}

var stopWords = map[string]bool{
	"and": true, "for": true, "the": true, "this": true, "that": true,
	"with": true, "from": true, "into": true, "make": true, "reduce": true,
	"improve": true, "optimize": true, "faster": true, "latency": true,
	"performance": true, "speed": true, "code": true, "function": true,
	"module": true, "handler": true, "endpoint": true, "feature": true,
}

func shouldSkipFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf", ".zip", ".gz", ".tar", ".sqlite", ".db":
		return true
	default:
		return false
	}
}

func looksText(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
}

func saveDiscoveryJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeDiscoveryFile(path, data, 0o644)
}

func writeDiscoveryFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
