package main

import (
	"fmt"
	"os"

	"github.com/akunzai/skills-manager/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		code := cli.ExitCode(err)
		if code != 1 {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(code)
	}
}
