package tui_test

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/akunzai/skills-manager/internal/tui"
)

func TestPromptMousePTY(t *testing.T) {
	if os.Getenv("SKILLS_MOUSE_HELPER") == "1" {
		switch os.Getenv("SKILLS_MOUSE_SCENARIO") {
		case "collapse":
			groups := make(tui.GroupedItems)
			for i := range 5 {
				groups[fmt.Sprintf("g%d", i)] = []tui.SelectOption{{Key: fmt.Sprintf("g%d-skill", i)}}
			}
			groups["g3"] = nil
			for i := range 8 {
				groups["g3"] = append(groups["g3"], tui.SelectOption{Key: fmt.Sprintf("child-%02d", i)})
			}
			selected, err := tui.PromptGroupedMultiSelect("Choose skills", groups)
			fmt.Printf("RESULT:%v:%v\n", selected, err)
			return
		case "single":
			selected, err := tui.PromptSelect("Choose scope", []tui.SelectOption{{Key: "alpha"}, {Key: "beta"}}, 0)
			fmt.Printf("RESULT:%v:%v\n", selected, err)
			return
		case "wheel":
			var items []tui.SelectOption
			for i := range 10 {
				items = append(items, tui.SelectOption{Key: fmt.Sprintf("item-%02d", i)})
			}
			selected, err := tui.PromptMultiSelect("Choose skills", items)
			fmt.Printf("RESULT:%v:%v\n", selected, err)
			return
		}
		if os.Getenv("SKILLS_MOUSE_SCENARIO") == "group" {
			selected, err := tui.PromptGroupedMultiSelect("Choose skills", tui.GroupedItems{"fixture": {{Key: "master"}, {Key: "child", DependsOn: "master"}, {Key: "beta"}}})
			fmt.Printf("RESULT:%v:%v\n", selected, err)
			return
		}
		selected, err := tui.PromptMultiSelect("Choose skills", []tui.SelectOption{{Key: "alpha"}, {Key: "beta"}})
		fmt.Printf("RESULT:%v:%v\n", selected, err)
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("PTY driver requires POSIX")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	for _, scenario := range []string{"multi", "group", "resize", "single", "wheel", "events", "escape", "timeout", "tiny", "collapse"} {
		t.Run(scenario, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), python, "testdata/mouse_pty.py", os.Args[0], scenario)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("PTY prompt: %v\n%s", err, out)
			}
		})
	}
}
