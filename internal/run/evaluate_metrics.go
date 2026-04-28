package run

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Automattic/code-crucible/internal/archive"
	"github.com/Automattic/code-crucible/internal/model"
)

func processMetrics(state *os.ProcessState, wall time.Duration) model.Metrics {
	metrics := model.Metrics{
		WallTimeMS:           float64(wall.Microseconds()) / 1000,
		ResourceMetricSource: "host-process",
	}
	if state == nil {
		return metrics
	}

	metrics.CPUUserSeconds = state.UserTime().Seconds()
	metrics.CPUSystemSeconds = state.SystemTime().Seconds()
	totalCPUSeconds := metrics.CPUUserSeconds + metrics.CPUSystemSeconds
	if wall > 0 {
		metrics.CPUPercent = totalCPUSeconds / wall.Seconds() * 100
	}

	if usage, ok := state.SysUsage().(*syscall.Rusage); ok && usage != nil {
		// Linux reports Maxrss in kilobytes.
		metrics.MaxRSSBytes = usage.Maxrss * 1024
		metrics.VoluntaryContextSwitches = usage.Nvcsw
		metrics.InvoluntaryContextSwitches = usage.Nivcsw
		metrics.IOBytesRead = usage.Inblock * 512
		metrics.IOBytesWritten = usage.Oublock * 512
	}

	return metrics
}

func mergeResourceMetrics(metrics, resource model.Metrics) model.Metrics {
	if resource.WallTimeMS > 0 {
		metrics.WallTimeMS = resource.WallTimeMS
	}
	if resource.CPUUserSeconds > 0 {
		metrics.CPUUserSeconds = resource.CPUUserSeconds
	}
	if resource.CPUSystemSeconds > 0 {
		metrics.CPUSystemSeconds = resource.CPUSystemSeconds
	}
	if resource.CPUPercent > 0 {
		metrics.CPUPercent = resource.CPUPercent
	}
	if resource.MaxRSSBytes > 0 {
		metrics.MaxRSSBytes = resource.MaxRSSBytes
	}
	if resource.VoluntaryContextSwitches > 0 {
		metrics.VoluntaryContextSwitches = resource.VoluntaryContextSwitches
	}
	if resource.InvoluntaryContextSwitches > 0 {
		metrics.InvoluntaryContextSwitches = resource.InvoluntaryContextSwitches
	}
	if resource.IOBytesRead > 0 {
		metrics.IOBytesRead = resource.IOBytesRead
	}
	if resource.IOBytesWritten > 0 {
		metrics.IOBytesWritten = resource.IOBytesWritten
	}
	if resource.ResourceMetricSource != "" {
		metrics.ResourceMetricSource = resource.ResourceMetricSource
	}
	return metrics
}

func loadMetrics(path string) (model.Metrics, error) {
	var metrics model.Metrics
	if err := loadJSON(path, &metrics); err != nil {
		return metrics, fmt.Errorf("metrics missing or invalid: %w", err)
	}
	return metrics, nil
}

func loadResourceMetrics(path string) (model.Metrics, error) {
	var metrics model.Metrics
	if err := loadJSON(path, &metrics); err != nil {
		return metrics, fmt.Errorf("resource metrics missing or invalid: %w", err)
	}
	return metrics, nil
}

func loadVerdict(path string) (model.Verdict, error) {
	var verdict model.Verdict
	if err := loadJSON(path, &verdict); err != nil {
		return verdict, fmt.Errorf("verdict missing or invalid: %w", err)
	}
	return verdict, nil
}

func loadJSON(path string, out any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("contains trailing data")
	}
	return nil
}

func candidateDirectory(projectDir string, candidate model.Candidate) string {
	sourcePath := archive.ProjectPath(projectDir, candidate.SourcePath)
	if filepath.Base(sourcePath) == "src" {
		return filepath.Dir(sourcePath)
	}
	return sourcePath
}

func statusForVerdict(verdict model.Verdict) string {
	if verdict.CorrectnessPassed && verdict.BenchmarkPassed && verdict.ExternalPolicyPassed {
		return model.CandidateStatusPassed
	}
	return model.CandidateStatusFailed
}
