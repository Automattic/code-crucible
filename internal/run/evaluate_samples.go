package run

import (
	"fmt"
	"math"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

type evaluatorSamplesResult struct {
	Metrics  model.Metrics
	Resource model.Metrics
	Verdict  model.Verdict
	Samples  []evaluationSample
	Errors   []string
	Warnings []string
}

type evaluationSample struct {
	Index    int           `json:"index"`
	Kind     string        `json:"kind"`
	Status   string        `json:"status"`
	Metrics  model.Metrics `json:"metrics,omitempty"`
	Resource model.Metrics `json:"resource_metrics,omitempty"`
	Verdict  model.Verdict `json:"verdict,omitempty"`
	Error    string        `json:"error,omitempty"`
	Warnings []string      `json:"warnings,omitempty"`
}

type evaluationSamplesArchive struct {
	Warmups     int                `json:"warmups"`
	Repetitions int                `json:"repetitions"`
	Samples     []evaluationSample `json:"samples"`
}

func runEvaluatorSampleSet(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, tracePath, recordFixturesPath, verdictPath, stdoutPath, stderrPath, samplesPath string, opts evaluatorExecutionOptions) evaluatorSamplesResult {
	warmups, repetitions := normalizedEvaluationCounts(opts)
	total := warmups + repetitions
	out := evaluatorSamplesResult{
		Samples: make([]evaluationSample, 0, total),
	}
	measuredMetrics := make([]model.Metrics, 0, repetitions)
	measuredVerdicts := make([]model.Verdict, 0, repetitions)

	for i := 0; i < total; i++ {
		kind := "repetition"
		if i < warmups {
			kind = "warmup"
		}
		sample := evaluationSample{
			Index: i + 1,
			Kind:  kind,
		}
		if err := removeEvaluationOutputs(metricsPath, resourcePath, tracePath, recordFixturesPath, verdictPath); err != nil {
			sample.Warnings = append(sample.Warnings, err.Error())
			out.Warnings = append(out.Warnings, err.Error())
		}

		resourceMetrics, runErr := runEvaluatorScript(evaluatorPath, candidateDir, runDir, metricsPath, resourcePath, verdictPath, stdoutPath, stderrPath, opts)
		sample.Resource = resourceMetrics
		if runErr != nil {
			sample.Status = "failed"
			sample.Error = fmt.Sprintf("%s %d failed: %v", kind, i+1, runErr)
			out.Errors = append(out.Errors, sample.Error)
			if metrics, err := loadMetrics(metricsPath); err == nil {
				metrics = mergeResourceMetrics(metrics, resourceMetrics)
				sample.Metrics = metrics
				if kind == "repetition" {
					measuredMetrics = append(measuredMetrics, metrics)
				}
			}
			if verdict, err := loadVerdict(verdictPath); err == nil {
				sample.Verdict = verdict
				if kind == "repetition" {
					measuredVerdicts = append(measuredVerdicts, verdict)
				}
			}
			out.Samples = append(out.Samples, sample)
			break
		}

		metrics, err := loadMetrics(metricsPath)
		if err != nil {
			sample.Warnings = append(sample.Warnings, err.Error())
			out.Warnings = append(out.Warnings, err.Error())
		}
		metrics = mergeResourceMetrics(metrics, resourceMetrics)

		verdict, err := loadVerdict(verdictPath)
		if err != nil {
			verdict = model.Verdict{
				CorrectnessPassed:    false,
				BenchmarkPassed:      false,
				ExternalPolicyPassed: false,
				Errors: []string{
					err.Error(),
				},
			}
		}
		sample.Metrics = metrics
		sample.Verdict = verdict
		sample.Status = statusForVerdict(verdict)
		out.Samples = append(out.Samples, sample)

		if kind == "warmup" {
			continue
		}
		measuredMetrics = append(measuredMetrics, metrics)
		measuredVerdicts = append(measuredVerdicts, verdict)
		if sample.Status != "passed" {
			break
		}
	}

	if len(measuredMetrics) > 0 {
		out.Metrics = aggregateMetricSamples(measuredMetrics, warmups)
		out.Resource = aggregateResourceSamples(measuredMetrics)
	}
	if len(measuredVerdicts) > 0 {
		out.Verdict = combineSampleVerdicts(measuredVerdicts)
	} else if len(out.Errors) > 0 {
		out.Verdict = model.Verdict{
			CorrectnessPassed:    false,
			BenchmarkPassed:      false,
			ExternalPolicyPassed: false,
			Errors:               append([]string(nil), out.Errors...),
		}
	} else {
		out.Verdict = model.Verdict{
			CorrectnessPassed:    false,
			BenchmarkPassed:      false,
			ExternalPolicyPassed: false,
			Errors: []string{
				"no measured evaluation repetitions completed",
			},
		}
	}

	if err := archive.SaveJSON(samplesPath, evaluationSamplesArchive{
		Warmups:     warmups,
		Repetitions: repetitions,
		Samples:     out.Samples,
	}); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("save evaluation samples: %v", err))
	}
	return out
}

func normalizedEvaluationCounts(opts evaluatorExecutionOptions) (int, int) {
	warmups := opts.Warmups
	if warmups < 0 {
		warmups = 0
	}
	repetitions := opts.Repetitions
	if repetitions <= 0 {
		repetitions = 1
	}
	return warmups, repetitions
}

func aggregateMetricSamples(samples []model.Metrics, warmups int) model.Metrics {
	metrics := model.Metrics{
		EvaluationWarmups:     warmups,
		EvaluationRepetitions: len(samples),
		ResourceMetricSource:  resourceMetricSource(samples),
	}
	runtimes := nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.RuntimeMeanMS })
	metrics.RuntimeMeanMS = meanFloat(runtimes)
	metrics.RuntimeMinMS = minFloat(runtimes)
	metrics.RuntimeMaxMS = maxFloat(runtimes)
	metrics.RuntimeStddevMS = stddevFloat(runtimes)
	metrics.P95LatencyMS = meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.P95LatencyMS }))
	metrics.BenchmarkNsPerOp = meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.BenchmarkNsPerOp }))
	metrics.BenchmarkRuns = sumInt(samples, func(m model.Metrics) int { return m.BenchmarkRuns })
	metrics.MemoryPeakBytes = maxInt64(samples, func(m model.Metrics) int64 { return m.MemoryPeakBytes })
	metrics.WallTimeMS = meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.WallTimeMS }))
	metrics.CPUUserSeconds = meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.CPUUserSeconds }))
	metrics.CPUSystemSeconds = meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.CPUSystemSeconds }))
	metrics.CPUPercent = meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.CPUPercent }))
	metrics.MaxRSSBytes = maxInt64(samples, func(m model.Metrics) int64 { return m.MaxRSSBytes })
	metrics.VoluntaryContextSwitches = sumInt64(samples, func(m model.Metrics) int64 { return m.VoluntaryContextSwitches })
	metrics.InvoluntaryContextSwitches = sumInt64(samples, func(m model.Metrics) int64 { return m.InvoluntaryContextSwitches })
	metrics.IOBytesRead = sumInt64(samples, func(m model.Metrics) int64 { return m.IOBytesRead })
	metrics.IOBytesWritten = sumInt64(samples, func(m model.Metrics) int64 { return m.IOBytesWritten })
	metrics.ExternalCallCount = int(math.Round(meanFloat(intValuesAsFloat(samples, func(m model.Metrics) int { return m.ExternalCallCount }))))
	metrics.ExternalLatencyMS = meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.ExternalLatencyMS }))
	metrics.ExternalCostCents = meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.ExternalCostCents }))
	return metrics
}

func aggregateResourceSamples(samples []model.Metrics) model.Metrics {
	resource := model.Metrics{
		WallTimeMS:                 meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.WallTimeMS })),
		CPUUserSeconds:             meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.CPUUserSeconds })),
		CPUSystemSeconds:           meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.CPUSystemSeconds })),
		CPUPercent:                 meanFloat(nonZeroFloatValues(samples, func(m model.Metrics) float64 { return m.CPUPercent })),
		MaxRSSBytes:                maxInt64(samples, func(m model.Metrics) int64 { return m.MaxRSSBytes }),
		VoluntaryContextSwitches:   sumInt64(samples, func(m model.Metrics) int64 { return m.VoluntaryContextSwitches }),
		InvoluntaryContextSwitches: sumInt64(samples, func(m model.Metrics) int64 { return m.InvoluntaryContextSwitches }),
		IOBytesRead:                sumInt64(samples, func(m model.Metrics) int64 { return m.IOBytesRead }),
		IOBytesWritten:             sumInt64(samples, func(m model.Metrics) int64 { return m.IOBytesWritten }),
		ResourceMetricSource:       resourceMetricSource(samples),
	}
	return resource
}

func resourceMetricSource(samples []model.Metrics) string {
	if len(samples) == 1 && samples[0].ResourceMetricSource != "" {
		return samples[0].ResourceMetricSource
	}
	return "evaluation-samples"
}

func combineSampleVerdicts(verdicts []model.Verdict) model.Verdict {
	combined := model.Verdict{
		CorrectnessPassed:    len(verdicts) > 0,
		BenchmarkPassed:      len(verdicts) > 0,
		ExternalPolicyPassed: len(verdicts) > 0,
	}
	for _, verdict := range verdicts {
		combined.CorrectnessPassed = combined.CorrectnessPassed && verdict.CorrectnessPassed
		combined.BenchmarkPassed = combined.BenchmarkPassed && verdict.BenchmarkPassed
		combined.ExternalPolicyPassed = combined.ExternalPolicyPassed && verdict.ExternalPolicyPassed
		combined.Errors = append(combined.Errors, verdict.Errors...)
		combined.Warnings = append(combined.Warnings, verdict.Warnings...)
		combined.Notes = append(combined.Notes, verdict.Notes...)
	}
	return combined
}

func nonZeroFloatValues(samples []model.Metrics, value func(model.Metrics) float64) []float64 {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if v := value(sample); v > 0 {
			values = append(values, v)
		}
	}
	return values
}

func intValuesAsFloat(samples []model.Metrics, value func(model.Metrics) int) []float64 {
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if v := value(sample); v > 0 {
			values = append(values, float64(v))
		}
	}
	return values
}

func meanFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func minFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, value := range values[1:] {
		if value < min {
			min = value
		}
	}
	return min
}

func maxFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	max := values[0]
	for _, value := range values[1:] {
		if value > max {
			max = value
		}
	}
	return max
}

func stddevFloat(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	mean := meanFloat(values)
	var sum float64
	for _, value := range values {
		diff := value - mean
		sum += diff * diff
	}
	return math.Sqrt(sum / float64(len(values)))
}

func sumInt(samples []model.Metrics, value func(model.Metrics) int) int {
	var total int
	for _, sample := range samples {
		total += value(sample)
	}
	return total
}

func sumInt64(samples []model.Metrics, value func(model.Metrics) int64) int64 {
	var total int64
	for _, sample := range samples {
		total += value(sample)
	}
	return total
}

func maxInt64(samples []model.Metrics, value func(model.Metrics) int64) int64 {
	var max int64
	for _, sample := range samples {
		if v := value(sample); v > max {
			max = v
		}
	}
	return max
}
