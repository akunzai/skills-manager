package cli

import (
	"encoding/json"
	"errors"
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

// --version names a release, not the latest one, so the output says so and
// a release other than the running one is offered, a downgrade included.
func TestSelfUpdateCheckWithAPinnedVersion(t *testing.T) {
	stubSelfUpdateExecutablePath(t, "/home/alice/.local/bin/skills")
	for _, tc := range []struct {
		name, latest string
		update       bool
		want         string
	}{
		{"older release", "0.18.0", true, "Update available: 0.19.0 -> v0.18.0"},
		{"running release", "0.19.0", false, "skills is already on v0.19.0."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubSelfUpdateCheck(t, func(string) (*updater.SelfUpdateInfo, error) {
				return &updater.SelfUpdateInfo{CurrentVersion: "0.19.0", LatestVersion: tc.latest, LatestTag: "v" + tc.latest, UpdateAvailable: tc.update}, nil
			})

			out, err := runCLI(t, "self-update", "--check", "--version", "v"+tc.latest)
			if err != nil {
				t.Fatalf("self-update --check --version: %v\n%s", err, out)
			}
			if !strings.Contains(out, "Target release:  v"+tc.latest) || strings.Contains(out, "Latest release") {
				t.Fatalf("output = %q; want the pinned release labelled as the target", out)
			}
			if !strings.Contains(out, tc.want) || strings.Contains(out, "ahead of latest release") {
				t.Fatalf("output = %q; want %q", out, tc.want)
			}
		})
	}
}

func stubSelfUpdateInstall(t *testing.T, install func(assetURL, checksumsURL, targetPath string, timeoutSec int) (string, error)) {
	t.Helper()
	old := selfUpdateInstall
	selfUpdateInstall = install
	t.Cleanup(func() { selfUpdateInstall = old })
}

// --json keeps stdout for one document whatever self-update ends up doing
// with an available release: installing it, previewing it, or failing to.
func TestSelfUpdateJSONIsOneDocument(t *testing.T) {
	stubSelfUpdateExecutablePath(t, "/home/alice/.local/bin/skills")
	for _, tc := range []struct {
		name       string
		args       []string
		noAsset    bool
		installErr error
		wantStatus string
		wantErr    bool
		installs   bool
	}{
		{name: "installed", args: []string{"--json"}, wantStatus: "updated", installs: true},
		{name: "dry run", args: []string{"--json", "--dry-run"}, wantStatus: "dry_run"},
		{name: "install failed", args: []string{"--json"}, installErr: errors.New("checksum mismatch"), wantStatus: "error", wantErr: true, installs: true},
		{name: "no compatible asset", args: []string{"--json"}, noAsset: true, wantStatus: "error", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubSelfUpdateCheck(t, func(string) (*updater.SelfUpdateInfo, error) {
				info := &updater.SelfUpdateInfo{CurrentVersion: "0.18.0", LatestVersion: "0.19.0", LatestTag: "v0.19.0", UpdateAvailable: true, AssetURL: "https://example.invalid/skills.tar.gz"}
				if tc.noAsset {
					info.AssetURL = ""
				}
				return info, nil
			})
			installed := false
			stubSelfUpdateInstall(t, func(_, _, targetPath string, _ int) (string, error) {
				installed = true
				return targetPath, tc.installErr
			})

			out, err := runCLI(t, append([]string{"self-update"}, tc.args...)...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("self-update %v: err = %v\n%s", tc.args, err, out)
			}
			if tc.wantErr && ExitCode(err) != 2 {
				t.Fatalf("exit code = %d; want 2", ExitCode(err))
			}
			if installed != tc.installs {
				t.Fatalf("installed = %v; want %v", installed, tc.installs)
			}
			var doc map[string]any
			decoder := json.NewDecoder(strings.NewReader(out))
			if err := decoder.Decode(&doc); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, out)
			}
			if decoder.More() {
				t.Fatalf("stdout holds more than one JSON document:\n%s", out)
			}
			if doc["status"] != tc.wantStatus {
				t.Fatalf("status = %v; want %s\n%s", doc["status"], tc.wantStatus, out)
			}
			// A failure says why; anything else reports the release it checked.
			if tc.wantErr && (doc["error"] == nil || doc["error"] == "") {
				t.Fatalf("error document has no error:\n%s", out)
			}
			if !tc.wantErr && doc["latest_tag"] != "v0.19.0" {
				t.Fatalf("latest_tag = %v; want v0.19.0\n%s", doc["latest_tag"], out)
			}
		})
	}
}
