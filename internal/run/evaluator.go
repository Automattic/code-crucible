package run

import (
	"path/filepath"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

type EvaluatorReadyOptions struct {
	ProjectDir string
	RunID      string
}

type EvaluatorReadyReport struct {
	RunID             string   `json:"run_id"`
	ConfigPath        string   `json:"config_path"`
	LeaderboardPath   string   `json:"leaderboard_path"`
	UpdatedCandidates []string `json:"updated_candidates"`
}

func MarkEvaluatorReady(opts EvaluatorReadyOptions) (*EvaluatorReadyReport, error) {
	projectDir := opts.ProjectDir
	if projectDir == "" {
		projectDir = "."
	}
	configPath, err := archive.RunConfigPath(projectDir, opts.RunID)
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
		runDir = archive.ProjectPath(projectDir, runDir)
	}
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return nil, err
	}

	report := &EvaluatorReadyReport{
		RunID:           cfg.ID,
		ConfigPath:      filepath.ToSlash(configPath),
		LeaderboardPath: filepath.ToSlash(leaderboardPath),
	}
	if !cfg.EvaluatorGenerated {
		cfg.EvaluatorGenerated = true
		if err := archive.SaveJSON(configPath, cfg); err != nil {
			return nil, err
		}
	}
	for i := range board.Results {
		if board.Results[i].Status != model.CandidateStatusNeedsEvaluator {
			continue
		}
		board.Results[i].Status = "pending"
		board.Results[i].Verdict.Notes = []string{
			"Evaluator is configured; candidate has not been evaluated yet.",
		}
		report.UpdatedCandidates = append(report.UpdatedCandidates, board.Results[i].Candidate.ID)
	}
	if len(report.UpdatedCandidates) > 0 {
		if err := archive.SaveJSON(leaderboardPath, board); err != nil {
			return nil, err
		}
	}
	return report, nil
}
