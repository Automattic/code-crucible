package cli

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/scoring"
)

type tuiDashboardData struct {
	ProjectDir  string
	RunDir      string
	Config      model.RunConfig
	Leaderboard model.Leaderboard
	Results     []model.CandidateResult
}

type tuiDashboardModel struct {
	data     tuiDashboardData
	selected int
	width    int
	height   int
}

func runTUI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := projectDirFlag(fs, "project directory containing .crucible")
	runID := fs.String("run", "latest", "run ID; defaults to latest run")
	noAltScreen := fs.Bool("no-alt-screen", false, "render inline instead of using the terminal alternate screen")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if stdin == nil {
		stdin = os.Stdin
	}

	data, err := loadTUIDashboard(*projectDir, *runID)
	if err != nil {
		fmt.Fprintf(stderr, "tui failed: %v\n", err)
		return 1
	}

	programOptions := []tea.ProgramOption{
		tea.WithInput(stdin),
		tea.WithOutput(stdout),
	}
	if !*noAltScreen {
		programOptions = append(programOptions, tea.WithAltScreen())
	}
	program := tea.NewProgram(newTUIDashboardModel(data), programOptions...)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(stderr, "tui failed: %v\n", err)
		return 1
	}
	return 0
}

func loadTUIDashboard(projectDir, runSelector string) (tuiDashboardData, error) {
	if strings.TrimSpace(projectDir) == "" {
		projectDir = "."
	}
	absProject, err := filepath.Abs(projectDir)
	if err != nil {
		return tuiDashboardData{}, err
	}
	configPath, err := archive.RunConfigPath(absProject, runSelector)
	if err != nil {
		return tuiDashboardData{}, err
	}
	cfg, err := archive.LoadRunConfig(configPath)
	if err != nil {
		return tuiDashboardData{}, err
	}
	runDir := cfg.RunDir
	if strings.TrimSpace(runDir) == "" {
		runDir = filepath.Dir(configPath)
	} else {
		runDir = archive.ProjectPath(absProject, runDir)
	}
	leaderboardPath := filepath.Join(runDir, "leaderboard.json")
	board, err := archive.LoadLeaderboard(leaderboardPath)
	if err != nil {
		return tuiDashboardData{}, err
	}
	return tuiDashboardData{
		ProjectDir:  absProject,
		RunDir:      runDir,
		Config:      *cfg,
		Leaderboard: *board,
		Results:     rankedResults(board.Results),
	}, nil
}

func newTUIDashboardModel(data tuiDashboardData) tuiDashboardModel {
	return tuiDashboardModel{
		data:  data,
		width: 100,
	}
}

func (m tuiDashboardModel) Init() tea.Cmd {
	return nil
}

func (m tuiDashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < len(m.data.Results)-1 {
				m.selected++
			}
		case "home":
			m.selected = 0
		case "end":
			if len(m.data.Results) > 0 {
				m.selected = len(m.data.Results) - 1
			}
		}
	}
	return m, nil
}

func (m tuiDashboardModel) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Code Crucible\n")
	fmt.Fprintf(&b, "Project: %s\n", m.data.ProjectDir)
	fmt.Fprintf(&b, "Run: %s\n", m.data.Config.ID)
	fmt.Fprintf(&b, "Created: %s\n", m.data.Config.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "Optimize: %s\n", shorten(m.data.Config.Optimize, maxInt(24, width-10)))
	fmt.Fprintf(&b, "Agent: %s  Round: %s  Variants: %d  External: %s\n",
		displayValue(m.data.Config.Agent),
		filepath.Base(filepath.FromSlash(m.data.Config.RoundDir)),
		m.data.Config.Variants,
		displayValue(string(m.data.Config.External.Mode)),
	)
	passed, failed, pending := tuiStatusCounts(m.data.Results)
	fmt.Fprintf(&b, "Candidates: %d  Passed: %d  Failed: %d  Pending: %d\n\n", len(m.data.Results), passed, failed, pending)

	if len(m.data.Results) == 0 {
		b.WriteString("No candidates are archived for this run.\n\n")
	} else {
		b.WriteString("Leaderboard\n")
		b.WriteString(m.leaderboardTable())
		b.WriteString("\n")
		b.WriteString(m.candidateDetail())
		b.WriteString("\n")
	}

	b.WriteString("Next Actions\n")
	for _, action := range m.nextActions() {
		fmt.Fprintf(&b, "%s\n", action)
	}
	b.WriteString("\nKeys: j/down select next, k/up select previous, q quit\n")
	return b.String()
}

func (m tuiDashboardModel) leaderboardTable() string {
	scoreValues := make([]float64, 0, len(m.data.Results))
	p95Values := make([]float64, 0, len(m.data.Results))
	nsPerOpValues := make([]float64, 0, len(m.data.Results))
	for _, result := range m.data.Results {
		scoreValues = append(scoreValues, result.Score)
		p95Values = append(p95Values, result.Metrics.P95LatencyMS)
		nsPerOpValues = append(nsPerOpValues, result.Metrics.BenchmarkNsPerOp)
	}
	scoreFormatter := newDynamicFloatFormatter(scoreValues, 2, 4)
	p95Formatter := newDynamicFloatFormatter(p95Values, 2, 6)
	nsPerOpFormatter := newDynamicFloatFormatter(nsPerOpValues, 0, 4)
	baselinePrimary := leaderboardBaselinePrimary(m.data.Results)
	baselineMemory := leaderboardBaselineMemory(m.data.Results)
	showExternalCalls := leaderboardHasExternalCalls(m.data.Results)

	headers := []string{"", "Rank", "Candidate", "Status", "Score", "P95 ms", "ns/op", "Speedup", "Memory", "Mem/Base"}
	if showExternalCalls {
		headers = append(headers, "Ext Calls")
	}
	rows := make([][]string, 0, len(m.data.Results))
	for i, result := range m.data.Results {
		marker := " "
		if i == m.selected {
			marker = ">"
		}
		rank := "-"
		if result.Status == "passed" {
			rank = strconv.Itoa(i + 1)
		}
		row := []string{
			marker,
			rank,
			result.Candidate.ID,
			result.Status,
			scoreFormatter.format(result.Score),
			formatOptionalFloat(p95Formatter, result.Metrics.P95LatencyMS),
			formatOptionalFloat(nsPerOpFormatter, result.Metrics.BenchmarkNsPerOp),
			formatSpeedup(baselinePrimary, scoring.PrimaryMetric(result.Metrics)),
			formatBytes(result.Metrics.MemoryPeakBytes),
			formatRelativeUsage(scoring.MemoryMetric(result.Metrics), baselineMemory),
		}
		if showExternalCalls {
			row = append(row, strconv.Itoa(leaderboardExternalCallCount(result)))
		}
		rows = append(rows, row)
	}
	var b strings.Builder
	printTable(&b, headers, rows)
	return b.String()
}

func (m tuiDashboardModel) candidateDetail() string {
	if len(m.data.Results) == 0 {
		return ""
	}
	result := m.data.Results[m.selected]
	scoreFormatter := newDynamicFloatFormatter([]float64{result.Score}, 2, 4)
	p95Formatter := newDynamicFloatFormatter([]float64{result.Metrics.P95LatencyMS}, 2, 6)
	nsPerOpFormatter := newDynamicFloatFormatter([]float64{result.Metrics.BenchmarkNsPerOp}, 0, 4)

	var b strings.Builder
	fmt.Fprintf(&b, "Candidate Detail\n")
	fmt.Fprintf(&b, "ID: %s  Status: %s  Score: %s\n", result.Candidate.ID, result.Status, scoreFormatter.format(result.Score))
	fmt.Fprintf(&b, "Name: %s\n", displayValue(result.Candidate.Name))
	fmt.Fprintf(&b, "Round: %d  Agent: %s  Model: %s\n", result.Candidate.Round, displayValue(result.Candidate.Agent), displayValue(result.Candidate.Model))
	fmt.Fprintf(&b, "Source: %s\n", displayValue(result.Candidate.SourcePath))
	fmt.Fprintf(&b, "P95: %s ms  ns/op: %s  Memory: %s  Eval CPU: %s s\n",
		formatOptionalFloat(p95Formatter, result.Metrics.P95LatencyMS),
		formatOptionalFloat(nsPerOpFormatter, result.Metrics.BenchmarkNsPerOp),
		formatBytes(result.Metrics.MemoryPeakBytes),
		formatOptionalCPU(result.Metrics.CPUUserSeconds+result.Metrics.CPUSystemSeconds),
	)
	fmt.Fprintf(&b, "External: %d calls  Policy: %s\n", leaderboardExternalCallCount(result), displayValue(externalPolicyStatus(result)))
	if result.ScoreExplanation != nil && strings.TrimSpace(result.ScoreExplanation.Reason) != "" {
		fmt.Fprintf(&b, "Score note: %s\n", result.ScoreExplanation.Reason)
	}
	if len(result.Verdict.Errors) > 0 {
		fmt.Fprintf(&b, "Error: %s\n", result.Verdict.Errors[0])
	}
	if len(result.Verdict.Warnings) > 0 {
		fmt.Fprintf(&b, "Warning: %s\n", result.Verdict.Warnings[0])
	}
	if len(result.Verdict.Notes) > 0 {
		fmt.Fprintf(&b, "Note: %s\n", result.Verdict.Notes[0])
	}
	return b.String()
}

func (m tuiDashboardModel) nextActions() []string {
	runID := m.data.Config.ID
	project := shellQuote(m.data.ProjectDir)
	actions := []string{
		fmt.Sprintf("Generate competitors: crucible generate --project-dir %s --run %s", project, shellQuote(runID)),
		fmt.Sprintf("Evaluate candidates:  crucible evaluate --project-dir %s --run %s", project, shellQuote(runID)),
		fmt.Sprintf("Open leaderboard:     crucible leaderboard --project-dir %s --run %s", project, shellQuote(runID)),
		fmt.Sprintf("Write HTML report:    crucible report --project-dir %s --run %s", project, shellQuote(runID)),
		fmt.Sprintf("Prepare next round:   crucible next-round --project-dir %s --run %s", project, shellQuote(runID)),
	}
	if len(m.data.Results) > 0 {
		candidateID := m.data.Results[m.selected].Candidate.ID
		actions = append(actions, fmt.Sprintf("Inspect selected:     crucible inspect --project-dir %s --run %s %s", project, shellQuote(runID), shellQuote(candidateID)))
	}
	return actions
}

func tuiStatusCounts(results []model.CandidateResult) (passed, failed, pending int) {
	for _, result := range results {
		switch result.Status {
		case "passed":
			passed++
		case "failed":
			failed++
		default:
			pending++
		}
	}
	return passed, failed, pending
}

func externalPolicyStatus(result model.CandidateResult) string {
	if result.External.PolicyEnforcement != nil && strings.TrimSpace(result.External.PolicyEnforcement.Status) != "" {
		return result.External.PolicyEnforcement.Status
	}
	if result.External.PolicyPassed {
		return "passed"
	}
	if result.External.Mode != "" {
		return string(result.External.Mode)
	}
	return ""
}

func displayValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func formatOptionalCPU(value float64) string {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "-"
	}
	return newDynamicFloatFormatter([]float64{value}, 2, 4).format(value)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n'\"\\$&;()<>|*?![]{}") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
