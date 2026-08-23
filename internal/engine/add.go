package engine

import (
	"fmt"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

const (
	AddRemote  = "remote"
	AddSymlink = "symlink"
	AddCommand = "command"
)

// AddSource is the Source add selected Skills from. CLI owns discovery and
// display verbs; this is what records Config and Materializes.
type AddSource struct {
	Kind          string
	RepoDir       string
	SourceKey     string
	RepoType      string
	URL           string
	AbsSourcePath string
	Description   string
	Command       string
	Check         string
}

func (s AddSource) resolvedPath(subpath string) string {
	if subpath != "" && subpath != "." {
		return filepath.Join(s.AbsSourcePath, filepath.FromSlash(subpath))
	}
	return s.AbsSourcePath
}

func recordAdded(cfg *config.Config, src AddSource, name, subpath, skillsDir string) {
	switch src.Kind {
	case AddSymlink:
		config.AddLocalSymlinkEntry(cfg, name, models.StoreLocalSourcePath(src.resolvedPath(subpath), skillsDir), src.Description)
	case AddCommand:
		config.AddLocalCommandEntry(cfg, name, src.Command, src.Check, src.Description)
	default:
		config.AddRemoteSkillEntry(cfg, src.SourceKey, name, subpath, src.RepoType, src.URL)
	}
}

// AddDeclared records the selected Skills into Config, saves, then
// Materializes only those Skills and applies Availability. Copy/symlink/
// command/check failures are returned after that skill's step; Availability
// still applies when a command installer fails.
func AddDeclared(
	cfg *config.Config,
	configPath, skillsDir string,
	src AddSource,
	skills map[string]string,
	agents []string,
	progress func(name, subpath string),
) error {
	names := sortedSkillKeys(skills)
	for _, name := range names {
		recordAdded(cfg, src, name, skills[name], skillsDir)
		if len(agents) > 0 {
			if err := config.IncludeSkillAgents(cfg, name, agents...); err != nil {
				return err
			}
		}
	}
	if err := config.SaveConfig(cfg, configPath); err != nil {
		return err
	}

	var stepErr error
	note := func(ev SyncEvent) {
		switch ev.Kind {
		case SyncCheckFailed:
			stepErr = fmt.Errorf("command check %q failed, skipping %s", ev.Path, ev.Skill)
		case SyncCommandFailed:
			stepErr = fmt.Errorf("%s", ev.Err)
		case SyncSymlinkFailed:
			stepErr = fmt.Errorf("%s", ev.Err)
		case SyncCopyFailed:
			stepErr = fmt.Errorf("%s", ev.Err)
		case SyncPathMissing:
			stepErr = fmt.Errorf("path missing in repository: %s", ev.Path)
		}
	}

	for _, name := range names {
		subpath := skills[name]
		if progress != nil {
			progress(name, subpath)
		}
		stepErr = nil
		var err error
		switch src.Kind {
		case AddSymlink:
			err = reconcileLocalSymlink(cfg, name, skillsDir, false, note)
		case AddCommand:
			err = reconcileCommand(cfg, name, skillsDir, false, note)
		default:
			one := map[string]string{name: subpath}
			err = reconcileRemoteSource(cfg, src.SourceKey, one, src.RepoDir, skillsDir, false, one, note)
		}
		if err != nil {
			return fmt.Errorf("saved config but failed to apply availability for %s: %w", name, err)
		}
		if stepErr != nil {
			return fmt.Errorf("failed to materialize skill %s: %w", name, stepErr)
		}
	}
	return nil
}
