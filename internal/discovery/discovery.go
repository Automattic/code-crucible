package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Discovery struct {
	ProjectDir string
	Optimize   string
	TargetPath string
	Files      []FileSummary
	Languages  []string
	Notes      []string
}

type FileSummary struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func Analyze(projectDir, optimize, targetPath string) (*Discovery, error) {
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	discovered := &Discovery{
		ProjectDir: absProject,
		Optimize:   optimize,
		TargetPath: targetPath,
	}

	if strings.TrimSpace(targetPath) == "" {
		discovered.Notes = append(discovered.Notes, "No target path was provided. The selected agent must locate the involved code before generating competitors.")
		return discovered, nil
	}

	absTarget := targetPath
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Join(absProject, targetPath)
	}

	info, err := os.Stat(absTarget)
	if err != nil {
		return nil, fmt.Errorf("inspect target path: %w", err)
	}

	if !info.IsDir() {
		rel, err := filepath.Rel(absProject, absTarget)
		if err != nil {
			rel = absTarget
		}
		discovered.Files = append(discovered.Files, FileSummary{Path: filepath.ToSlash(rel), Size: info.Size()})
		discovered.Languages = sortedLanguages(discovered.Files)
		return discovered, nil
	}

	err = filepath.WalkDir(absTarget, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() && shouldSkipDir(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(absProject, path)
		if err != nil {
			rel = path
		}
		discovered.Files = append(discovered.Files, FileSummary{Path: filepath.ToSlash(rel), Size: info.Size()})
		if len(discovered.Files) >= 200 {
			discovered.Notes = append(discovered.Notes, "File list was capped at 200 entries for the initial scaffold.")
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	discovered.Languages = sortedLanguages(discovered.Files)
	return discovered, nil
}

func (d Discovery) InterfaceMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Interface Discovery\n\n")
	fmt.Fprintf(&b, "Optimization request: %s\n\n", d.Optimize)

	if d.TargetPath == "" {
		fmt.Fprintf(&b, "Target path: not yet selected by the operator.\n\n")
	} else {
		fmt.Fprintf(&b, "Target path: `%s`\n\n", d.TargetPath)
	}

	if len(d.Languages) > 0 {
		fmt.Fprintf(&b, "Detected languages: %s\n\n", strings.Join(d.Languages, ", "))
	}

	if len(d.Files) > 0 {
		fmt.Fprintf(&b, "## Candidate Source Inputs\n\n")
		for _, file := range d.Files {
			fmt.Fprintf(&b, "- `%s` (%d bytes)\n", file.Path, file.Size)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, `## Required Drop-in Interface

Document the public function, command, class, handler, API route, or module boundary that each competitor must implement.

- Inputs:
- Outputs:
- Error behavior:
- Side effects:
- Concurrency expectations:
- Persistence or filesystem behavior:

## External Communications

Document every external dependency used by the evaluated code.

- Service or host:
- Protocol:
- Request shape:
- Response shape:
- Authentication requirements:
- Allowed mode: deny, allowlist, mock, replay, or record
- Fixture or mock handler:

## Correctness Checks

List deterministic checks that every competitor must pass before benchmarking.

- Required fixtures:
- Edge cases:
- Golden outputs:
- Regression cases:

## Benchmark Metrics

List the metrics that should decide the tournament.

- Primary metric:
- Secondary metrics:
- Constraints:
- Disqualification rules:

## Agent Notes

Use this document as the contract for generated competitors. A competitor is only valid when it can replace the baseline at the documented boundary without requiring changes to caller code or test harnesses.
`)

	if len(d.Notes) > 0 {
		fmt.Fprintf(&b, "\n## Discovery Notes\n\n")
		for _, note := range d.Notes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
	}

	return b.String()
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".crucible", ".git", ".hg", ".svn", ".next", "build", "dist", "node_modules", "target", "vendor":
		return true
	default:
		return false
	}
}

func sortedLanguages(files []FileSummary) []string {
	seen := map[string]bool{}
	for _, file := range files {
		lang := languageForExt(filepath.Ext(file.Path))
		if lang != "" {
			seen[lang] = true
		}
	}

	var languages []string
	for lang := range seen {
		languages = append(languages, lang)
	}
	sort.Strings(languages)
	return languages
}

func languageForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".go":
		return "Go"
	case ".js", ".mjs", ".cjs":
		return "JavaScript"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".jsx":
		return "JavaScript JSX"
	case ".py":
		return "Python"
	case ".php":
		return "PHP"
	case ".rb":
		return "Ruby"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".cs":
		return "C#"
	case ".c", ".h":
		return "C"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return "C++"
	case ".sh":
		return "Shell"
	default:
		return ""
	}
}
