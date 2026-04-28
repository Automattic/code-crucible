package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Automattic/code-crucible/internal/run"
)

func printAdoptionReport(stdout, stderr io.Writer, report *run.AdoptionReport) {
	fmt.Fprintf(stdout, "\nAdoption report for run %s\n", report.RunID)
	fmt.Fprintf(stdout, "Leaderboard: %s\n", report.LeaderboardPath)
	fmt.Fprintf(stdout, "Added: %s\n", formatIDList(report.Added))
	fmt.Fprintf(stdout, "Already present: %s\n", formatIDList(report.Existing))
	if len(report.Invalid) == 0 {
		fmt.Fprintln(stdout, "Invalid: none")
		return
	}

	fmt.Fprintf(stderr, "Invalid generated candidates:\n")
	for _, issue := range report.Invalid {
		fmt.Fprintf(stderr, "- %s: %s (%s)\n", issue.ID, issue.Reason, issue.Path)
	}
}

func formatIDList(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

func printEvaluationReport(stdout io.Writer, report *run.EvaluationReport) {
	fmt.Fprintf(stdout, "\nEvaluation report for run %s\n", report.RunID)
	fmt.Fprintf(stdout, "Evaluator: %s\n", report.EvaluatorPath)
	fmt.Fprintf(stdout, "Leaderboard: %s\n", report.LeaderboardPath)
	if len(report.Results) == 0 {
		fmt.Fprintln(stdout, "No candidates evaluated.")
		return
	}

	scoreValues := make([]float64, 0, len(report.Results))
	for _, result := range report.Results {
		scoreValues = append(scoreValues, result.Score)
	}
	scoreFormatter := newDynamicFloatFormatter(scoreValues, 2, 4)
	rows := make([][]string, 0, len(report.Results))
	for _, result := range report.Results {
		rows = append(rows, []string{
			result.ID,
			result.Status,
			scoreFormatter.format(result.Score),
		})
	}
	printTable(stdout, []string{"Candidate", "Status", "Score"}, rows)
}
