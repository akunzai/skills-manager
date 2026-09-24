package engine

import (
	"os"
	"testing"
)

// TestMain points XDG_STATE_HOME at a throwaway directory for the whole
// package, so a test that forgets t.Setenv cannot write Scope state for its
// temporary Scope into the developer's real state directory, where doctor
// then reports it as a missing path.
func TestMain(m *testing.M) {
	state, err := os.MkdirTemp("", "skills-manager-test-state-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_STATE_HOME", state)
	code := m.Run()
	os.RemoveAll(state)
	os.Exit(code)
}
