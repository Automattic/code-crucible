package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

type CancellationOptions struct {
	ProjectDir  string
	RunID       string
	Action      string
	CandidateID string
	Reason      string
}

type CancellationEvent struct {
	RunID             string    `json:"run_id"`
	Action            string    `json:"action"`
	CandidateID       string    `json:"candidate_id,omitempty"`
	Reason            string    `json:"reason,omitempty"`
	CanceledAt        time.Time `json:"canceled_at"`
	LeaderboardPath   string    `json:"leaderboard_path,omitempty"`
	EventPath         string    `json:"event_path"`
	UpdatedCandidates []string  `json:"updated_candidates,omitempty"`
	SkippedCandidates []string  `json:"skipped_candidates,omitempty"`
}

func MarkCanceled(opts CancellationOptions) (*CancellationEvent, error) {
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
	} else {
		runDir = archive.ProjectPath(absProject, runDir)
	}

	event := &CancellationEvent{
		RunID:       cfg.ID,
		Action:      strings.TrimSpace(opts.Action),
		CandidateID: strings.TrimSpace(opts.CandidateID),
		Reason:      strings.TrimSpace(opts.Reason),
		CanceledAt:  time.Now().UTC(),
	}
	if event.Action == "" {
		event.Action = "unknown"
	}
	if event.Reason == "" {
		event.Reason = "user canceled action"
	}

	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	if board, err := archive.LoadLeaderboard(leaderboardPath); err == nil {
		event.LeaderboardPath = filepath.ToSlash(leaderboardPath)
		if event.Action == "evaluate" {
			markCanceledEvaluationCandidates(board, event)
			if len(event.UpdatedCandidates) > 0 {
				if err := archive.SaveJSON(leaderboardPath, board); err != nil {
					return nil, err
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	eventPath, err := appendCancellationEvent(runDir, event)
	if err != nil {
		return nil, err
	}
	event.EventPath = filepath.ToSlash(eventPath)
	return event, nil
}

func markCanceledEvaluationCandidates(board *model.Leaderboard, event *CancellationEvent) {
	for i := range board.Results {
		result := &board.Results[i]
		if event.CandidateID != "" && result.Candidate.ID != event.CandidateID {
			continue
		}
		if !candidateStatusCancelable(result.Status) {
			event.SkippedCandidates = append(event.SkippedCandidates, result.Candidate.ID)
			continue
		}
		result.Status = model.CandidateStatusCanceled
		result.Score = 0
		result.ScoreExplanation = &model.ScoreExplanation{
			Scoreable: false,
			Reason:    "candidate evaluation canceled",
		}
		result.Verdict.CorrectnessPassed = false
		result.Verdict.BenchmarkPassed = false
		result.Verdict.ExternalPolicyPassed = false
		result.Verdict.Notes = appendMissingStrings(result.Verdict.Notes, []string{event.Reason})
		event.UpdatedCandidates = append(event.UpdatedCandidates, result.Candidate.ID)
	}
}

func candidateStatusCancelable(status string) bool {
	switch strings.TrimSpace(status) {
	case "", model.CandidateStatusGenerated, "pending", "running":
		return true
	default:
		return false
	}
}

func appendCancellationEvent(runDir string, event *CancellationEvent) (string, error) {
	eventsDir := filepath.Join(runDir, "events")
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(eventsDir, "cancellations.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	defer file.Close()
	event.EventPath = filepath.ToSlash(path)
	data, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("encode cancellation event: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return "", err
	}
	return path, nil
}
