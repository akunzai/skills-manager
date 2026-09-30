package cli_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/akunzai/skills-manager/internal/cli"
)

func TestCLINoMousePTY(t *testing.T) {
	if fixture := os.Getenv("SKILLS_CLI_MOUSE_FIXTURE"); fixture != "" {
		os.Args = []string{"skills", "rm", "--no-mouse", "--config", filepath.Join(fixture, "skills.json"), "--skills-dir", filepath.Join(fixture, "skills"), "--cache-dir", filepath.Join(fixture, "cache")}
		fmt.Printf("RESULT:%v\n", cli.Execute())
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("PTY driver requires POSIX")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	cmd := exec.CommandContext(t.Context(), python, "testdata/no_mouse_pty.py", os.Args[0])
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("CLI optout: %v\n%s", err, out)
	}
}
