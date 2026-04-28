package evaluator

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const SemanticChecksFilename = "semantic-checks.json"

type SemanticChecks struct {
	Version int             `json:"version"`
	Checks  []SemanticCheck `json:"checks"`
}

type SemanticCheck struct {
	Name            string `json:"name,omitempty"`
	Command         string `json:"command"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
	MaxOutputBytes  int    `json:"max_output_bytes,omitempty"`
	CompareExitCode *bool  `json:"compare_exit_code,omitempty"`
	CompareStdout   *bool  `json:"compare_stdout,omitempty"`
	CompareStderr   *bool  `json:"compare_stderr,omitempty"`
}

func LoadSemanticChecks(path string) (*SemanticChecks, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var checks SemanticChecks
	if err := json.Unmarshal(data, &checks); err != nil {
		return nil, err
	}
	if checks.Version != 0 && checks.Version != 1 {
		return nil, fmt.Errorf("unsupported semantic checks version %d", checks.Version)
	}
	for i, check := range checks.Checks {
		if strings.TrimSpace(check.Command) == "" {
			return nil, fmt.Errorf("semantic check %d command is required", i+1)
		}
	}
	if checks.Version == 0 {
		checks.Version = 1
	}
	return &checks, nil
}
