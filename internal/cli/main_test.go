package cli

import (
	"os"
	"testing"
)

// TestMain points XDG_STATE_HOME at a throwaway directory for the whole
// package, so a test that forgets t.Setenv cannot write Scope state for its
// temporary Scope into the developer's real state directory, where doctor
// then reports it as a missing path.
//
// It also hides the developer's global and system git config and stops git
// guessing an identity from the machine, so tests see git the way CI does: a
// fixture that commits or tags without its own identity fails here instead of
// only in CI.
func TestMain(m *testing.M) {
	state, err := os.MkdirTemp("", "skills-manager-test-state-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", state)
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// core.autocrlf=false: with the global config hidden, a Windows runner's
	// autocrlf=true would otherwise check LF fixtures out as CRLF, and every
	// LF-written Skill would read as drift.
	os.Setenv("GIT_CONFIG_COUNT", "2")
	os.Setenv("GIT_CONFIG_KEY_0", "user.useConfigOnly")
	os.Setenv("GIT_CONFIG_VALUE_0", "true")
	os.Setenv("GIT_CONFIG_KEY_1", "core.autocrlf")
	os.Setenv("GIT_CONFIG_VALUE_1", "false")
	code := m.Run()
	os.RemoveAll(state)
	os.Exit(code)
}
