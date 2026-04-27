package archive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/gaarai/code-crucible/internal/model"
	"github.com/gaarai/code-crucible/internal/project"
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

func LeaderboardPath(projectDir, runID string) (string, error) {
	if strings.TrimSpace(runID) == "" {
		runDir, err := LatestRunDir(projectDir)
		if err != nil {
			return "", err
		}
		return filepath.Join(runDir, "leaderboard.json"), nil
	}
	return filepath.Join(project.WorkDir(projectDir), "runs", runID, "leaderboard.json"), nil
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
