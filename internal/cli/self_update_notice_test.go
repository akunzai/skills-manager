package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/updater"
)

func init() {
	selfUpdateNoticeIsTerminal = func() bool { return false }
	selfUpdateNoticeCheck = func() (*updater.SelfUpdateInfo, error) {
		return nil, errors.New("self-update notice check not stubbed")
	}
}

func TestSelfUpdateNoticePrintsOnTTY(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	stubSelfUpdateNotice(t, true, func() (*updater.SelfUpdateInfo, error) {
		return &updater.SelfUpdateInfo{LatestVersion: "0.13.0", UpdateAvailable: true}, nil
	})

	var stdout, stderr bytes.Buffer
	RootCmd.SetOut(&stdout)
	RootCmd.SetErr(&stderr)
	RootCmd.SetArgs([]string{"version"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	wantOut := fmt.Sprintf("skills-manager %s\n", updater.Version)
	if stdout.String() != wantOut {
		t.Fatalf("stdout = %q; want %q", stdout.String(), wantOut)
	}
	wantErr := updater.NoticeLine("0.13.0") + "\n"
	if stderr.String() != wantErr {
		t.Fatalf("stderr = %q; want %q", stderr.String(), wantErr)
	}

	stderr.Reset()
	RootCmd.SetArgs([]string{"version"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("second version: %v", err)
	}
	if stderr.String() != "" {
		t.Fatalf("second run printed notice again: %q", stderr.String())
	}
}

func TestSelfUpdateNoticeSkipsJSON(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	checked := false
	stubSelfUpdateNotice(t, true, func() (*updater.SelfUpdateInfo, error) {
		checked = true
		return &updater.SelfUpdateInfo{LatestVersion: "0.13.0", UpdateAvailable: true}, nil
	})

	configFile, skillsDir := homeAgents(t, home)
	var stdout, stderr bytes.Buffer
	RootCmd.SetOut(&stdout)
	RootCmd.SetErr(&stderr)
	RootCmd.SetArgs([]string{"ls", "--json", "--config", configFile, "--skills-dir", skillsDir})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	if checked {
		t.Fatal("checked for Self-update despite --json")
	}
	if strings.Contains(stderr.String(), "Self-update") {
		t.Fatalf("stderr noticed despite --json: %q", stderr.String())
	}
}

func TestSelfUpdateNoticeSkipsSelfUpdateCommand(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	checked := false
	stubSelfUpdateNotice(t, true, func() (*updater.SelfUpdateInfo, error) {
		checked = true
		return &updater.SelfUpdateInfo{LatestVersion: "0.13.0", UpdateAvailable: true}, nil
	})

	cmd, _, err := RootCmd.Find([]string{"self-update"})
	if err != nil {
		t.Fatal(err)
	}
	maybeNotifySelfUpdate(cmd)
	if checked {
		t.Fatal("checked for Self-update on self-update itself")
	}
}

func TestSelfUpdateNoticeSkipsEnv(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	t.Setenv(updater.SkipSelfUpdateCheckEnv, "1")
	checked := false
	stubSelfUpdateNotice(t, true, func() (*updater.SelfUpdateInfo, error) {
		checked = true
		return &updater.SelfUpdateInfo{LatestVersion: "0.13.0", UpdateAvailable: true}, nil
	})

	var stderr bytes.Buffer
	RootCmd.SetOut(&bytes.Buffer{})
	RootCmd.SetErr(&stderr)
	RootCmd.SetArgs([]string{"version"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if checked {
		t.Fatal("checked for Self-update despite skip env")
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q; want empty", stderr.String())
	}
}

func TestSelfUpdateNoticeSkipsNonTTY(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	checked := false
	stubSelfUpdateNotice(t, false, func() (*updater.SelfUpdateInfo, error) {
		checked = true
		return &updater.SelfUpdateInfo{LatestVersion: "0.13.0", UpdateAvailable: true}, nil
	})

	var stderr bytes.Buffer
	RootCmd.SetOut(&bytes.Buffer{})
	RootCmd.SetErr(&stderr)
	RootCmd.SetArgs([]string{"version"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if checked {
		t.Fatal("checked for Self-update on non-TTY")
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q; want empty", stderr.String())
	}
}

func TestSelfUpdateNoticeCheckFailureLeavesCommandOk(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	stubSelfUpdateNotice(t, true, func() (*updater.SelfUpdateInfo, error) {
		return nil, errors.New("github down")
	})

	var stdout, stderr bytes.Buffer
	RootCmd.SetOut(&stdout)
	RootCmd.SetErr(&stderr)
	RootCmd.SetArgs([]string{"version"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("version failed after notice check error: %v", err)
	}
	if stdout.String() != fmt.Sprintf("skills-manager %s\n", updater.Version) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "Self-update") {
		t.Fatalf("printed notice after failed check: %q", stderr.String())
	}

	stubSelfUpdateNotice(t, true, func() (*updater.SelfUpdateInfo, error) {
		return &updater.SelfUpdateInfo{LatestVersion: "0.13.0", UpdateAvailable: true}, nil
	})
	stderr.Reset()
	RootCmd.SetArgs([]string{"version"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("second version: %v", err)
	}
	if stderr.String() != "" {
		t.Fatalf("retried check after a failed attempt: %q", stderr.String())
	}
}

func TestSelfUpdateNoticeSilentWhenCurrent(t *testing.T) {
	resetRootCmdFlags()
	isolateHome(t)
	stubSelfUpdateNotice(t, true, func() (*updater.SelfUpdateInfo, error) {
		return &updater.SelfUpdateInfo{LatestVersion: updater.Version, UpdateAvailable: false}, nil
	})

	var stderr bytes.Buffer
	RootCmd.SetOut(&bytes.Buffer{})
	RootCmd.SetErr(&stderr)
	RootCmd.SetArgs([]string{"version"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if stderr.String() != "" {
		t.Fatalf("printed notice when current: %q", stderr.String())
	}
}

func TestSelfUpdateNoticeAfterFailedCommand(t *testing.T) {
	resetRootCmdFlags()
	home := isolateHome(t)
	stubSelfUpdateNotice(t, true, func() (*updater.SelfUpdateInfo, error) {
		return &updater.SelfUpdateInfo{LatestVersion: "0.13.0", UpdateAvailable: true}, nil
	})
	configFile, _ := homeAgents(t, home)

	var stderr bytes.Buffer
	RootCmd.SetOut(&bytes.Buffer{})
	RootCmd.SetErr(&stderr)
	RootCmd.SetArgs([]string{"init", "--config", configFile})
	if err := Execute(); err == nil {
		t.Fatal("expected init to fail when config exists")
	}
	wantErr := updater.NoticeLine("0.13.0") + "\n"
	if stderr.String() != wantErr {
		t.Fatalf("stderr = %q; want %q", stderr.String(), wantErr)
	}
}

func stubSelfUpdateNotice(t *testing.T, terminal bool, check func() (*updater.SelfUpdateInfo, error)) {
	t.Helper()
	oldTerm, oldCheck := selfUpdateNoticeIsTerminal, selfUpdateNoticeCheck
	selfUpdateNoticeIsTerminal = func() bool { return terminal }
	selfUpdateNoticeCheck = check
	t.Cleanup(func() {
		selfUpdateNoticeIsTerminal, selfUpdateNoticeCheck = oldTerm, oldCheck
	})
}

func homeAgents(t *testing.T, home string) (configFile, skillsDir string) {
	t.Helper()
	configFile = filepath.Join(home, ".agents", "skills.json")
	skillsDir = filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfig(config.DefaultConfig(), configFile); err != nil {
		t.Fatal(err)
	}
	return configFile, skillsDir
}
