// Command at-include flattens @<path> Markdown imports the way Claude Code does
// when it reads a CLAUDE.md file.
package main

import (
	"os"

	"github.com/happycollision/at-include/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
