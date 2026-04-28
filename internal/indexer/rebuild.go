package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/project"
	"github.com/Automattic/code-crucible/internal/scoring"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type Options struct {
	ProjectDir string
	RunID      string
}

type Report struct {
	ProjectDir  string `json:"project_dir"`
	IndexPath   string `json:"index_path"`
	RunID       string `json:"run_id,omitempty"`
	Runs        int    `json:"runs"`
	Candidates  int    `json:"candidates"`
	Schema      int    `json:"schema"`
	RebuildMode string `json:"rebuild_mode"`
	SchemaReset bool   `json:"schema_reset,omitempty"`
}

func IndexPath(projectDir string) string {
	return filepath.Join(project.WorkDir(projectDir), "index.sqlite")
}

func Rebuild(opts Options) (*Report, error) {
	if strings.TrimSpace(opts.ProjectDir) == "" {
		opts.ProjectDir = "."
	}
	absProject, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	if _, err := project.Ensure(absProject); err != nil {
		return nil, err
	}

	indexPath := IndexPath(absProject)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return nil, err
	}

	ctx := context.Background()
	requestedRunID := strings.TrimSpace(opts.RunID)
	if requestedRunID != "" {
		if _, err := collectRunDirs(absProject, requestedRunID); err != nil {
			return nil, err
		}
	}

	db, schemaReset, err := openIndex(ctx, indexPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	collectionRunID := requestedRunID
	rebuildMode := "all"
	if collectionRunID != "" {
		rebuildMode = "run"
	}
	if schemaReset {
		collectionRunID = ""
		rebuildMode = "schema-reset"
	}
	runDirs, err := collectRunDirs(absProject, collectionRunID)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := clearIndexedRows(ctx, tx, collectionRunID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO index_meta(key, value) VALUES
  ('schema_version', ?),
  ('project_dir', ?),
  ('rebuilt_at', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
`, strconv.Itoa(schemaVersion), absProject, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, fmt.Errorf("update index metadata: %w", err)
	}

	report := &Report{
		ProjectDir:  absProject,
		IndexPath:   indexPath,
		RunID:       requestedRunID,
		Schema:      schemaVersion,
		RebuildMode: rebuildMode,
		SchemaReset: schemaReset,
	}

	for _, runDir := range runDirs {
		candidates, err := indexRun(ctx, tx, absProject, runDir)
		if err != nil {
			return nil, err
		}
		report.Runs++
		report.Candidates += candidates
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit SQLite index: %w", err)
	}
	return report, nil
}

func collectRunDirs(projectDir, runID string) ([]string, error) {
	runsDir := filepath.Join(project.WorkDir(projectDir), "runs")
	runID = strings.TrimSpace(runID)
	if runID != "" {
		runDir := filepath.Join(runsDir, runID)
		if _, err := os.Stat(filepath.Join(runDir, "run.json")); err != nil {
			return nil, err
		}
		return []string{runDir}, nil
	}

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	runDirs := make([]string, 0, len(names))
	for _, name := range names {
		runDirs = append(runDirs, filepath.Join(runsDir, name))
	}
	return runDirs, nil
}

func openIndex(ctx context.Context, indexPath string) (*sql.DB, bool, error) {
	db, err := sql.Open("sqlite", indexPath)
	if err != nil {
		return nil, false, err
	}

	schemaReset, err := schemaResetRequired(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, false, err
	}
	if schemaReset {
		_ = db.Close()
		if err := removeIndexFiles(indexPath); err != nil {
			return nil, false, err
		}
		db, err = sql.Open("sqlite", indexPath)
		if err != nil {
			return nil, false, err
		}
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, false, fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	if err := applySchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, false, err
	}
	return db, schemaReset, nil
}

func schemaResetRequired(ctx context.Context, db *sql.DB) (bool, error) {
	tables, err := userTables(ctx, db)
	if err != nil {
		return true, nil
	}
	if len(tables) == 0 {
		return false, nil
	}
	if !tables["index_meta"] {
		return true, nil
	}

	var version string
	err = db.QueryRowContext(ctx, "SELECT value FROM index_meta WHERE key = 'schema_version'").Scan(&version)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return true, nil
	}
	return strings.TrimSpace(version) != strconv.Itoa(schemaVersion), nil
}

func userTables(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
SELECT name
FROM sqlite_master
WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tables, nil
}

func removeIndexFiles(indexPath string) error {
	for _, path := range []string{indexPath, indexPath + "-wal", indexPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale SQLite index %s: %w", path, err)
		}
	}
	return nil
}

func applySchema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS index_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			optimize TEXT NOT NULL,
			project_dir TEXT NOT NULL,
			run_dir TEXT NOT NULL,
			round_dir TEXT NOT NULL,
			active_round INTEGER NOT NULL,
			target_path TEXT NOT NULL,
			agent TEXT NOT NULL,
			variants INTEGER NOT NULL,
			rounds INTEGER NOT NULL,
			exploration REAL NOT NULL,
			evaluator TEXT NOT NULL,
			evaluator_script TEXT NOT NULL,
			external_mode TEXT NOT NULL,
			external_fixtures TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS candidates (
			run_id TEXT NOT NULL,
			id TEXT NOT NULL,
			name TEXT NOT NULL,
			round INTEGER NOT NULL,
			parent_ids TEXT NOT NULL,
			agent TEXT NOT NULL,
			model TEXT NOT NULL,
			source_path TEXT NOT NULL,
			baseline INTEGER NOT NULL,
			status TEXT NOT NULL,
			score REAL NOT NULL,
			scoreable INTEGER NOT NULL,
			score_reason TEXT NOT NULL,
			primary_metric_ms REAL NOT NULL,
			runtime_mean_ms REAL NOT NULL,
			p95_latency_ms REAL NOT NULL,
			benchmark_ns_per_op REAL NOT NULL,
			benchmark_runs INTEGER NOT NULL,
			memory_metric_bytes REAL NOT NULL,
			memory_peak_bytes INTEGER NOT NULL,
			max_rss_bytes INTEGER NOT NULL,
			wall_time_ms REAL NOT NULL,
			cpu_user_seconds REAL NOT NULL,
			cpu_system_seconds REAL NOT NULL,
			cpu_percent REAL NOT NULL,
			external_call_count INTEGER NOT NULL,
			external_mode TEXT NOT NULL,
			external_policy_status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(run_id, id),
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS candidates_run_score_idx
			ON candidates(run_id, status, score DESC, p95_latency_ms ASC)`,
		`CREATE INDEX IF NOT EXISTS candidates_status_idx
			ON candidates(status)`,
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply SQLite schema: %w", err)
		}
	}
	return nil
}

func clearIndexedRows(ctx context.Context, tx *sql.Tx, runID string) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM candidates"); err != nil {
			return fmt.Errorf("clear indexed candidates: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM runs"); err != nil {
			return fmt.Errorf("clear indexed runs: %w", err)
		}
		return nil
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM candidates WHERE run_id = ?", runID); err != nil {
		return fmt.Errorf("clear indexed candidates for %s: %w", runID, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM runs WHERE id = ?", runID); err != nil {
		return fmt.Errorf("clear indexed run %s: %w", runID, err)
	}
	return nil
}

func indexRun(ctx context.Context, tx *sql.Tx, projectDir, runDir string) (int, error) {
	cfg, err := archive.LoadRunConfig(filepath.Join(runDir, "run.json"))
	if err != nil {
		return 0, fmt.Errorf("load run config %s: %w", runDir, err)
	}
	board, err := archive.LoadLeaderboard(filepath.Join(runDir, "leaderboard.json"))
	if err != nil {
		return 0, fmt.Errorf("load leaderboard %s: %w", runDir, err)
	}

	runID := firstNonEmpty(cfg.ID, board.RunID, filepath.Base(runDir))
	optimize := firstNonEmpty(cfg.Optimize, board.Optimize)
	createdAt := cfg.CreatedAt
	if createdAt.IsZero() {
		createdAt = board.CreatedAt
	}
	runPath := archive.ProjectPath(projectDir, cfg.RunDir)
	if runPath == "" {
		runPath = runDir
	}
	roundPath := archive.ProjectPath(projectDir, cfg.RoundDir)
	if roundPath == "" {
		roundPath = cfg.RoundDir
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO runs (
	id, optimize, project_dir, run_dir, round_dir, active_round, target_path, agent,
	variants, rounds, exploration, evaluator, evaluator_script, external_mode,
	external_fixtures, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, runID, optimize, projectDir, filepath.ToSlash(runPath), filepath.ToSlash(roundPath), roundNumber(cfg.RoundDir), cfg.TargetPath,
		cfg.Agent, cfg.Variants, cfg.Rounds, cfg.Exploration, cfg.Evaluator, cfg.EvaluatorScript,
		string(cfg.External.Mode), cfg.External.Fixtures, formatTime(createdAt)); err != nil {
		return 0, fmt.Errorf("index run %s: %w", runID, err)
	}

	for _, result := range board.Results {
		scoreable, reason := scoreDetails(result)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO candidates (
	run_id, id, name, round, parent_ids, agent, model, source_path, baseline, status,
	score, scoreable, score_reason, primary_metric_ms, runtime_mean_ms, p95_latency_ms,
	benchmark_ns_per_op, benchmark_runs, memory_metric_bytes, memory_peak_bytes,
	max_rss_bytes, wall_time_ms, cpu_user_seconds, cpu_system_seconds, cpu_percent,
	external_call_count, external_mode, external_policy_status, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, runID, result.Candidate.ID, result.Candidate.Name, result.Candidate.Round,
			strings.Join(result.Candidate.ParentIDs, ","), result.Candidate.Agent, result.Candidate.Model,
			result.Candidate.SourcePath, boolInt(result.Candidate.Baseline), result.Status, result.Score,
			boolInt(scoreable), reason, scoring.PrimaryMetric(result.Metrics), result.Metrics.RuntimeMeanMS,
			result.Metrics.P95LatencyMS, result.Metrics.BenchmarkNsPerOp, result.Metrics.BenchmarkRuns,
			scoring.MemoryMetric(result.Metrics), result.Metrics.MemoryPeakBytes, result.Metrics.MaxRSSBytes,
			result.Metrics.WallTimeMS, result.Metrics.CPUUserSeconds, result.Metrics.CPUSystemSeconds,
			result.Metrics.CPUPercent, externalCallCount(result), string(result.External.Mode),
			policyStatus(result.External.PolicyEnforcement), formatTime(result.Candidate.CreatedAt)); err != nil {
			return 0, fmt.Errorf("index candidate %s/%s: %w", runID, result.Candidate.ID, err)
		}
	}
	return len(board.Results), nil
}

func scoreDetails(result model.CandidateResult) (bool, string) {
	if result.ScoreExplanation == nil {
		return false, ""
	}
	return result.ScoreExplanation.Scoreable, result.ScoreExplanation.Reason
}

func externalCallCount(result model.CandidateResult) int {
	if result.Metrics.ExternalCallCount > 0 {
		return result.Metrics.ExternalCallCount
	}
	return result.External.RequestCount
}

func policyStatus(enforcement *model.ExternalPolicyEnforcement) string {
	if enforcement == nil {
		return ""
	}
	return enforcement.Status
}

func roundNumber(path string) int {
	base := filepath.Base(filepath.Clean(filepath.FromSlash(path)))
	var round int
	if _, err := fmt.Sscanf(base, "round-%d", &round); err == nil {
		return round
	}
	return 0
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
