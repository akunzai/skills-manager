package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/updater"
)

func init() {
	// Never the real path: it must not accidentally resolve under a Cellar
	// or scoop/apps directory on a machine that has this CLI installed via
	// a package manager.
	selfUpdateExecutablePath = func() string { return "/home/tester/.local/bin/skills" }
}

// TestSelfUpdateRefusesUnderHomebrew covers the acceptance criterion: with
// the executable resolved under a Homebrew Cellar directory, self-update
// downloads nothing, prints the brew command, and exits non-zero.
func TestSelfUpdateRefusesUnderHomebrew(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	stubSelfUpdateExecutablePath(t, "/opt/homebrew/Cellar/skills-manager/0.18.0/bin/skills")
	stubSelfUpdateCheckMustNotBeCalled(t)

	out, err := runCLI(t, "self-update")
	if err == nil {
		t.Fatal("expected self-update to refuse under a Homebrew install")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("ExitCode = %d; want 1", ExitCode(err))
	}
	if !strings.Contains(out, updater.HomebrewUpgradeCommand) {
		t.Fatalf("output = %q; want it to name %q", out, updater.HomebrewUpgradeCommand)
	}
}

// TestSelfUpdateRefusesUnderScoop covers the Scoop half of the same
// acceptance criterion.
func TestSelfUpdateRefusesUnderScoop(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	stubSelfUpdateExecutablePath(t, `C:\Users\alice\scoop\apps\skills-manager\current\skills.exe`)
	stubSelfUpdateGOOS(t, "windows")
	stubSelfUpdateCheckMustNotBeCalled(t)

	out, err := runCLI(t, "self-update")
	if err == nil {
		t.Fatal("expected self-update to refuse under a Scoop install")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("ExitCode = %d; want 1", ExitCode(err))
	}
	if !strings.Contains(out, updater.ScoopUpgradeCommand) {
		t.Fatalf("output = %q; want it to name %q", out, updater.ScoopUpgradeCommand)
	}
}

// TestSelfUpdateRefusalIsJSON covers the --json shape of the same refusal.
func TestSelfUpdateRefusalIsJSON(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	stubSelfUpdateExecutablePath(t, "/opt/homebrew/Cellar/skills-manager/0.18.0/bin/skills")
	stubSelfUpdateCheckMustNotBeCalled(t)

	out, err := runCLI(t, "self-update", "--json")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("err = %v; want ExitCode 1", err)
	}
	var payload map[string]string
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jsonErr, out)
	}
	if payload["command"] != updater.HomebrewUpgradeCommand {
		t.Fatalf("payload[command] = %q; want %q", payload["command"], updater.HomebrewUpgradeCommand)
	}
	if payload["package_manager"] != "Homebrew" {
		t.Fatalf("payload[package_manager] = %q; want Homebrew", payload["package_manager"])
	}
}

// TestSelfUpdateCheckStillReachesNetworkUnderPackageManager covers the
// acceptance criterion that --check still reports availability and names
// the package-manager command, unlike a plain self-update.
func TestSelfUpdateCheckStillReachesNetworkUnderPackageManager(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	stubSelfUpdateExecutablePath(t, "/opt/homebrew/Cellar/skills-manager/0.18.0/bin/skills")
	checked := false
	stubSelfUpdateCheck(t, func(string) (*updater.SelfUpdateInfo, error) {
		checked = true
		return &updater.SelfUpdateInfo{
			CurrentVersion:  "0.18.0",
			LatestVersion:   "0.19.0",
			LatestTag:       "v0.19.0",
			UpdateAvailable: true,
		}, nil
	})

	out, err := runCLI(t, "self-update", "--check")
	if err != nil {
		t.Fatalf("self-update --check: %v\n%s", err, out)
	}
	if !checked {
		t.Fatal("--check must still reach the network check under a package manager install")
	}
	if !strings.Contains(out, updater.HomebrewUpgradeCommand) {
		t.Fatalf("output = %q; want it to name %q", out, updater.HomebrewUpgradeCommand)
	}
	if strings.Contains(out, "skills self-update") {
		t.Fatalf("output = %q; must not suggest self-update under a package manager install", out)
	}
}

// TestSelfUpdateUnaffectedElsewhere covers the acceptance criterion that
// installs anywhere else behave exactly as today.
func TestSelfUpdateUnaffectedElsewhere(t *testing.T) {
	resetSubcommandFlags()
	t.Cleanup(resetSubcommandFlags)
	stubSelfUpdateExecutablePath(t, "/home/alice/.local/bin/skills")
	checked := false
	stubSelfUpdateCheck(t, func(string) (*updater.SelfUpdateInfo, error) {
		checked = true
		return &updater.SelfUpdateInfo{
			CurrentVersion:  "0.18.0",
			LatestVersion:   "0.19.0",
			LatestTag:       "v0.19.0",
			UpdateAvailable: true,
		}, nil
	})

	out, err := runCLI(t, "self-update", "--check")
	if err != nil {
		t.Fatalf("self-update --check: %v\n%s", err, out)
	}
	if !checked {
		t.Fatal("--check must reach the network check")
	}
	if !strings.Contains(out, "skills self-update") {
		t.Fatalf("output = %q; want the usual self-update suggestion", out)
	}
}

func stubSelfUpdateExecutablePath(t *testing.T, path string) {
	t.Helper()
	old := selfUpdateExecutablePath
	selfUpdateExecutablePath = func() string { return path }
	t.Cleanup(func() { selfUpdateExecutablePath = old })
}

func stubSelfUpdateGOOS(t *testing.T, goos string) {
	t.Helper()
	old := selfUpdateGOOS
	selfUpdateGOOS = goos
	t.Cleanup(func() { selfUpdateGOOS = old })
}

func stubSelfUpdateCheck(t *testing.T, check func(string) (*updater.SelfUpdateInfo, error)) {
	t.Helper()
	old := selfUpdateCheck
	selfUpdateCheck = check
	t.Cleanup(func() { selfUpdateCheck = old })
}

func stubSelfUpdateCheckMustNotBeCalled(t *testing.T) {
	t.Helper()
	stubSelfUpdateCheck(t, func(string) (*updater.SelfUpdateInfo, error) {
		t.Fatal("self-update must not reach the network when a package manager owns this install")
		return nil, nil
	})
}
