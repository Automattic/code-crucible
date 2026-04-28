package report

import (
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
	"github.com/Automattic/code-crucible/internal/scoring"
)

type Options struct {
	ProjectDir string
	RunID      string
	OutputPath string
}

type HTMLReport struct {
	RunID      string `json:"run_id"`
	RunDir     string `json:"run_dir"`
	OutputPath string `json:"output_path"`
	Candidates int    `json:"candidates"`
	Passed     int    `json:"passed"`
	Failed     int    `json:"failed"`
	Canceled   int    `json:"canceled"`
	Pending    int    `json:"pending"`
}

type reportPage struct {
	GeneratedAt string
	Run         model.RunConfig
	Board       model.Leaderboard
	RunDir      string
	OutputPath  string
	Summary     reportSummary
	Rows        []candidateRow
}

type reportSummary struct {
	Candidates int
	Passed     int
	Failed     int
	Canceled   int
	Pending    int
	Best       string
	BestScore  string
}

type candidateRow struct {
	Rank          int
	ID            string
	Name          string
	Status        string
	Round         int
	Agent         string
	Model         string
	Score         string
	P95Latency    string
	Benchmark     string
	Speedup       string
	Memory        string
	MemoryBase    string
	EvalCPU       string
	ExternalCalls int
	SourcePath    string
	ScoreReason   string
	Verdict       string
}

func GenerateHTML(opts Options) (*HTMLReport, error) {
	if strings.TrimSpace(opts.ProjectDir) == "" {
		opts.ProjectDir = "."
	}
	absProject, err := filepath.Abs(opts.ProjectDir)
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
	runDir := filepath.Dir(configPath)
	board, err := archive.LoadLeaderboard(filepath.Join(runDir, "leaderboard.json"))
	if err != nil {
		return nil, err
	}

	outputPath := strings.TrimSpace(opts.OutputPath)
	if outputPath == "" {
		outputPath = filepath.Join(runDir, "reports", "leaderboard.html")
	} else if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(absProject, outputPath)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, err
	}

	page := buildReportPage(*cfg, *board, runDir, outputPath)
	file, err := os.Create(outputPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if err := reportTemplate.Execute(file, page); err != nil {
		return nil, err
	}

	return &HTMLReport{
		RunID:      firstNonEmpty(cfg.ID, board.RunID, filepath.Base(runDir)),
		RunDir:     filepath.ToSlash(runDir),
		OutputPath: filepath.ToSlash(outputPath),
		Candidates: page.Summary.Candidates,
		Passed:     page.Summary.Passed,
		Failed:     page.Summary.Failed,
		Canceled:   page.Summary.Canceled,
		Pending:    page.Summary.Pending,
	}, nil
}

func buildReportPage(cfg model.RunConfig, board model.Leaderboard, runDir, outputPath string) reportPage {
	results := rankedResults(board.Results)
	baselinePrimary := baselinePrimaryMetric(results)
	baselineMemory := baselineMemoryMetric(results)

	page := reportPage{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Run:         cfg,
		Board:       board,
		RunDir:      filepath.ToSlash(runDir),
		OutputPath:  filepath.ToSlash(outputPath),
	}

	for i, result := range results {
		row := candidateReportRow(i+1, result, baselinePrimary, baselineMemory)
		page.Rows = append(page.Rows, row)
		page.Summary.Candidates++
		switch result.Status {
		case model.CandidateStatusPassed:
			page.Summary.Passed++
			if page.Summary.Best == "" {
				page.Summary.Best = result.Candidate.ID
				page.Summary.BestScore = formatNumber(result.Score)
			}
		case model.CandidateStatusFailed:
			page.Summary.Failed++
		case model.CandidateStatusCanceled:
			page.Summary.Canceled++
		default:
			page.Summary.Pending++
		}
	}
	if page.Summary.Best == "" {
		page.Summary.Best = "-"
		page.Summary.BestScore = "-"
	}
	return page
}

func candidateReportRow(rank int, result model.CandidateResult, baselinePrimary, baselineMemory float64) candidateRow {
	metrics := result.Metrics
	primary := scoring.PrimaryMetric(metrics)
	memory := scoring.MemoryMetric(metrics)
	cpu := metrics.CPUUserSeconds + metrics.CPUSystemSeconds
	row := candidateRow{
		Rank:          rank,
		ID:            result.Candidate.ID,
		Name:          firstNonEmpty(result.Candidate.Name, result.Candidate.ID),
		Status:        firstNonEmpty(result.Status, "pending"),
		Round:         result.Candidate.Round,
		Agent:         firstNonEmpty(result.Candidate.Agent, "-"),
		Model:         firstNonEmpty(result.Candidate.Model, "-"),
		Score:         formatNumber(result.Score),
		P95Latency:    formatMS(metrics.P95LatencyMS),
		Benchmark:     formatNumber(metrics.BenchmarkNsPerOp),
		Speedup:       formatImprovement(baselinePrimary, primary),
		Memory:        formatBytes(int64(memory)),
		MemoryBase:    formatRelative(memory, baselineMemory),
		EvalCPU:       formatSeconds(cpu),
		ExternalCalls: externalCallCount(result),
		SourcePath:    result.Candidate.SourcePath,
		Verdict:       verdictSummary(result.Verdict),
	}
	if result.ScoreExplanation != nil {
		row.ScoreReason = result.ScoreExplanation.Reason
		if row.ScoreReason == "" && result.ScoreExplanation.Scoreable {
			row.ScoreReason = fmt.Sprintf("penalty %s", formatNumber(result.ScoreExplanation.TotalPenalty))
		}
	}
	return row
}

func rankedResults(results []model.CandidateResult) []model.CandidateResult {
	ranked := append([]model.CandidateResult(nil), results...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left := ranked[i]
		right := ranked[j]
		if statusPriority(left.Status) != statusPriority(right.Status) {
			return statusPriority(left.Status) < statusPriority(right.Status)
		}
		if left.Status == model.CandidateStatusPassed && right.Status == model.CandidateStatusPassed {
			if left.Score != right.Score {
				return left.Score > right.Score
			}
			if left.Metrics.P95LatencyMS != right.Metrics.P95LatencyMS {
				return left.Metrics.P95LatencyMS < right.Metrics.P95LatencyMS
			}
		}
		return left.Candidate.ID < right.Candidate.ID
	})
	return ranked
}

func statusPriority(status string) int {
	switch status {
	case model.CandidateStatusPassed:
		return 0
	case model.CandidateStatusFailed:
		return 1
	case model.CandidateStatusCanceled:
		return 2
	default:
		return 3
	}
}

func baselinePrimaryMetric(results []model.CandidateResult) float64 {
	for _, result := range results {
		if result.Candidate.Baseline {
			return scoring.PrimaryMetric(result.Metrics)
		}
	}
	return 0
}

func baselineMemoryMetric(results []model.CandidateResult) float64 {
	for _, result := range results {
		if result.Candidate.Baseline {
			return scoring.MemoryMetric(result.Metrics)
		}
	}
	return 0
}

func externalCallCount(result model.CandidateResult) int {
	if result.Metrics.ExternalCallCount > 0 {
		return result.Metrics.ExternalCallCount
	}
	return result.External.RequestCount
}

func verdictSummary(verdict model.Verdict) string {
	switch {
	case verdict.CorrectnessPassed && verdict.BenchmarkPassed && verdict.ExternalPolicyPassed:
		return "passed"
	case !verdict.CorrectnessPassed:
		return "correctness failed"
	case !verdict.BenchmarkPassed:
		return "benchmark failed"
	case !verdict.ExternalPolicyPassed:
		return "external policy failed"
	default:
		return "pending"
	}
}

func formatNumber(value float64) string {
	if value == 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		if value == 0 {
			return "0"
		}
		return "-"
	}
	if math.Abs(value) >= 100000 || math.Abs(value) < 0.001 {
		return fmt.Sprintf("%.4e", value)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", value), "0"), ".")
}

func formatMS(value float64) string {
	if value <= 0 {
		return "-"
	}
	return formatNumber(value)
}

func formatSeconds(value float64) string {
	if value <= 0 {
		return "-"
	}
	return formatNumber(value)
}

func formatImprovement(baseline, value float64) string {
	if baseline <= 0 || value <= 0 {
		return "-"
	}
	return formatNumber(baseline/value) + "x"
}

func formatRelative(value, baseline float64) string {
	if baseline <= 0 || value <= 0 {
		return "-"
	}
	return formatNumber(value/baseline) + "x"
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
	v := float64(value)
	for _, suffix := range units {
		v /= unit
		if v < unit {
			return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".") + " " + suffix
		}
	}
	return fmt.Sprintf("%d B", value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Code Crucible Report - {{.Board.RunID}}</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f7f8fa;
      --panel: #ffffff;
      --text: #1b1f24;
      --muted: #667085;
      --border: #d8dee4;
      --accent: #0969da;
      --passed: #1a7f37;
      --failed: #cf222e;
      --pending: #8250df;
    }
    body {
      margin: 0;
      background: var(--bg);
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      line-height: 1.45;
    }
    header, main {
      max-width: 1180px;
      margin: 0 auto;
      padding: 24px;
    }
    header {
      padding-top: 32px;
      padding-bottom: 8px;
    }
    h1 {
      margin: 0 0 8px;
      font-size: 28px;
      line-height: 1.15;
    }
    h2 {
      margin: 28px 0 12px;
      font-size: 18px;
    }
    code {
      font-family: ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", monospace;
      font-size: 0.92em;
    }
    .muted {
      color: var(--muted);
    }
    .summary {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
      gap: 12px;
      margin: 20px 0 24px;
    }
    .metric {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 6px;
      padding: 14px;
    }
    .metric span {
      display: block;
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
    }
    .metric strong {
      display: block;
      margin-top: 4px;
      font-size: 22px;
    }
    .panel {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 6px;
      overflow: hidden;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 14px;
    }
    th, td {
      padding: 10px 12px;
      border-bottom: 1px solid var(--border);
      text-align: left;
      vertical-align: top;
      white-space: nowrap;
    }
    th {
      background: #eef2f6;
      color: #344054;
      font-size: 12px;
      text-transform: uppercase;
    }
    tr:last-child td {
      border-bottom: 0;
    }
    .candidate {
      white-space: normal;
      min-width: 220px;
    }
    .status {
      font-weight: 600;
    }
    .status-passed {
      color: var(--passed);
    }
    .status-failed {
      color: var(--failed);
    }
    .status-pending {
      color: var(--pending);
    }
    .details {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
      gap: 12px;
      margin-top: 12px;
    }
    .detail {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 6px;
      padding: 14px;
    }
    .detail h3 {
      margin: 0 0 8px;
      font-size: 15px;
    }
    .detail dl {
      display: grid;
      grid-template-columns: 110px 1fr;
      gap: 6px 10px;
      margin: 0;
      font-size: 13px;
    }
    .detail dt {
      color: var(--muted);
    }
    .detail dd {
      margin: 0;
      overflow-wrap: anywhere;
    }
    .table-wrap {
      overflow-x: auto;
    }
  </style>
</head>
<body>
  <header>
    <h1>Code Crucible Report</h1>
    <div class="muted">Run <code>{{.Board.RunID}}</code> generated {{.GeneratedAt}}</div>
  </header>
  <main>
    <section>
      <h2>Summary</h2>
      <div class="summary">
        <div class="metric"><span>Candidates</span><strong>{{.Summary.Candidates}}</strong></div>
        <div class="metric"><span>Passed</span><strong>{{.Summary.Passed}}</strong></div>
        <div class="metric"><span>Failed</span><strong>{{.Summary.Failed}}</strong></div>
        <div class="metric"><span>Canceled</span><strong>{{.Summary.Canceled}}</strong></div>
        <div class="metric"><span>Pending</span><strong>{{.Summary.Pending}}</strong></div>
        <div class="metric"><span>Best</span><strong>{{.Summary.Best}}</strong></div>
        <div class="metric"><span>Best Score</span><strong>{{.Summary.BestScore}}</strong></div>
      </div>
      <p><strong>Optimize:</strong> {{.Board.Optimize}}</p>
      <p class="muted">Run archive: <code>{{.RunDir}}</code></p>
    </section>

    <section>
      <h2>Leaderboard</h2>
      <div class="panel table-wrap">
        <table>
          <thead>
            <tr>
              <th>Rank</th>
              <th>Candidate</th>
              <th>Status</th>
              <th>Score</th>
              <th>P95 ms</th>
              <th>ns/op</th>
              <th>Speedup</th>
              <th>Memory</th>
              <th>Mem/Base</th>
              <th>Eval CPU s</th>
              <th>Ext Calls</th>
            </tr>
          </thead>
          <tbody>
            {{range .Rows}}
            <tr>
              <td>{{.Rank}}</td>
              <td class="candidate"><strong>{{.ID}}</strong><br><span class="muted">{{.Name}}</span></td>
              <td class="status status-{{.Status}}">{{.Status}}</td>
              <td>{{.Score}}</td>
              <td>{{.P95Latency}}</td>
              <td>{{.Benchmark}}</td>
              <td>{{.Speedup}}</td>
              <td>{{.Memory}}</td>
              <td>{{.MemoryBase}}</td>
              <td>{{.EvalCPU}}</td>
              <td>{{.ExternalCalls}}</td>
            </tr>
            {{end}}
          </tbody>
        </table>
      </div>
    </section>

    <section>
      <h2>Candidate Details</h2>
      <div class="details">
        {{range .Rows}}
        <article class="detail">
          <h3>{{.ID}}</h3>
          <dl>
            <dt>Round</dt><dd>{{.Round}}</dd>
            <dt>Agent</dt><dd>{{.Agent}}</dd>
            <dt>Model</dt><dd>{{.Model}}</dd>
            <dt>Source</dt><dd><code>{{.SourcePath}}</code></dd>
            <dt>Verdict</dt><dd>{{.Verdict}}</dd>
            <dt>Score note</dt><dd>{{if .ScoreReason}}{{.ScoreReason}}{{else}}-{{end}}</dd>
          </dl>
        </article>
        {{end}}
      </div>
    </section>
  </main>
</body>
</html>
`))
