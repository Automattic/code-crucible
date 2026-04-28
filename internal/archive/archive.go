package archive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
)

func SaveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func LoadLeaderboard(path string) (*model.Leaderboard, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var board model.Leaderboard
	if err := json.Unmarshal(data, &board); err != nil {
		return nil, err
	}
	return &board, nil
}

func LoadRunConfig(path string) (*model.RunConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg model.RunConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LatestRunDir(projectDir string) (string, error) {
	runsDir := filepath.Join(project.WorkDir(projectDir), "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", err
	}

	var runs []string
	for _, entry := range entries {
		if entry.IsDir() {
			runs = append(runs, filepath.Join(runsDir, entry.Name()))
		}
	}
	sort.Strings(runs)
	if len(runs) == 0 {
		return "", fmt.Errorf("no Code Crucible runs found in %s", runsDir)
	}
	return runs[len(runs)-1], nil
}

func RunDir(projectDir, runID string) (string, error) {
	if strings.TrimSpace(runID) == "" {
		return LatestRunDir(projectDir)
	}
	return filepath.Join(project.WorkDir(projectDir), "runs", runID), nil
}

func RunConfigPath(projectDir, runID string) (string, error) {
	runDir, err := RunDir(projectDir, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(runDir, "run.json"), nil
}

func LeaderboardPath(projectDir, runID string) (string, error) {
	runDir, err := RunDir(projectDir, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(runDir, "leaderboard.json"), nil
}

func ProjectPath(projectDir, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	path = filepath.FromSlash(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(projectDir, path))
}

func ProjectRelativePath(projectDir, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	rel, err := filepath.Rel(projectDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(filepath.Clean(rel))
}

func Slug(input string, maxLen int) string {
	input = strings.ToLower(strings.TrimSpace(input))
	var out strings.Builder
	lastDash := false
	for _, r := range input {
		allowed := unicode.IsLetter(r) || unicode.IsDigit(r)
		if allowed {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && out.Len() > 0 {
			out.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(out.String(), "-")
	if slug == "" {
		return "optimization"
	}
	if maxLen > 0 && len(slug) > maxLen {
		truncated := strings.TrimRight(slug[:maxLen], "-")
		if cut := strings.LastIndex(truncated, "-"); cut > 0 {
			truncated = truncated[:cut]
		}
		slug = truncated
		if slug == "" {
			return "optimization"
		}
	}
	return slug
}
