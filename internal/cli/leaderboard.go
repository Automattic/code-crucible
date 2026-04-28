package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/scoring"
)

func runLeaderboard(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("leaderboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "", "run ID; defaults to latest run")
	jsonOut := fs.Bool("json", false, "print raw leaderboard JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path, err := archive.LeaderboardPath(*projectDir, *runID)
	if err != nil {
		fmt.Fprintf(stderr, "leaderboard failed: %v\n", err)
		return 1
	}
	board, err := archive.LoadLeaderboard(path)
	if err != nil {
		fmt.Fprintf(stderr, "leaderboard failed: %v\n", err)
		return 1
	}

	if *jsonOut {
		data, err := json.MarshalIndent(board, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "leaderboard failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}

	fmt.Fprintf(stdout, "Run: %s\n", board.RunID)
	fmt.Fprintf(stdout, "Optimize: %s\n\n", board.Optimize)
	printLeaderboardTable(stdout, rankedResults(board.Results))
	return 0
}

func rankedResults(results []model.CandidateResult) []model.CandidateResult {
	ranked := append([]model.CandidateResult(nil), results...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left := ranked[i]
		right := ranked[j]
		leftPriority := leaderboardStatusPriority(left.Status)
		rightPriority := leaderboardStatusPriority(right.Status)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if left.Metrics.P95LatencyMS != right.Metrics.P95LatencyMS {
			return left.Metrics.P95LatencyMS < right.Metrics.P95LatencyMS
		}
		return left.Candidate.ID < right.Candidate.ID
	})
	return ranked
}

func leaderboardStatusPriority(status string) int {
	switch status {
	case "passed":
		return 0
	case "failed":
		return 1
	case "generated":
		return 2
	case model.CandidateStatusNeedsEvaluator:
		return 3
	case "pending":
		return 4
	default:
		return 5
	}
}

func printLeaderboardTable(stdout io.Writer, results []model.CandidateResult) {
	scoreValues := make([]float64, 0, len(results))
	p95Values := make([]float64, 0, len(results))
	nsPerOpValues := make([]float64, 0, len(results))
	cpuValues := make([]float64, 0, len(results))
	for _, result := range results {
		scoreValues = append(scoreValues, result.Score)
		p95Values = append(p95Values, result.Metrics.P95LatencyMS)
		nsPerOpValues = append(nsPerOpValues, result.Metrics.BenchmarkNsPerOp)
		cpuValues = append(cpuValues, result.Metrics.CPUUserSeconds+result.Metrics.CPUSystemSeconds)
	}

	scoreFormatter := newDynamicFloatFormatter(scoreValues, 2, 4)
	p95Formatter := newDynamicFloatFormatter(p95Values, 2, 6)
	nsPerOpFormatter := newDynamicFloatFormatter(nsPerOpValues, 0, 4)
	cpuFormatter := newDynamicFloatFormatter(cpuValues, 2, 4)
	baselinePrimary := leaderboardBaselinePrimary(results)
	baselineMemory := leaderboardBaselineMemory(results)
	showExternalCalls := leaderboardHasExternalCalls(results)

	rows := make([][]string, 0, len(results))
	for i, result := range results {
		rank := "-"
		if result.Status == "passed" {
			rank = fmt.Sprintf("%d", i+1)
		}
		row := []string{
			rank,
			result.Candidate.ID,
			result.Status,
			scoreFormatter.format(result.Score),
			formatOptionalFloat(p95Formatter, result.Metrics.P95LatencyMS),
			formatOptionalFloat(nsPerOpFormatter, result.Metrics.BenchmarkNsPerOp),
			formatSpeedup(baselinePrimary, scoring.PrimaryMetric(result.Metrics)),
			formatBytes(result.Metrics.MemoryPeakBytes),
			formatRelativeUsage(scoring.MemoryMetric(result.Metrics), baselineMemory),
			cpuFormatter.format(result.Metrics.CPUUserSeconds + result.Metrics.CPUSystemSeconds),
		}
		if showExternalCalls {
			row = append(row, fmt.Sprintf("%d", leaderboardExternalCallCount(result)))
		}
		rows = append(rows, row)
	}

	headers := []string{"Rank", "Candidate", "Status", "Score", "P95 ms", "ns/op", "Speedup", "Memory", "Mem/Base", "Eval CPU s"}
	if showExternalCalls {
		headers = append(headers, "Ext Calls")
	}
	printTable(stdout, headers, rows)
}

func leaderboardBaselinePrimary(results []model.CandidateResult) float64 {
	for _, result := range results {
		if result.Candidate.Baseline {
			return scoring.PrimaryMetric(result.Metrics)
		}
	}
	return 0
}

func leaderboardBaselineMemory(results []model.CandidateResult) float64 {
	for _, result := range results {
		if result.Candidate.Baseline {
			return scoring.MemoryMetric(result.Metrics)
		}
	}
	return 0
}

func leaderboardHasExternalCalls(results []model.CandidateResult) bool {
	for _, result := range results {
		if leaderboardExternalCallCount(result) > 0 {
			return true
		}
	}
	return false
}

func leaderboardExternalCallCount(result model.CandidateResult) int {
	if result.Metrics.ExternalCallCount > 0 {
		return result.Metrics.ExternalCallCount
	}
	return result.External.RequestCount
}

type dynamicFloatFormatter struct {
	decimals   int
	scientific bool
}

func newDynamicFloatFormatter(values []float64, defaultDecimals, maxDecimals int) dynamicFloatFormatter {
	if maxDecimals < defaultDecimals {
		maxDecimals = defaultDecimals
	}

	decimals := defaultDecimals
	finite := finiteAbsValues(values)
	if len(finite) == 0 {
		return dynamicFloatFormatter{decimals: decimals}
	}

	sort.Float64s(finite)
	minPositive := finite[0]
	maxValue := finite[len(finite)-1]
	if maxValue >= 1_000_000 || minPositive < math.Pow10(-maxDecimals) || maxValue/minPositive >= 1_000_000 {
		return dynamicFloatFormatter{decimals: maxDecimals, scientific: true}
	}

	if minPositive < 1 {
		decimals = maxInt(decimals, int(math.Ceil(-math.Log10(minPositive)))+2)
	}
	if minDelta := minDistinctDelta(finite); minDelta > 0 && minDelta < 1 {
		decimals = maxInt(decimals, int(math.Ceil(-math.Log10(minDelta)))+1)
	}
	if decimals > maxDecimals {
		decimals = maxDecimals
	}
	return dynamicFloatFormatter{decimals: decimals}
}

func finiteAbsValues(values []float64) []float64 {
	finite := make([]float64, 0, len(values))
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value == 0 {
			continue
		}
		finite = append(finite, math.Abs(value))
	}
	return finite
}

func minDistinctDelta(sorted []float64) float64 {
	var minDelta float64
	for i := 1; i < len(sorted); i++ {
		delta := sorted[i] - sorted[i-1]
		if delta <= 0 {
			continue
		}
		if minDelta == 0 || delta < minDelta {
			minDelta = delta
		}
	}
	return minDelta
}

func (f dynamicFloatFormatter) format(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "-"
	}
	if f.scientific && value != 0 {
		return fmt.Sprintf("%.*e", maxInt(3, f.decimals), value)
	}
	formatted := fmt.Sprintf("%.*f", f.decimals, value)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "" || formatted == "-0" {
		return "0"
	}
	return formatted
}

func formatOptionalFloat(formatter dynamicFloatFormatter, value float64) string {
	if value <= 0 {
		return "-"
	}
	return formatter.format(value)
}

func formatSpeedup(baseline, value float64) string {
	return formatMultiplier(baseline, value)
}

func formatMultiplier(baseline, value float64) string {
	if baseline <= 0 || value <= 0 || math.IsNaN(baseline) || math.IsNaN(value) || math.IsInf(baseline, 0) || math.IsInf(value, 0) {
		return "-"
	}
	multiplier := baseline / value
	if math.Abs(multiplier-1) < 0.005 {
		return "1x"
	}
	switch {
	case multiplier >= 100:
		return fmt.Sprintf("%.0fx", multiplier)
	case multiplier >= 10:
		return fmt.Sprintf("%.1fx", multiplier)
	default:
		return fmt.Sprintf("%.2fx", multiplier)
	}
}

func formatRelativeUsage(value, baseline float64) string {
	if baseline <= 0 || value <= 0 || math.IsNaN(baseline) || math.IsNaN(value) || math.IsInf(baseline, 0) || math.IsInf(value, 0) {
		return "-"
	}
	ratio := value / baseline
	if math.Abs(ratio-1) < 0.005 {
		return "1x"
	}
	switch {
	case ratio < 0.01:
		return fmt.Sprintf("%.4fx", ratio)
	case ratio < 0.1:
		return fmt.Sprintf("%.3fx", ratio)
	case ratio < 10:
		return fmt.Sprintf("%.2fx", ratio)
	case ratio < 100:
		return fmt.Sprintf("%.1fx", ratio)
	default:
		return fmt.Sprintf("%.0fx", ratio)
	}
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "-"
	}
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	scaled := float64(value)
	unitIndex := -1
	for scaled >= unit && unitIndex < len(units)-1 {
		scaled /= unit
		unitIndex++
	}
	if math.Abs(scaled-math.Round(scaled)) < 0.005 {
		return fmt.Sprintf("%.0f %s", scaled, units[unitIndex])
	}
	if scaled >= 100 {
		return fmt.Sprintf("%.0f %s", scaled, units[unitIndex])
	}
	if scaled >= 10 {
		return fmt.Sprintf("%.1f %s", scaled, units[unitIndex])
	}
	return fmt.Sprintf("%.2f %s", scaled, units[unitIndex])
}

func printTable(stdout io.Writer, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	printTableRow(stdout, widths, headers)
	for _, row := range rows {
		printTableRow(stdout, widths, row)
	}
}

func printTableRow(stdout io.Writer, widths []int, row []string) {
	for i, cell := range row {
		padding := widths[i]
		if i == len(row)-1 {
			fmt.Fprintf(stdout, "%-*s\n", padding, cell)
			return
		}
		fmt.Fprintf(stdout, "%-*s  ", padding, cell)
	}
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
