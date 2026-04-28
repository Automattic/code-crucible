package main

import (
	"os"

	"github.com/Automattic/code-crucible/internal/cli"
)

func main() {
	os.Exit(cli.RunWithIO(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
