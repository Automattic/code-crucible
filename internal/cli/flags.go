package cli

import "flag"

func projectDirFlag(fs *flag.FlagSet, usage string) *string {
	var projectDir string
	fs.StringVar(&projectDir, "project", ".", usage)
	fs.StringVar(&projectDir, "project-dir", ".", usage+" (alias for --project)")
	return &projectDir
}
