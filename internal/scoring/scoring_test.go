package scoring

import (
	"testing"

	"github.com/Automattic/code-crucible/internal/model"
)

func TestScoreResultsUsesRelativeLogScale(t *testing.T) {
	results := []model.CandidateResult{
		scoreFixture("baseline", 100, 32768),
		scoreFixture("full-sort", 1, 32768),
		scoreFixture("heap", 0.01, 208),
	}

	ScoreResults(results)

	if results[2].Score < 999 {
		t.Fatalf("best candidate score = %f, want near 1000", results[2].Score)
	}
	if results[0].Score >= 100 {
		t.Fatalf("baseline score = %f, want a large penalty for massive latency gap", results[0].Score)
	}
	if results[1].Score <= results[0].Score {
		t.Fatalf("middle candidate score = %f, baseline = %f, want middle candidate higher", results[1].Score, results[0].Score)
	}
}

func TestScoreResultsFailsClosed(t *testing.T) {
	result := scoreFixture("failed", 1, 1)
	result.Verdict.CorrectnessPassed = false
	results := []model.CandidateResult{result}

	ScoreResults(results)

	if results[0].Score != 0 {
		t.Fatalf("failed candidate score = %f, want 0", results[0].Score)
	}
}

func scoreFixture(id string, p95MS float64, memoryBytes int64) model.CandidateResult {
	return model.CandidateResult{
		Candidate: model.Candidate{ID: id},
		Metrics: model.Metrics{
			P95LatencyMS:    p95MS,
			MemoryPeakBytes: memoryBytes,
		},
		Verdict: model.Verdict{
			CorrectnessPassed:    true,
			BenchmarkPassed:      true,
			ExternalPolicyPassed: true,
		},
		Status: "passed",
	}
}
