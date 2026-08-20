package engine

import (
	"fmt"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
)

type HookResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func ExecutePostHooks(postHooks []config.PostHook, dryRun bool) []HookResult {
	results := make([]HookResult, 0, len(postHooks))

	for _, hook := range postHooks {
		name := hook.Name
		if name == "" {
			name = "unnamed-hook"
		}
		desc := hook.Description
		cond := hook.Condition
		cmd := hook.Run

		if strings.TrimSpace(cmd) == "" {
			continue
		}

		if cond != "" {
			_, _, err := RunCmd(cond, "")
			if err != nil {
				results = append(results, HookResult{
					Name:    name,
					Success: true,
					Message: fmt.Sprintf("Condition not met (%s), skipped", cond),
				})
				continue
			}
		}

		if dryRun {
			results = append(results, HookResult{
				Name:    name,
				Success: true,
				Message: fmt.Sprintf("[Dry-run] Would execute: %s", cmd),
			})
			continue
		}

		stdout, stderr, err := RunCmd(cmd, "")
		if err != nil {
			errMsg := stderr
			if errMsg == "" {
				errMsg = stdout
			}
			if errMsg == "" {
				errMsg = err.Error()
			}
			results = append(results, HookResult{
				Name:    name,
				Success: false,
				Message: fmt.Sprintf("Failed: %s", errMsg),
			})
		} else {
			msg := desc
			if msg == "" {
				msg = cmd
			}
			results = append(results, HookResult{
				Name:    name,
				Success: true,
				Message: fmt.Sprintf("Success: %s", msg),
			})
		}
	}

	return results
}
