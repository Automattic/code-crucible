package run

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Automattic/code-crucible/internal/agent"
	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

type NextRoundOptions struct {
	ProjectDir string
	RunID      string
	Parents    int
}

type NextRoundReport struct {
	RunID           string   `json:"run_id"`
	PreviousRound   int      `json:"previous_round"`
	Round           int      `json:"round"`
	RoundDir        string   `json:"round_dir"`
	PromptPath      string   `json:"prompt_path"`
	LeaderboardPath string   `json:"leaderboard_path"`
	ParentIDs       []string `json:"parent_ids"`
	NextCandidateID string   `json:"next_candidate_id"`
}

func PrepareNextRound(opts NextRoundOptions) (*NextRoundReport, error) {
	projectDir := opts.ProjectDir
	if strings.TrimSpace(projectDir) == "" {
		projectDir = "."
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

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
	}
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return nil, err
	}

	parentResults := selectRoundParents(board.Results, opts.Parents)
	if len(parentResults) == 0 {
		return nil, fmt.Errorf("no passed candidates are available to seed the next round")
	}
	parentIDs := make([]string, 0, len(parentResults))
	for _, result := range parentResults {
		parentIDs = append(parentIDs, result.Candidate.ID)
	}

	previousRound := roundNumberFromDir(cfg.RoundDir)
	if previousRound <= 0 {
		previousRound = 1
	}
	nextRound := previousRound + 1
	roundDir := roundDir(runDir, nextRound)
	if exists, err := pathExists(roundDir); err != nil {
		return nil, err
	} else if exists {
		return nil, fmt.Errorf("round directory already exists: %s", roundDir)
	}
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		return nil, err
	}

	scratchDir := filepath.Join(runDir, "tmp", "generation", roundName(nextRound))
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return nil, err
	}

	promptPath := filepath.Join(runDir, "prompts", fmt.Sprintf("generation-%s.md", roundName(nextRound)))
	nextCandidate := nextCandidateNumber(board.Results)
	cfg.RoundDir = filepath.ToSlash(roundDir)
	cfg.PromptPath = filepath.ToSlash(promptPath)
	if cfg.Rounds < nextRound {
		cfg.Rounds = nextRound
	}

	prompt := agent.BuildGenerationPrompt(agent.GenerationPromptRequest{
		RunConfig:         *cfg,
		Round:             nextRound,
		NextCandidate:     nextCandidate,
		ParentIDs:         parentIDs,
		InterfaceDocPath:  cfg.InterfaceDocs,
		RunDir:            filepath.ToSlash(runDir),
		RoundDir:          filepath.ToSlash(roundDir),
		BaselineSourceDir: baselineSourceDir(board.Results),
		ScratchDir:        filepath.ToSlash(scratchDir),
		History:           board.Results,
	})
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return nil, err
	}
	if err := archive.SaveJSON(configPath, cfg); err != nil {
		return nil, err
	}

	return &NextRoundReport{
		RunID:           cfg.ID,
		PreviousRound:   previousRound,
		Round:           nextRound,
		RoundDir:        filepath.ToSlash(roundDir),
		PromptPath:      filepath.ToSlash(promptPath),
		LeaderboardPath: filepath.ToSlash(leaderboardPath),
		ParentIDs:       parentIDs,
		NextCandidateID: fmt.Sprintf("candidate-%04d", nextCandidate),
	}, nil
}

func selectRoundParents(results []model.CandidateResult, limit int) []model.CandidateResult {
	if limit <= 0 {
		limit = 3
	}
	passed := make([]model.CandidateResult, 0, len(results))
	for _, result := range results {
		if result.Status == "passed" {
			passed = append(passed, result)
		}
	}
	sort.SliceStable(passed, func(i, j int) bool {
		left := passed[i]
		right := passed[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if left.Metrics.P95LatencyMS != right.Metrics.P95LatencyMS {
			return left.Metrics.P95LatencyMS < right.Metrics.P95LatencyMS
		}
		return left.Candidate.ID < right.Candidate.ID
	})
	if len(passed) > limit {
		passed = passed[:limit]
	}
	return passed
}

func nextCandidateNumber(results []model.CandidateResult) int {
	next := 1
	for _, result := range results {
		n := candidateNumber(result.Candidate.ID)
		if n >= next {
			next = n + 1
		}
	}
	return next
}

func candidateNumber(id string) int {
	if !strings.HasPrefix(id, "candidate-") {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "candidate-"))
	if err != nil {
		return -1
	}
	return n
}

func baselineSourceDir(results []model.CandidateResult) string {
	for _, result := range results {
		if result.Candidate.Baseline {
			return result.Candidate.SourcePath
		}
	}
	return ""
}

func roundDir(runDir string, round int) string {
	return filepath.Join(runDir, roundName(round))
}

func roundName(round int) string {
	if round <= 0 {
		round = 1
	}
	return fmt.Sprintf("round-%04d", round)
}

func roundNumberFromDir(path string) int {
	base := filepath.Base(filepath.Clean(path))
	if !strings.HasPrefix(base, "round-") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(base, "round-"))
	if err != nil {
		return 0
	}
	return n
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
