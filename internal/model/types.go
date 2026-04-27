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
	ID            string         `json:"id"`
	ProjectDir    string         `json:"project_dir"`
	Optimize      string         `json:"optimize"`
	TargetPath    string         `json:"target_path,omitempty"`
	Agent         string         `json:"agent"`
	Variants      int            `json:"variants"`
	Rounds        int            `json:"rounds"`
	Exploration   float64        `json:"exploration"`
	Evaluator     string         `json:"evaluator,omitempty"`
	External      ExternalPolicy `json:"external"`
	CreatedAt     time.Time      `json:"created_at"`
	InterfaceDocs string         `json:"interface_docs"`
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
	RuntimeMeanMS     float64 `json:"runtime_mean_ms,omitempty"`
	P95LatencyMS      float64 `json:"p95_latency_ms,omitempty"`
	MemoryPeakBytes   int64   `json:"memory_peak_bytes,omitempty"`
	CPUUserSeconds    float64 `json:"cpu_user_seconds,omitempty"`
	CPUSystemSeconds  float64 `json:"cpu_system_seconds,omitempty"`
	IOBytesRead       int64   `json:"io_bytes_read,omitempty"`
	IOBytesWritten    int64   `json:"io_bytes_written,omitempty"`
	ExternalCallCount int     `json:"external_call_count,omitempty"`
	ExternalLatencyMS float64 `json:"external_latency_ms,omitempty"`
	ExternalCostCents float64 `json:"external_cost_cents,omitempty"`
}

type ExternalCallTrace struct {
	Mode             ExternalMode `json:"mode"`
	RequestCount     int          `json:"request_count"`
	UniqueHosts      []string     `json:"unique_hosts,omitempty"`
	BytesSent        int64        `json:"bytes_sent,omitempty"`
	BytesReceived    int64        `json:"bytes_received,omitempty"`
	RetryCount       int          `json:"retry_count,omitempty"`
	FailureCount     int          `json:"failure_count,omitempty"`
	EstimatedCost    float64      `json:"estimated_cost,omitempty"`
	TracePath        string       `json:"trace_path,omitempty"`
	PolicyPassed     bool         `json:"policy_passed"`
	PolicyViolations []string     `json:"policy_violations,omitempty"`
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
	Candidate Candidate         `json:"candidate"`
	Metrics   Metrics           `json:"metrics"`
	External  ExternalCallTrace `json:"external"`
	Verdict   Verdict           `json:"verdict"`
	Score     float64           `json:"score,omitempty"`
	Status    string            `json:"status"`
}

type Leaderboard struct {
	RunID     string            `json:"run_id"`
	Optimize  string            `json:"optimize"`
	CreatedAt time.Time         `json:"created_at"`
	Results   []CandidateResult `json:"results"`
}
