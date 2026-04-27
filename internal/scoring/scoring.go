package scoring

import "github.com/Automattic/code-crucible/internal/model"

func Score(result model.CandidateResult) float64 {
	if !result.Verdict.CorrectnessPassed || !result.Verdict.ExternalPolicyPassed {
		return 0
	}

	score := 1000.0
	if result.Metrics.P95LatencyMS > 0 {
		score -= result.Metrics.P95LatencyMS
	}
	if result.Metrics.RuntimeMeanMS > 0 {
		score -= result.Metrics.RuntimeMeanMS * 0.25
	}
	if result.Metrics.MemoryPeakBytes > 0 {
		score -= float64(result.Metrics.MemoryPeakBytes) / (1024 * 1024)
	}
	score -= float64(result.Metrics.ExternalCallCount) * 5
	if score < 0 {
		return 0
	}
	return score
}
