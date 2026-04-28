package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/Automattic/code-crucible/internal/agent"
)

func checkProviderExecutable(provider agent.ProviderDefinition, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("agent provider %q has no command to execute", provider.Name)
	}
	binary := strings.TrimSpace(command[0])
	if binary == "" {
		return fmt.Errorf("agent provider %q has an empty executable path", provider.Name)
	}
	if _, err := exec.LookPath(binary); err == nil {
		return nil
	}
	if provider.Kind == "codex" {
		return fmt.Errorf("agent provider %q executable %q was not found or is not executable; install the Codex CLI, add it to PATH, pass --codex-bin, or configure another generation-capable provider", provider.Name, binary)
	}
	return fmt.Errorf("agent provider %q executable %q was not found or is not executable; fix .crucible/config.json or configure another generation-capable provider", provider.Name, binary)
}
