package scoring

import (
	"math"
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
	if results[2].ScoreExplanation == nil {
		t.Fatal("best candidate score explanation is nil")
	}
	if results[2].ScoreExplanation.FinalScore != results[2].Score {
		t.Fatalf("explanation final score = %f, score = %f", results[2].ScoreExplanation.FinalScore, results[2].Score)
	}
	if results[0].ScoreExplanation == nil || !results[0].ScoreExplanation.Clamped {
		t.Fatalf("baseline explanation = %#v, want clamped penalty explanation", results[0].ScoreExplanation)
	}
	if results[1].ScoreExplanation.PrimaryMetricRatio != 100 {
		t.Fatalf("middle primary ratio = %f, want 100", results[1].ScoreExplanation.PrimaryMetricRatio)
	}
	if !closeEnough(results[1].ScoreExplanation.PrimaryPenalty, math.Log2(100)*pointsLostPerDoubling) {
		t.Fatalf("middle primary penalty = %f", results[1].ScoreExplanation.PrimaryPenalty)
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
	if results[0].ScoreExplanation == nil {
		t.Fatal("failed candidate score explanation is nil")
	}
	if results[0].ScoreExplanation.Scoreable {
		t.Fatalf("failed candidate explanation scoreable = true")
	}
	if results[0].ScoreExplanation.Reason != "correctness failed" {
		t.Fatalf("failed candidate reason = %q", results[0].ScoreExplanation.Reason)
	}
}

func TestScoreResultsExplainsExternalPenalties(t *testing.T) {
	results := []model.CandidateResult{
		scoreFixture("baseline", 1, 1024),
		scoreFixture("with-external", 1, 1024),
	}
	results[1].Metrics.ExternalCallCount = 2
	results[1].Metrics.ExternalLatencyMS = 50
	results[1].Metrics.ExternalCostCents = 3

	ScoreResults(results)

	explanation := results[1].ScoreExplanation
	if explanation == nil {
		t.Fatal("score explanation is nil")
	}
	if explanation.ExternalCallPenalty != 50 {
		t.Fatalf("external call penalty = %f, want 50", explanation.ExternalCallPenalty)
	}
	if explanation.ExternalLatencyPenalty != 5 {
		t.Fatalf("external latency penalty = %f, want 5", explanation.ExternalLatencyPenalty)
	}
	if explanation.ExternalCostPenalty != 3 {
		t.Fatalf("external cost penalty = %f, want 3", explanation.ExternalCostPenalty)
	}
	if explanation.TotalPenalty != 58 {
		t.Fatalf("total penalty = %f, want 58", explanation.TotalPenalty)
	}
	if results[1].Score != 942 {
		t.Fatalf("score = %f, want 942", results[1].Score)
	}
}

func TestScoreResultsUsesExternalTraceCountFallback(t *testing.T) {
	results := []model.CandidateResult{
		scoreFixture("baseline", 1, 1024),
		scoreFixture("trace-calls", 1, 1024),
	}
	results[1].External.RequestCount = 3

	ScoreResults(results)

	explanation := results[1].ScoreExplanation
	if explanation == nil {
		t.Fatal("score explanation is nil")
	}
	if explanation.ExternalCallCount != 3 {
		t.Fatalf("external call count = %d, want 3", explanation.ExternalCallCount)
	}
	if explanation.ExternalCallPenalty != 75 {
		t.Fatalf("external call penalty = %f, want 75", explanation.ExternalCallPenalty)
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

func closeEnough(left, right float64) bool {
	return math.Abs(left-right) < 0.000001
}
