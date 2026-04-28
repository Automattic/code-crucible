package evaluator

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Automattic/code-crucible/internal/discovery"
)

const ContractChecksFilename = "contract-checks.json"

type ContractChecks struct {
	Version                  int      `json:"version"`
	SourcePath               string   `json:"source_path,omitempty"`
	RequiredSourceExtensions []string `json:"required_source_extensions,omitempty"`
	DropInInterface          string   `json:"drop_in_interface,omitempty"`
	Inputs                   []string `json:"inputs,omitempty"`
	Outputs                  []string `json:"outputs,omitempty"`
	EvaluatorStrategy        []string `json:"evaluator_strategy,omitempty"`
	Metrics                  []string `json:"metrics,omitempty"`
	GeneratedChecks          []string `json:"generated_checks"`
}

func BuildContractChecks(plan *discovery.AgentPlan) *ContractChecks {
	if !hasDeterministicContractDetail(plan) {
		return nil
	}
	checks := &ContractChecks{
		Version:           1,
		SourcePath:        strings.TrimSpace(plan.SourcePath),
		DropInInterface:   strings.TrimSpace(plan.DropInInterface),
		Inputs:            trimmedValues(plan.Inputs),
		Outputs:           trimmedValues(plan.Outputs),
		EvaluatorStrategy: trimmedValues(plan.EvaluatorStrategy),
		Metrics:           trimmedValues(plan.Metrics),
		GeneratedChecks: []string{
			"candidate.json exists",
			"design.md exists",
			"candidate src contains at least one source file",
		},
	}
	if ext := sourceExtension(plan.SourcePath); ext != "" {
		checks.RequiredSourceExtensions = []string{ext}
		checks.GeneratedChecks = append(checks.GeneratedChecks, "candidate src contains "+ext+" files")
	}
	return checks
}

func MarshalContractChecks(checks *ContractChecks) ([]byte, error) {
	data, err := json.MarshalIndent(checks, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func hasDeterministicContractDetail(plan *discovery.AgentPlan) bool {
	if plan == nil {
		return false
	}
	hasInterface := strings.TrimSpace(plan.DropInInterface) != ""
	hasIO := len(trimmedValues(plan.Inputs)) > 0 || len(trimmedValues(plan.Outputs)) > 0
	hasEvaluatorHint := len(trimmedValues(plan.EvaluatorStrategy)) > 0
	hasSourceExtension := sourceExtension(plan.SourcePath) != ""
	return hasInterface && (hasIO || hasEvaluatorHint || hasSourceExtension)
}

func sourceExtension(path string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	if ext == "" {
		return ""
	}
	return ext
}

func trimmedValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
