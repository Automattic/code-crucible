package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type QueryOptions struct {
	ProjectDir string
	RunID      string
	Status     string
	Limit      int
}

type RunSummary struct {
	ID             string  `json:"id"`
	Optimize       string  `json:"optimize"`
	RunDir         string  `json:"run_dir"`
	ActiveRound    int     `json:"active_round"`
	Agent          string  `json:"agent"`
	Variants       int     `json:"variants"`
	Rounds         int     `json:"rounds"`
	ExternalMode   string  `json:"external_mode"`
	CreatedAt      string  `json:"created_at"`
	Candidates     int     `json:"candidates"`
	Passed         int     `json:"passed"`
	Failed         int     `json:"failed"`
	Pending        int     `json:"pending"`
	BestCandidate  string  `json:"best_candidate,omitempty"`
	BestScore      float64 `json:"best_score,omitempty"`
	BestPrimaryMS  float64 `json:"best_primary_metric_ms,omitempty"`
	BestMemoryByte float64 `json:"best_memory_bytes,omitempty"`
}

type CandidateSummary struct {
	RunID                string  `json:"run_id"`
	RunOptimize          string  `json:"run_optimize"`
	RunDir               string  `json:"run_dir"`
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Status               string  `json:"status"`
	Round                int     `json:"round"`
	Agent                string  `json:"agent"`
	Model                string  `json:"model,omitempty"`
	Score                float64 `json:"score"`
	Scoreable            bool    `json:"scoreable"`
	ScoreReason          string  `json:"score_reason,omitempty"`
	PrimaryMetricMS      float64 `json:"primary_metric_ms,omitempty"`
	P95LatencyMS         float64 `json:"p95_latency_ms,omitempty"`
	BenchmarkNsPerOp     float64 `json:"benchmark_ns_per_op,omitempty"`
	MemoryMetricBytes    float64 `json:"memory_metric_bytes,omitempty"`
	WallTimeMS           float64 `json:"wall_time_ms,omitempty"`
	CPUUserSeconds       float64 `json:"cpu_user_seconds,omitempty"`
	CPUSystemSeconds     float64 `json:"cpu_system_seconds,omitempty"`
	ExternalCallCount    int     `json:"external_call_count,omitempty"`
	ExternalPolicyStatus string  `json:"external_policy_status,omitempty"`
	SourcePath           string  `json:"source_path"`
}

func QueryRuns(opts QueryOptions) ([]RunSummary, error) {
	db, err := openQueryDB(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `
SELECT
	r.id,
	r.optimize,
	r.run_dir,
	r.active_round,
	r.agent,
	r.variants,
	r.rounds,
	r.external_mode,
	r.created_at,
	COUNT(c.id) AS candidates,
	COALESCE(SUM(CASE WHEN c.status = 'passed' THEN 1 ELSE 0 END), 0) AS passed,
	COALESCE(SUM(CASE WHEN c.status = 'failed' THEN 1 ELSE 0 END), 0) AS failed,
	COALESCE(SUM(CASE WHEN c.status NOT IN ('passed', 'failed') THEN 1 ELSE 0 END), 0) AS pending,
	COALESCE((
		SELECT c2.id
		FROM candidates c2
		WHERE c2.run_id = r.id AND c2.status = 'passed'
		ORDER BY c2.score DESC, c2.p95_latency_ms ASC, c2.id ASC
		LIMIT 1
	), '') AS best_candidate,
	COALESCE((
		SELECT c2.score
		FROM candidates c2
		WHERE c2.run_id = r.id AND c2.status = 'passed'
		ORDER BY c2.score DESC, c2.p95_latency_ms ASC, c2.id ASC
		LIMIT 1
	), 0) AS best_score,
	COALESCE((
		SELECT c2.primary_metric_ms
		FROM candidates c2
		WHERE c2.run_id = r.id AND c2.status = 'passed'
		ORDER BY c2.score DESC, c2.p95_latency_ms ASC, c2.id ASC
		LIMIT 1
	), 0) AS best_primary_metric_ms,
	COALESCE((
		SELECT c2.memory_metric_bytes
		FROM candidates c2
		WHERE c2.run_id = r.id AND c2.status = 'passed'
		ORDER BY c2.score DESC, c2.p95_latency_ms ASC, c2.id ASC
		LIMIT 1
	), 0) AS best_memory_bytes
FROM runs r
LEFT JOIN candidates c ON c.run_id = r.id
GROUP BY r.id
ORDER BY r.created_at DESC, r.id DESC`
	query = appendLimit(query, opts.Limit)

	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("query indexed runs: %w", err)
	}
	defer rows.Close()

	var summaries []RunSummary
	for rows.Next() {
		var summary RunSummary
		if err := rows.Scan(
			&summary.ID,
			&summary.Optimize,
			&summary.RunDir,
			&summary.ActiveRound,
			&summary.Agent,
			&summary.Variants,
			&summary.Rounds,
			&summary.ExternalMode,
			&summary.CreatedAt,
			&summary.Candidates,
			&summary.Passed,
			&summary.Failed,
			&summary.Pending,
			&summary.BestCandidate,
			&summary.BestScore,
			&summary.BestPrimaryMS,
			&summary.BestMemoryByte,
		); err != nil {
			return nil, fmt.Errorf("scan indexed run: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query indexed runs: %w", err)
	}
	return summaries, nil
}

func QueryCandidates(opts QueryOptions) ([]CandidateSummary, error) {
	db, err := openQueryDB(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var filters []string
	var args []any
	if strings.TrimSpace(opts.RunID) != "" {
		filters = append(filters, "c.run_id = ?")
		args = append(args, strings.TrimSpace(opts.RunID))
	}
	if strings.TrimSpace(opts.Status) != "" {
		filters = append(filters, "c.status = ?")
		args = append(args, strings.TrimSpace(opts.Status))
	}

	query := `
SELECT
	c.run_id,
	r.optimize,
	r.run_dir,
	c.id,
	c.name,
	c.status,
	c.round,
	c.agent,
	c.model,
	c.score,
	c.scoreable,
	c.score_reason,
	c.primary_metric_ms,
	c.p95_latency_ms,
	c.benchmark_ns_per_op,
	c.memory_metric_bytes,
	c.wall_time_ms,
	c.cpu_user_seconds,
	c.cpu_system_seconds,
	c.external_call_count,
	c.external_policy_status,
	c.source_path
FROM candidates c
JOIN runs r ON r.id = c.run_id`
	if len(filters) > 0 {
		query += "\nWHERE " + strings.Join(filters, " AND ")
	}
	query += `
ORDER BY
	CASE c.status WHEN 'passed' THEN 0 WHEN 'failed' THEN 1 ELSE 2 END,
	c.score DESC,
	c.p95_latency_ms ASC,
	c.id ASC`
	query = appendLimit(query, opts.Limit)

	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("query indexed candidates: %w", err)
	}
	defer rows.Close()

	var summaries []CandidateSummary
	for rows.Next() {
		var summary CandidateSummary
		var scoreable int
		if err := rows.Scan(
			&summary.RunID,
			&summary.RunOptimize,
			&summary.RunDir,
			&summary.ID,
			&summary.Name,
			&summary.Status,
			&summary.Round,
			&summary.Agent,
			&summary.Model,
			&summary.Score,
			&scoreable,
			&summary.ScoreReason,
			&summary.PrimaryMetricMS,
			&summary.P95LatencyMS,
			&summary.BenchmarkNsPerOp,
			&summary.MemoryMetricBytes,
			&summary.WallTimeMS,
			&summary.CPUUserSeconds,
			&summary.CPUSystemSeconds,
			&summary.ExternalCallCount,
			&summary.ExternalPolicyStatus,
			&summary.SourcePath,
		); err != nil {
			return nil, fmt.Errorf("scan indexed candidate: %w", err)
		}
		summary.Scoreable = scoreable != 0
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query indexed candidates: %w", err)
	}
	return summaries, nil
}

func openQueryDB(projectDir string) (*sql.DB, error) {
	if strings.TrimSpace(projectDir) == "" {
		projectDir = "."
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}
	indexPath := IndexPath(absProject)
	if _, err := os.Stat(indexPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("SQLite index is missing: %s; run crucible index first", indexPath)
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", indexPath)
	if err != nil {
		return nil, err
	}
	stale, err := schemaResetRequired(context.Background(), db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if stale {
		_ = db.Close()
		return nil, fmt.Errorf("SQLite index schema is stale: %s; run crucible index first", indexPath)
	}
	return db, nil
}

func appendLimit(query string, limit int) string {
	if limit <= 0 {
		return query
	}
	return query + fmt.Sprintf("\nLIMIT %d", limit)
}
