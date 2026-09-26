package cli

import (
	"fmt"
	"runtime"
	"time"

	"github.com/akunzai/skills-manager/internal/tui"
	"github.com/akunzai/skills-manager/internal/updater"
	"github.com/spf13/cobra"
)

var (
	selfUpdateNoticeIsTerminal = tui.IsTerminal
	selfUpdateNoticeCheck      = func() (*updater.SelfUpdateInfo, error) {
		return updater.CheckSelfUpdateWithTimeout("", updater.NoticeCheckTimeoutSec)
	}
	// selfUpdateNoticeExecutablePath is a seam over
	// updater.GetCurrentExecutablePath so tests can simulate a Homebrew or
	// Scoop install without a real one.
	selfUpdateNoticeExecutablePath = updater.GetCurrentExecutablePath
	selfUpdateNoticeCmd            *cobra.Command
)

func maybeNotifySelfUpdate(cmd *cobra.Command) {
	jsonOutput := false
	if flag, err := cmd.Flags().GetBool("json"); err == nil {
		jsonOutput = flag
	}
	last := updater.ReadCheckStamp(updater.CheckStampPath())
	if !updater.AllowSelfUpdateNotice(updater.SkipSelfUpdateCheck(), selfUpdateNoticeIsTerminal(), jsonOutput, cmd.Name(), time.Now(), last) {
		return
	}
	info, err := selfUpdateNoticeCheck()
	_ = updater.WriteCheckStamp(updater.CheckStampPath(), time.Now())
	if err != nil || info == nil || !info.UpdateAvailable {
		return
	}
	command := "skills self-update"
	if pkgMgr := updater.ClassifyExecutablePath(selfUpdateNoticeExecutablePath(), runtime.GOOS); pkgMgr != nil {
		command = pkgMgr.Command
	}
	fmt.Fprintln(cmd.ErrOrStderr(), updater.NoticeLine(info.LatestVersion, command))
}
