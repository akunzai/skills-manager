package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/akunzai/skills-manager/internal/models"
)

const SkipSelfUpdateCheckEnv = "SKILLS_SKIP_SELF_UPDATE_CHECK"

const NoticeCheckTimeoutSec = 2

const noticeCheckInterval = 24 * time.Hour

func NoticeLine(latestVersion string) string {
	return fmt.Sprintf("Self-update %s is available. Run: skills self-update", latestVersion)
}

func SkipSelfUpdateCheck() bool {
	return os.Getenv(SkipSelfUpdateCheckEnv) != ""
}

func AllowSelfUpdateNotice(skipEnv, terminal, jsonOutput bool, command string, now, last time.Time) bool {
	if skipEnv || !terminal || jsonOutput || command == "self-update" {
		return false
	}
	if last.IsZero() {
		return true
	}
	return now.Sub(last) >= noticeCheckInterval
}

func CheckStampPath() string {
	stateHome := models.ResolveEnvPath("XDG_STATE_HOME", "~/.local/state")
	return filepath.Join(stateHome, "skills-manager", "self-update-check")
}

func ReadCheckStamp(path string) time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	unix, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}

func WriteCheckStamp(path string, at time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.FormatInt(at.Unix(), 10)+"\n"), 0o644)
}
