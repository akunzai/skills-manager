package engine

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	unsetRepositoryGitEnv()
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

// unsetRepositoryGitEnv clears the variables git uses to locate a repository
// (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE and the rest git itself lists).
// Run from `git rebase --exec` or a hook, the tests inherit them, and every
// fixture's git command then writes into the developer's repository instead
// of its temporary one: its config, commits and tags.
func unsetRepositoryGitEnv() {
	names, err := exec.Command("git", "rev-parse", "--local-env-vars").Output()
	if err != nil {
		panic(err)
	}
	for name := range strings.FieldsSeq(string(names)) {
		os.Unsetenv(name)
	}
}

// setGitConfig adds one git config entry for a test on top of TestMain's.
// Setting GIT_CONFIG_COUNT directly would drop TestMain's entries.
func setGitConfig(t *testing.T, key, value string) {
	t.Helper()
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	t.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", n), key)
	t.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", n), value)
	t.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n+1))
}
