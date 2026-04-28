package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

var generatedCandidateIDPattern = regexp.MustCompile(`^candidate-\d{4}$`)

type AdoptionOptions struct {
	ProjectDir string
	RunID      string
	Agent      string
	Model      string
}

type AdoptionIssue struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type AdoptionReport struct {
	RunID           string          `json:"run_id"`
	RoundDir        string          `json:"round_dir"`
	LeaderboardPath string          `json:"leaderboard_path"`
	Added           []string        `json:"added"`
	Existing        []string        `json:"existing"`
	Invalid         []AdoptionIssue `json:"invalid"`
}

func AdoptCandidates(opts AdoptionOptions) (*AdoptionReport, error) {
	projectDir := opts.ProjectDir
	if strings.TrimSpace(projectDir) == "" {
		projectDir = "."
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}
	opts.ProjectDir = absProject

	configPath, err := archive.RunConfigPath(absProject, opts.RunID)
	if err != nil {
		return nil, err
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		return nil, err
	}

	runDir := cfg.RunDir
	if runDir == "" {
		runDir = filepath.Dir(configPath)
	} else {
		runDir = archive.ProjectPath(absProject, runDir)
	}
	roundDir := cfg.RoundDir
	if roundDir == "" {
		roundDir = filepath.Join(runDir, "round-0001")
	} else {
		roundDir = archive.ProjectPath(absProject, roundDir)
	}
	roundNumber := roundNumberFromDir(roundDir)
	if roundNumber <= 0 {
		roundNumber = 1
	}

	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return nil, err
	}

	report := &AdoptionReport{
		RunID:           cfg.ID,
		RoundDir:        roundDir,
		LeaderboardPath: leaderboardPath,
	}

	seen := make(map[string]bool, len(board.Results))
	for _, result := range board.Results {
		seen[result.Candidate.ID] = true
	}

	entries, err := os.ReadDir(roundDir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		id := entry.Name()
		if id == "candidate-0000-baseline" {
			continue
		}
		if !strings.HasPrefix(id, "candidate-") {
			continue
		}
		if !generatedCandidateIDPattern.MatchString(id) {
			report.Invalid = append(report.Invalid, AdoptionIssue{
				ID:     id,
				Path:   filepath.Join(roundDir, id),
				Reason: "generated candidate directories must use candidate-NNNN",
			})
			continue
		}
		if seen[id] {
			report.Existing = append(report.Existing, id)
			continue
		}

		candidate, issue := loadAdoptableCandidate(filepath.Join(roundDir, id), roundDir, id, roundNumber, opts)
		if issue != nil {
			report.Invalid = append(report.Invalid, *issue)
			continue
		}

		board.Results = append(board.Results, model.CandidateResult{
			Candidate: candidate,
			External: model.ExternalCallTrace{
				Mode:         cfg.External.Mode,
				PolicyPassed: false,
			},
			Verdict: model.Verdict{
				CorrectnessPassed:    false,
				BenchmarkPassed:      false,
				ExternalPolicyPassed: false,
				Notes: []string{
					"Candidate adopted from generated artifacts but not evaluated yet.",
				},
			},
			Status: "generated",
		})
		seen[id] = true
		report.Added = append(report.Added, id)
	}

	if len(report.Added) > 0 {
		if err := archive.SaveJSON(leaderboardPath, board); err != nil {
			return nil, err
		}
	}

	return report, nil
}

func loadAdoptableCandidate(candidateDir, roundDir, id string, roundNumber int, opts AdoptionOptions) (model.Candidate, *AdoptionIssue) {
	srcDir := filepath.Join(candidateDir, "src")
	if info, err := os.Stat(srcDir); err != nil || !info.IsDir() {
		return model.Candidate{}, invalidCandidate(id, candidateDir, "missing src directory")
	}

	designPath := filepath.Join(candidateDir, "design.md")
	if info, err := os.Stat(designPath); err != nil || info.IsDir() {
		return model.Candidate{}, invalidCandidate(id, candidateDir, "missing design.md")
	}

	metadataPath := filepath.Join(candidateDir, "candidate.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return model.Candidate{}, invalidCandidate(id, candidateDir, "missing candidate.json")
	}

	var candidate model.Candidate
	if err := json.Unmarshal(data, &candidate); err != nil {
		return model.Candidate{}, invalidCandidate(id, metadataPath, fmt.Sprintf("invalid candidate.json: %v", err))
	}

	if candidate.ID != id {
		return model.Candidate{}, invalidCandidate(id, metadataPath, "candidate.json id must match directory name")
	}
	if strings.TrimSpace(candidate.Name) == "" {
		return model.Candidate{}, invalidCandidate(id, metadataPath, "candidate.json name is required")
	}
	if candidate.Round != roundNumber {
		return model.Candidate{}, invalidCandidate(id, metadataPath, fmt.Sprintf("candidate.json round must be %d", roundNumber))
	}
	if candidate.Baseline {
		return model.Candidate{}, invalidCandidate(id, metadataPath, "generated candidates cannot be marked as baseline")
	}
	if len(candidate.ParentIDs) == 0 {
		return model.Candidate{}, invalidCandidate(id, metadataPath, "candidate.json parent_ids is required")
	}
	if strings.TrimSpace(candidate.Agent) == "" {
		return model.Candidate{}, invalidCandidate(id, metadataPath, "candidate.json agent is required")
	}
	if strings.TrimSpace(candidate.SourcePath) == "" {
		return model.Candidate{}, invalidCandidate(id, metadataPath, "candidate.json source_path is required")
	}
	if !sourcePathResolvesToSrc(candidate.SourcePath, candidateDir, roundDir, srcDir) {
		return model.Candidate{}, invalidCandidate(id, metadataPath, "candidate.json source_path must resolve to src")
	}

	if candidate.Model == "" && opts.Model != "" {
		candidate.Model = opts.Model
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now().UTC()
	}
	candidate.SourcePath = archive.ProjectRelativePath(opts.ProjectDir, srcDir)

	return candidate, nil
}

func invalidCandidate(id, path, reason string) *AdoptionIssue {
	return &AdoptionIssue{
		ID:     id,
		Path:   path,
		Reason: reason,
	}
}

func sourcePathResolvesToSrc(sourcePath, candidateDir, roundDir, srcDir string) bool {
	candidates := []string{sourcePath}
	if !filepath.IsAbs(sourcePath) {
		candidates = append(candidates,
			filepath.Join(candidateDir, sourcePath),
			filepath.Join(roundDir, sourcePath),
		)
	}

	cleanSrc := filepath.Clean(srcDir)
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		if candidate == cleanSrc {
			return true
		}
	}
	return false
}
