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
	return scoreWithReference(result, primary, memory)
}

func ScoreResults(results []model.CandidateResult) {
	bestPrimary := bestPrimaryMetric(results)
	bestMemory := bestMemoryMetric(results)
	for i := range results {
		results[i].Score = scoreWithReference(results[i], bestPrimary, bestMemory)
	}
}

func scoreWithReference(result model.CandidateResult, bestPrimary, bestMemory float64) float64 {
	if !scoreable(result) {
		return 0
	}

	score := maxScore
	primary := PrimaryMetric(result.Metrics)
	if primary > 0 && bestPrimary > 0 {
		score -= ratioPenalty(primary, bestPrimary, pointsLostPerDoubling)
	}

	memory := MemoryMetric(result.Metrics)
	if memory > 0 && bestMemory > 0 {
		score -= ratioPenalty(memory, bestMemory, pointsLostPerMemDoubling)
	}

	score -= float64(result.Metrics.ExternalCallCount) * pointsLostPerExternalCall
	score -= result.Metrics.ExternalCostCents
	if result.Metrics.ExternalLatencyMS > 0 {
		score -= result.Metrics.ExternalLatencyMS * 0.1
	}

	return clampScore(score)
}

func scoreable(result model.CandidateResult) bool {
	return result.Verdict.CorrectnessPassed &&
		result.Verdict.BenchmarkPassed &&
		result.Verdict.ExternalPolicyPassed
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
