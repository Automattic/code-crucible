package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Automattic/code-crucible/internal/indexer"
)

func runQuery(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "query requires a kind: runs or candidates\n")
		return 2
	}

	kind := args[0]
	switch kind {
	case "runs":
		return runQueryRuns(args[1:], stdout, stderr)
	case "candidates":
		return runQueryCandidates(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown query kind %q; expected runs or candidates\n", kind)
		return 2
	}
}

func runQueryRuns(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("query runs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	limit := fs.Int("limit", 0, "maximum number of runs to return")
	jsonOut := fs.Bool("json", false, "print run summaries as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *limit < 0 {
		fmt.Fprintf(stderr, "query runs failed: --limit must be at least 0\n")
		return 2
	}

	runs, err := indexer.QueryRuns(indexer.QueryOptions{
		ProjectDir: *projectDir,
		Limit:      *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "query runs failed: %v\n", err)
		return 1
	}
	if *jsonOut {
		return printJSON(stdout, stderr, runs, "query runs")
	}

	rows := make([][]string, 0, len(runs))
	scoreValues := make([]float64, 0, len(runs))
	for _, run := range runs {
		scoreValues = append(scoreValues, run.BestScore)
	}
	scoreFormatter := newDynamicFloatFormatter(scoreValues, 2, 4)
	for _, run := range runs {
		rows = append(rows, []string{
			run.ID,
			shorten(run.Optimize, 48),
			strconv.Itoa(run.Candidates),
			strconv.Itoa(run.Passed),
			run.BestCandidate,
			formatOptionalFloat(scoreFormatter, run.BestScore),
			strconv.Itoa(run.ActiveRound),
			run.ExternalMode,
		})
	}
	printTable(stdout, []string{"Run", "Optimize", "Candidates", "Passed", "Best", "Best Score", "Round", "External"}, rows)
	return 0
}

func runQueryCandidates(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("query candidates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID filter")
	status := fs.String("status", "", "status filter, such as passed or failed")
	limit := fs.Int("limit", 0, "maximum number of candidates to return")
	jsonOut := fs.Bool("json", false, "print candidate summaries as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *limit < 0 {
		fmt.Fprintf(stderr, "query candidates failed: --limit must be at least 0\n")
		return 2
	}

	candidates, err := indexer.QueryCandidates(indexer.QueryOptions{
		ProjectDir: *projectDir,
		RunID:      *runID,
		Status:     *status,
		Limit:      *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "query candidates failed: %v\n", err)
		return 1
	}
	if *jsonOut {
		return printJSON(stdout, stderr, candidates, "query candidates")
	}

	scoreValues := make([]float64, 0, len(candidates))
	primaryValues := make([]float64, 0, len(candidates))
	for _, candidate := range candidates {
		scoreValues = append(scoreValues, candidate.Score)
		primaryValues = append(primaryValues, candidate.PrimaryMetricMS)
	}
	scoreFormatter := newDynamicFloatFormatter(scoreValues, 2, 4)
	primaryFormatter := newDynamicFloatFormatter(primaryValues, 2, 4)

	rows := make([][]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, []string{
			candidate.RunID,
			candidate.ID,
			candidate.Status,
			scoreFormatter.format(candidate.Score),
			formatOptionalFloat(primaryFormatter, candidate.PrimaryMetricMS),
			formatBytes(int64(candidate.MemoryMetricBytes)),
			strconv.Itoa(candidate.ExternalCallCount),
			candidate.ExternalPolicyStatus,
		})
	}
	printTable(stdout, []string{"Run", "Candidate", "Status", "Score", "Primary ms", "Memory", "Ext Calls", "Policy"}, rows)
	return 0
}

func printJSON(stdout, stderr io.Writer, value any, label string) int {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "%s failed: %v\n", label, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\n", data)
	return 0
}

func shorten(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
