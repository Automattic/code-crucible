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
	PartialCandidates []string  `json:"partial_candidates,omitempty"`
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
		switch event.Action {
		case "evaluate":
			markCanceledEvaluationCandidates(board, event)
			if len(event.UpdatedCandidates) > 0 {
				if err := archive.SaveJSON(leaderboardPath, board); err != nil {
					return nil, err
				}
			}
		case "generate":
			roundDir := cfg.RoundDir
			if roundDir == "" {
				roundDir = filepath.Join(runDir, "round-0001")
			} else {
				roundDir = archive.ProjectPath(absProject, roundDir)
			}
			if err := markCanceledGeneratedCandidates(board, event, absProject, roundDir, cfg.External.Mode); err != nil {
				return nil, err
			}
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

func markCanceledGeneratedCandidates(board *model.Leaderboard, event *CancellationEvent, projectDir, roundDir string, externalMode model.ExternalMode) error {
	if !externalMode.Valid() {
		externalMode = model.ExternalModeDeny
	}
	seen := make(map[string]bool, len(board.Results))
	for _, result := range board.Results {
		seen[result.Candidate.ID] = true
	}
	entries, err := os.ReadDir(roundDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	roundNumber := roundNumberFromDir(roundDir)
	if roundNumber <= 0 {
		roundNumber = 1
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if id == "candidate-0000-baseline" || !strings.HasPrefix(id, "candidate-") {
			continue
		}
		if !generatedCandidateIDPattern.MatchString(id) {
			event.PartialCandidates = append(event.PartialCandidates, filepath.ToSlash(filepath.Join(roundDir, id))+": generated candidate directories must use candidate-NNNN")
			continue
		}
		if seen[id] {
			continue
		}
		candidateDir := filepath.Join(roundDir, id)
		candidate, issue := loadAdoptableCandidate(candidateDir, roundDir, id, roundNumber, AdoptionOptions{
			ProjectDir: projectDir,
		})
		if issue != nil {
			event.PartialCandidates = append(event.PartialCandidates, fmt.Sprintf("%s: %s", filepath.ToSlash(issue.Path), issue.Reason))
			continue
		}
		if candidate.Agent == "" {
			candidate.Agent = "unknown"
		}
		if candidate.SourcePath == "" {
			candidate.SourcePath = archive.ProjectRelativePath(projectDir, filepath.Join(candidateDir, "src"))
		}
		board.Results = append(board.Results, model.CandidateResult{
			Candidate: candidate,
			External: model.ExternalCallTrace{
				Mode:         externalMode,
				PolicyPassed: false,
			},
			Verdict: model.Verdict{
				CorrectnessPassed:    false,
				BenchmarkPassed:      false,
				ExternalPolicyPassed: false,
				Notes: []string{
					event.Reason,
				},
			},
			ScoreExplanation: &model.ScoreExplanation{
				Scoreable: false,
				Reason:    "candidate generation canceled",
			},
			Status: model.CandidateStatusCanceled,
		})
		seen[id] = true
		event.UpdatedCandidates = append(event.UpdatedCandidates, id)
	}
	return nil
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
