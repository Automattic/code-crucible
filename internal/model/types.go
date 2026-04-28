package model

import "time"

type ExternalMode string

const (
	ExternalModeDeny      ExternalMode = "deny"
	ExternalModeAllowlist ExternalMode = "allowlist"
	ExternalModeMock      ExternalMode = "mock"
	ExternalModeReplay    ExternalMode = "replay"
	ExternalModeRecord    ExternalMode = "record"
)

func (m ExternalMode) Valid() bool {
	switch m {
	case ExternalModeDeny, ExternalModeAllowlist, ExternalModeMock, ExternalModeReplay, ExternalModeRecord:
		return true
	default:
		return false
	}
}

type ExternalPolicy struct {
	Mode      ExternalMode `json:"mode"`
	Allowlist []string     `json:"allowlist,omitempty"`
	Fixtures  string       `json:"fixtures,omitempty"`
}

type RunConfig struct {
	ID              string         `json:"id"`
	ProjectDir      string         `json:"project_dir"`
	RunDir          string         `json:"run_dir"`
	RoundDir        string         `json:"round_dir"`
	Optimize        string         `json:"optimize"`
	SourcePath      string         `json:"source_path,omitempty"`
	Agent           string         `json:"agent"`
	Variants        int            `json:"variants"`
	Rounds          int            `json:"rounds"`
	Exploration     float64        `json:"exploration"`
	Evaluator       string         `json:"evaluator,omitempty"`
	EvaluatorScript string         `json:"evaluator_script,omitempty"`
	External        ExternalPolicy `json:"external"`
	CreatedAt       time.Time      `json:"created_at"`
	InterfaceDocs   string         `json:"interface_docs"`
	PromptPath      string         `json:"prompt_path"`
}

type Candidate struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Round      int       `json:"round"`
	ParentIDs  []string  `json:"parent_ids,omitempty"`
	Agent      string    `json:"agent"`
	Model      string    `json:"model,omitempty"`
	SourcePath string    `json:"source_path"`
	Baseline   bool      `json:"baseline"`
	CreatedAt  time.Time `json:"created_at"`
}

type Metrics struct {
	RuntimeMeanMS              float64 `json:"runtime_mean_ms,omitempty"`
	RuntimeMinMS               float64 `json:"runtime_min_ms,omitempty"`
	RuntimeMaxMS               float64 `json:"runtime_max_ms,omitempty"`
	RuntimeStddevMS            float64 `json:"runtime_stddev_ms,omitempty"`
	P95LatencyMS               float64 `json:"p95_latency_ms,omitempty"`
	BenchmarkNsPerOp           float64 `json:"benchmark_ns_per_op,omitempty"`
	BenchmarkRuns              int     `json:"benchmark_runs,omitempty"`
	EvaluationWarmups          int     `json:"evaluation_warmups,omitempty"`
	EvaluationRepetitions      int     `json:"evaluation_repetitions,omitempty"`
	MemoryPeakBytes            int64   `json:"memory_peak_bytes,omitempty"`
	WallTimeMS                 float64 `json:"wall_time_ms,omitempty"`
	CPUUserSeconds             float64 `json:"cpu_user_seconds,omitempty"`
	CPUSystemSeconds           float64 `json:"cpu_system_seconds,omitempty"`
	CPUPercent                 float64 `json:"cpu_percent,omitempty"`
	MaxRSSBytes                int64   `json:"max_rss_bytes,omitempty"`
	VoluntaryContextSwitches   int64   `json:"voluntary_context_switches,omitempty"`
	InvoluntaryContextSwitches int64   `json:"involuntary_context_switches,omitempty"`
	IOBytesRead                int64   `json:"io_bytes_read,omitempty"`
	IOBytesWritten             int64   `json:"io_bytes_written,omitempty"`
	ResourceMetricSource       string  `json:"resource_metric_source,omitempty"`
	ExternalCallCount          int     `json:"external_call_count,omitempty"`
	ExternalLatencyMS          float64 `json:"external_latency_ms,omitempty"`
	ExternalCostCents          float64 `json:"external_cost_cents,omitempty"`
}

type ExternalCallTrace struct {
	Mode               ExternalMode               `json:"mode"`
	RequestCount       int                        `json:"request_count"`
	UniqueHosts        []string                   `json:"unique_hosts,omitempty"`
	BytesSent          int64                      `json:"bytes_sent,omitempty"`
	BytesReceived      int64                      `json:"bytes_received,omitempty"`
	RetryCount         int                        `json:"retry_count,omitempty"`
	FailureCount       int                        `json:"failure_count,omitempty"`
	EstimatedCost      float64                    `json:"estimated_cost,omitempty"`
	TracePath          string                     `json:"trace_path,omitempty"`
	RecordFixturesPath string                     `json:"record_fixtures_path,omitempty"`
	PolicyPassed       bool                       `json:"policy_passed"`
	PolicyViolations   []string                   `json:"policy_violations,omitempty"`
	PolicyEnforcement  *ExternalPolicyEnforcement `json:"policy_enforcement,omitempty"`
}

type ExternalPolicyEnforcement struct {
	Mode      ExternalMode `json:"mode"`
	Status    string       `json:"status"`
	Mechanism string       `json:"mechanism,omitempty"`
	Warnings  []string     `json:"warnings,omitempty"`
	Errors    []string     `json:"errors,omitempty"`
}

type Verdict struct {
	CorrectnessPassed    bool     `json:"correctness_passed"`
	BenchmarkPassed      bool     `json:"benchmark_passed"`
	ExternalPolicyPassed bool     `json:"external_policy_passed"`
	Errors               []string `json:"errors,omitempty"`
	Warnings             []string `json:"warnings,omitempty"`
	Notes                []string `json:"notes,omitempty"`
}

type CandidateResult struct {
	Candidate        Candidate         `json:"candidate"`
	Metrics          Metrics           `json:"metrics"`
	External         ExternalCallTrace `json:"external"`
	Verdict          Verdict           `json:"verdict"`
	Score            float64           `json:"score"`
	ScoreExplanation *ScoreExplanation `json:"score_explanation,omitempty"`
	Status           string            `json:"status"`
}

type ScoreExplanation struct {
	Scoreable bool   `json:"scoreable"`
	Reason    string `json:"reason,omitempty"`

	BaseScore  float64 `json:"base_score"`
	FinalScore float64 `json:"final_score"`
	Clamped    bool    `json:"clamped,omitempty"`

	BestPrimaryMetricMS      float64 `json:"best_primary_metric_ms,omitempty"`
	CandidatePrimaryMetricMS float64 `json:"candidate_primary_metric_ms,omitempty"`
	PrimaryMetricRatio       float64 `json:"primary_metric_ratio,omitempty"`
	PrimaryPenalty           float64 `json:"primary_penalty,omitempty"`

	BestMemoryBytes      float64 `json:"best_memory_bytes,omitempty"`
	CandidateMemoryBytes float64 `json:"candidate_memory_bytes,omitempty"`
	MemoryRatio          float64 `json:"memory_ratio,omitempty"`
	MemoryPenalty        float64 `json:"memory_penalty,omitempty"`

	ExternalCallCount      int     `json:"external_call_count,omitempty"`
	ExternalCallPenalty    float64 `json:"external_call_penalty,omitempty"`
	ExternalLatencyMS      float64 `json:"external_latency_ms,omitempty"`
	ExternalLatencyPenalty float64 `json:"external_latency_penalty,omitempty"`
	ExternalCostCents      float64 `json:"external_cost_cents,omitempty"`
	ExternalCostPenalty    float64 `json:"external_cost_penalty,omitempty"`

	TotalPenalty float64 `json:"total_penalty,omitempty"`
}

type Leaderboard struct {
	RunID     string            `json:"run_id"`
	Optimize  string            `json:"optimize"`
	CreatedAt time.Time         `json:"created_at"`
	Results   []CandidateResult `json:"results"`
}
