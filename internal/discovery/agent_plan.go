package discovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const AgentPlanFilename = "agent-plan.json"

type AgentPlan struct {
	SourcePath             string                  `json:"source_path,omitempty"`
	SourcePathConfidence   string                  `json:"source_path_confidence,omitempty"`
	DropInInterface        string                  `json:"drop_in_interface,omitempty"`
	Inputs                 []string                `json:"inputs,omitempty"`
	Outputs                []string                `json:"outputs,omitempty"`
	ExternalCommunications []ExternalCommunication `json:"external_communications,omitempty"`
	ExternalMode           string                  `json:"external_mode,omitempty"`
	EvaluatorStrategy      []string                `json:"evaluator_strategy,omitempty"`
	Metrics                []string                `json:"metrics,omitempty"`
	ClarifyingQuestions    []string                `json:"clarifying_questions,omitempty"`
	SuggestedNextCommand   string                  `json:"suggested_next_command,omitempty"`
	Notes                  []string                `json:"notes,omitempty"`
}

type ExternalCommunication struct {
	Service  string `json:"service,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Request  string `json:"request,omitempty"`
	Response string `json:"response,omitempty"`
	Auth     string `json:"auth,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

func (p AgentPlan) HasContent() bool {
	return strings.TrimSpace(p.SourcePath) != "" ||
		strings.TrimSpace(p.DropInInterface) != "" ||
		len(p.Inputs) > 0 ||
		len(p.Outputs) > 0 ||
		len(p.ExternalCommunications) > 0 ||
		strings.TrimSpace(p.ExternalMode) != "" ||
		len(p.EvaluatorStrategy) > 0 ||
		len(p.Metrics) > 0 ||
		len(p.ClarifyingQuestions) > 0 ||
		strings.TrimSpace(p.SuggestedNextCommand) != "" ||
		len(p.Notes) > 0
}

func LoadAgentPlan(path string) (*AgentPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeAgentPlan(data)
}

func SaveAgentPlan(path string, plan *AgentPlan) error {
	if plan == nil {
		return fmt.Errorf("agent plan is nil")
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeDiscoveryFile(path, data, 0o644)
}

func DecodeAgentPlan(data []byte) (*AgentPlan, error) {
	candidates := [][]byte{bytes.TrimSpace(data)}
	candidates = append(candidates, fencedJSONBlocks(string(data))...)

	var firstErr error
	for _, candidate := range candidates {
		candidate = bytes.TrimSpace(candidate)
		if len(candidate) == 0 {
			continue
		}
		var plan AgentPlan
		if err := json.Unmarshal(candidate, &plan); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !plan.HasContent() {
			if firstErr == nil {
				firstErr = fmt.Errorf("agent plan JSON did not contain usable discovery fields")
			}
			continue
		}
		return &plan, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("agent plan JSON was not found")
}

func ExtractAgentPlanFile(markdownPath, jsonPath string) (*AgentPlan, error) {
	data, err := os.ReadFile(markdownPath)
	if err != nil {
		return nil, err
	}
	plan, err := DecodeAgentPlan(data)
	if err != nil {
		return nil, err
	}
	if err := SaveAgentPlan(jsonPath, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func fencedJSONBlocks(markdown string) [][]byte {
	var blocks [][]byte
	var current strings.Builder
	inBlock := false
	jsonBlock := false

	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inBlock {
				info := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
				jsonBlock = info == "" || strings.HasPrefix(info, "json")
				inBlock = true
				current.Reset()
				continue
			}
			if jsonBlock {
				blocks = append(blocks, []byte(current.String()))
			}
			inBlock = false
			jsonBlock = false
			current.Reset()
			continue
		}
		if inBlock && jsonBlock {
			current.WriteString(line)
			current.WriteByte('\n')
		}
	}
	return blocks
}

func AgentPlanPath(planDir string) string {
	return filepath.Join(planDir, AgentPlanFilename)
}
