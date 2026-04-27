package scoring

import (
	"math"

	"github.com/Automattic/code-crucible/internal/model"
)

const (
	maxScore                  = 1000.0
	pointsLostPerDoubling     = 100.0
	pointsLostPerMemDoubling  = 10.0
	pointsLostPerExternalCall = 25.0
)

func Score(result model.CandidateResult) float64 {
	primary := PrimaryMetric(result.Metrics)
	memory := MemoryMetric(result.Metrics)
	explanation := Explain(result, primary, memory)
	return explanation.FinalScore
}

func ScoreResults(results []model.CandidateResult) {
	bestPrimary := bestPrimaryMetric(results)
	bestMemory := bestMemoryMetric(results)
	for i := range results {
		explanation := Explain(results[i], bestPrimary, bestMemory)
		results[i].Score = explanation.FinalScore
		results[i].ScoreExplanation = &explanation
	}
}

func Explain(result model.CandidateResult, bestPrimary, bestMemory float64) model.ScoreExplanation {
	explanation := model.ScoreExplanation{
		Scoreable: scoreable(result),
		BaseScore: maxScore,
	}

	primary := PrimaryMetric(result.Metrics)
	memory := MemoryMetric(result.Metrics)
	explanation.BestPrimaryMetricMS = bestPrimary
	explanation.CandidatePrimaryMetricMS = primary
	explanation.BestMemoryBytes = bestMemory
	explanation.CandidateMemoryBytes = memory
	explanation.ExternalCallCount = externalCallCount(result)
	explanation.ExternalLatencyMS = result.Metrics.ExternalLatencyMS
	explanation.ExternalCostCents = result.Metrics.ExternalCostCents

	if !scoreable(result) {
		explanation.Reason = unscoreableReason(result)
		explanation.FinalScore = 0
		return explanation
	}

	if primary > 0 && bestPrimary > 0 {
		explanation.PrimaryMetricRatio = primary / bestPrimary
		explanation.PrimaryPenalty = ratioPenalty(primary, bestPrimary, pointsLostPerDoubling)
	}

	if memory > 0 && bestMemory > 0 {
		explanation.MemoryRatio = memory / bestMemory
		explanation.MemoryPenalty = ratioPenalty(memory, bestMemory, pointsLostPerMemDoubling)
	}

	explanation.ExternalCallPenalty = float64(explanation.ExternalCallCount) * pointsLostPerExternalCall
	explanation.ExternalCostPenalty = result.Metrics.ExternalCostCents
	if result.Metrics.ExternalLatencyMS > 0 {
		explanation.ExternalLatencyPenalty = result.Metrics.ExternalLatencyMS * 0.1
	}

	explanation.TotalPenalty = explanation.PrimaryPenalty +
		explanation.MemoryPenalty +
		explanation.ExternalCallPenalty +
		explanation.ExternalCostPenalty +
		explanation.ExternalLatencyPenalty
	unclamped := maxScore - explanation.TotalPenalty
	explanation.FinalScore = clampScore(unclamped)
	explanation.Clamped = explanation.FinalScore != unclamped
	return explanation
}

func scoreable(result model.CandidateResult) bool {
	return result.Verdict.CorrectnessPassed &&
		result.Verdict.BenchmarkPassed &&
		result.Verdict.ExternalPolicyPassed
}

func unscoreableReason(result model.CandidateResult) string {
	switch {
	case !result.Verdict.CorrectnessPassed:
		return "correctness failed"
	case !result.Verdict.BenchmarkPassed:
		return "benchmark failed"
	case !result.Verdict.ExternalPolicyPassed:
		return "external policy failed"
	default:
		return "candidate is not scoreable"
	}
}

func externalCallCount(result model.CandidateResult) int {
	if result.Metrics.ExternalCallCount > 0 {
		return result.Metrics.ExternalCallCount
	}
	return result.External.RequestCount
}

func bestPrimaryMetric(results []model.CandidateResult) float64 {
	var best float64
	for _, result := range results {
		if !scoreable(result) {
			continue
		}
		metric := PrimaryMetric(result.Metrics)
		if metric <= 0 {
			continue
		}
		if best == 0 || metric < best {
			best = metric
		}
	}
	return best
}

func bestMemoryMetric(results []model.CandidateResult) float64 {
	var best float64
	for _, result := range results {
		if !scoreable(result) {
			continue
		}
		metric := MemoryMetric(result.Metrics)
		if metric <= 0 {
			continue
		}
		if best == 0 || metric < best {
			best = metric
		}
	}
	return best
}

func PrimaryMetric(metrics model.Metrics) float64 {
	switch {
	case metrics.P95LatencyMS > 0:
		return metrics.P95LatencyMS
	case metrics.BenchmarkNsPerOp > 0:
		return metrics.BenchmarkNsPerOp / 1_000_000
	case metrics.RuntimeMeanMS > 0:
		return metrics.RuntimeMeanMS
	case metrics.CPUUserSeconds+metrics.CPUSystemSeconds > 0:
		return (metrics.CPUUserSeconds + metrics.CPUSystemSeconds) * 1000
	case metrics.WallTimeMS > 0:
		return metrics.WallTimeMS
	default:
		return 0
	}
}

func MemoryMetric(metrics model.Metrics) float64 {
	switch {
	case metrics.MemoryPeakBytes > 0:
		return float64(metrics.MemoryPeakBytes)
	case metrics.MaxRSSBytes > 0:
		return float64(metrics.MaxRSSBytes)
	default:
		return 0
	}
}

func ratioPenalty(value, best, pointsPerDoubling float64) float64 {
	if value <= 0 || best <= 0 || pointsPerDoubling <= 0 {
		return 0
	}
	ratio := value / best
	if ratio <= 1 {
		return 0
	}
	return math.Log2(ratio) * pointsPerDoubling
}

func clampScore(score float64) float64 {
	switch {
	case math.IsNaN(score) || score < 0:
		return 0
	case score > maxScore:
		return maxScore
	default:
		return score
	}
}
