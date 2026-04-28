package indexer

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/run"
)

func TestRebuildCreatesSQLiteIndexFromRunArchive(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "ranking")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package ranking\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "ranking/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}

	boardPath := filepath.Join(created.RunDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(boardPath)
	if err != nil {
		t.Fatal(err)
	}
	board.Results[0].Status = "passed"
	board.Results[0].Metrics = model.Metrics{
		P95LatencyMS:     8,
		BenchmarkNsPerOp: 8_000_000,
		MemoryPeakBytes:  4096,
	}
	board.Results[0].Verdict = model.Verdict{
		CorrectnessPassed:    true,
		BenchmarkPassed:      true,
		ExternalPolicyPassed: true,
	}
	board.Results = append(board.Results, model.CandidateResult{
		Candidate: model.Candidate{
			ID:         "candidate-0001",
			Name:       "faster ranking",
			Round:      1,
			ParentIDs:  []string{"candidate-0000-baseline"},
			Agent:      "codex",
			Model:      "test-model",
			SourcePath: "round-0001/candidate-0001/src",
			CreatedAt:  time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC),
		},
		Metrics: model.Metrics{
			RuntimeMeanMS:     3,
			P95LatencyMS:      4,
			BenchmarkNsPerOp:  4_000_000,
			BenchmarkRuns:     20,
			MemoryPeakBytes:   2048,
			CPUUserSeconds:    1.5,
			CPUSystemSeconds:  0.25,
			ExternalCallCount: 2,
		},
		External: model.ExternalCallTrace{
			Mode:         model.ExternalModeDeny,
			RequestCount: 1,
			PolicyPassed: true,
			PolicyEnforcement: &model.ExternalPolicyEnforcement{
				Mode:   model.ExternalModeDeny,
				Status: "passed",
			},
		},
		Verdict: model.Verdict{
			CorrectnessPassed:    true,
			BenchmarkPassed:      true,
			ExternalPolicyPassed: true,
		},
		Score: 1000,
		ScoreExplanation: &model.ScoreExplanation{
			Scoreable: true,
		},
		Status: "passed",
	})
	if err := archive.SaveJSON(boardPath, board); err != nil {
		t.Fatal(err)
	}

	report, err := Rebuild(Options{ProjectDir: projectDir})
	if err != nil {
		t.Fatal(err)
	}
	if report.Runs != 1 || report.Candidates != 2 {
		t.Fatalf("report counts = runs %d candidates %d, want 1 and 2", report.Runs, report.Candidates)
	}
	if report.IndexPath != filepath.Join(projectDir, ".crucible", "index.sqlite") {
		t.Fatalf("index path = %q", report.IndexPath)
	}
	if report.Schema != schemaVersion || report.SchemaReset {
		t.Fatalf("schema report = version %d reset %v, want version %d without reset", report.Schema, report.SchemaReset, schemaVersion)
	}

	db, err := sql.Open("sqlite", report.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var optimize string
	var activeRound int
	if err := db.QueryRow("SELECT optimize, active_round FROM runs WHERE id = ?", created.ID).Scan(&optimize, &activeRound); err != nil {
		t.Fatal(err)
	}
	if optimize != "make ranking faster" || activeRound != 1 {
		t.Fatalf("indexed run = %q round %d", optimize, activeRound)
	}

	var status string
	var score float64
	var p95 float64
	var externalCalls int
	var policyStatus string
	if err := db.QueryRow(`
SELECT status, score, p95_latency_ms, external_call_count, external_policy_status
FROM candidates
WHERE run_id = ? AND id = 'candidate-0001'
`, created.ID).Scan(&status, &score, &p95, &externalCalls, &policyStatus); err != nil {
		t.Fatal(err)
	}
	if status != "passed" || score != 1000 || p95 != 4 || externalCalls != 2 || policyStatus != "passed" {
		t.Fatalf("indexed candidate = status %q score %f p95 %f calls %d policy %q", status, score, p95, externalCalls, policyStatus)
	}

	var schema string
	if err := db.QueryRow("SELECT value FROM index_meta WHERE key = 'schema_version'").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if schema != strconv.Itoa(schemaVersion) {
		t.Fatalf("schema_version = %q, want %d", schema, schemaVersion)
	}
}

func TestRebuildResetsStaleSQLiteSchema(t *testing.T) {
	projectDir := t.TempDir()
	sourceDir := filepath.Join(projectDir, "ranking")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "rank.go"), []byte("package ranking\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := run.Create(run.Options{
		ProjectDir:   projectDir,
		Optimize:     "make ranking faster",
		SourcePath:   "ranking/rank.go",
		Variants:     1,
		ExternalMode: "deny",
	})
	if err != nil {
		t.Fatal(err)
	}

	indexPath := IndexPath(projectDir)
	db, err := sql.Open("sqlite", indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE index_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO index_meta(key, value) VALUES ('schema_version', '0');
CREATE TABLE runs (id TEXT PRIMARY KEY);
INSERT INTO runs(id) VALUES ('stale-run');
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := Rebuild(Options{ProjectDir: projectDir})
	if err != nil {
		t.Fatal(err)
	}
	if !report.SchemaReset || report.RebuildMode != "schema-reset" {
		t.Fatalf("schema reset report = reset %v mode %q", report.SchemaReset, report.RebuildMode)
	}
	if report.Schema != schemaVersion || report.Runs != 1 || report.Candidates != 1 {
		t.Fatalf("report = schema %d runs %d candidates %d", report.Schema, report.Runs, report.Candidates)
	}

	db, err = sql.Open("sqlite", indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var schema string
	if err := db.QueryRow("SELECT value FROM index_meta WHERE key = 'schema_version'").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if schema != strconv.Itoa(schemaVersion) {
		t.Fatalf("schema_version = %q, want %d", schema, schemaVersion)
	}

	var optimize string
	if err := db.QueryRow("SELECT optimize FROM runs WHERE id = ?", created.ID).Scan(&optimize); err != nil {
		t.Fatal(err)
	}
	if optimize != "make ranking faster" {
		t.Fatalf("indexed optimize = %q", optimize)
	}
}
