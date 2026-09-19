package cli

import (
	"fmt"
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
	selfUpdateNoticeCmd *cobra.Command
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
	fmt.Fprintln(cmd.ErrOrStderr(), updater.NoticeLine(info.LatestVersion))
}
