package engine

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// AddSourceKind represents the kind of Source being added.
type AddSourceKind string

const (
	AddSourceRemote  AddSourceKind = "remote"
	AddSourceSymlink AddSourceKind = "symlink"
	AddSourceCommand AddSourceKind = "command"
)

// AddSource describes where a Skill is obtained and how it should be configured.
type AddSource struct {
	Kind        AddSourceKind
	Key         string // repository source key, local directory path, or command
	RepoDir     string // cached git repository directory
	RepoType    string // "github", "gitlab", "git"
	URL         string // remote clone URL
	LocalPath   string // absolute path to local skill directory
	Command     string // installer command
	Check       string // command pre-check
	Description string // description of the skill
}

func NewRemoteAddSource(key, repoType, url, repoDir string) AddSource {
	return AddSource{
		Kind:     AddSourceRemote,
		Key:      key,
		RepoType: repoType,
		URL:      url,
		RepoDir:  repoDir,
	}
}

func NewSymlinkAddSource(absPath, description string) AddSource {
	return AddSource{
		Kind:        AddSourceSymlink,
		Key:         absPath,
		LocalPath:   absPath,
		Description: description,
	}
}

func NewCommandAddSource(command, check, description string) AddSource {
	return AddSource{
		Kind:        AddSourceCommand,
		Key:         command,
		Command:     command,
		Check:       check,
		Description: description,
	}
}

func (s AddSource) proposedDisplay(subpath, skillsDir string) string {
	switch s.Kind {
	case AddSourceCommand:
		return fmt.Sprintf("[command] %s", s.Command)
	case AddSourceSymlink:
		p := s.LocalPath
		if subpath != "" && subpath != "." {
			p = filepath.Join(s.LocalPath, filepath.FromSlash(subpath))
		}
		return fmt.Sprintf("[symlink] %s", models.ToTildePath(p))
	default:
		if subpath != "" && subpath != "." {
			return fmt.Sprintf("[remote] %s (%s)", s.Key, subpath)
		}
		return fmt.Sprintf("[remote] %s", s.Key)
	}
}

// AddConflict records an existing skill declaration or filesystem entry that
// will be replaced by the Add plan.
type AddConflict struct {
	Skill       string
	CurrentSrc  string
	ProposedSrc string
}

// AddPlan is the calculated set of Skills to record in Config, Materialize, and link.
type AddPlan struct {
	Source     AddSource
	Skills     map[string]string // skill name -> subpath
	Conflicts  []AddConflict
	Agents     []string // explicit agent overrides to include
	ConfigPath string
	SkillsDir  string
}

// BuildAddPlan inspects existing Config and filesystem Inventory to calculate
// skill replacements and build an AddPlan.
func BuildAddPlan(
	cfg *config.Config,
	configPath string,
	skillsDir string,
	source AddSource,
	skills map[string]string,
	agents []string,
) AddPlan {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if skillsDir == "" {
		skillsDir = models.DefaultSkillsDir()
	}
	if configPath == "" {
		configPath = models.DefaultConfigFile()
	}

	plan := AddPlan{
		Source:     source,
		Skills:     skills,
		Agents:     agents,
		ConfigPath: configPath,
		SkillsDir:  skillsDir,
	}

	for _, name := range sortedSkillKeys(skills) {
		subpath := skills[name]
		newSrcDisplay := source.proposedDisplay(subpath, skillsDir)
		cat, srcKey, found := config.FindSkillSource(cfg, name)
		if found {
			if cat == "remote" {
				if source.Kind != AddSourceRemote || srcKey != source.Key {
					plan.Conflicts = append(plan.Conflicts, AddConflict{
						Skill:       name,
						CurrentSrc:  fmt.Sprintf("[remote] %s", srcKey),
						ProposedSrc: newSrcDisplay,
					})
				}
			} else if cat == "local" {
				if entry, ok := cfg.Local[name]; ok {
					if entry.Type == "command" {
						if source.Kind != AddSourceCommand || entry.Command != source.Command {
							plan.Conflicts = append(plan.Conflicts, AddConflict{
								Skill:       name,
								CurrentSrc:  fmt.Sprintf("[command] %s", entry.Command),
								ProposedSrc: newSrcDisplay,
							})
						}
					} else {
						localSkillSource := source.LocalPath
						if subpath != "" && subpath != "." {
							localSkillSource = filepath.Join(source.LocalPath, filepath.FromSlash(subpath))
						}
						stored := models.StoreLocalSourcePath(localSkillSource, skillsDir)
						if source.Kind != AddSourceSymlink || (entry.Source != stored && models.ToTildePath(entry.Source) != models.ToTildePath(localSkillSource)) {
							plan.Conflicts = append(plan.Conflicts, AddConflict{
								Skill:       name,
								CurrentSrc:  fmt.Sprintf("[symlink] %s", models.ToTildePath(entry.Source)),
								ProposedSrc: newSrcDisplay,
							})
						}
					}
				}
			}
		} else {
			targetPath := filepath.Join(skillsDir, name)
			if fi, err := os.Lstat(targetPath); err == nil {
				current := "[untracked directory]"
				if fi.Mode()&os.ModeSymlink != 0 {
					if target, err := os.Readlink(targetPath); err == nil {
						current = fmt.Sprintf("[symlink] %s", models.ToTildePath(target))
					} else {
						current = "[symlink]"
					}
				}
				plan.Conflicts = append(plan.Conflicts, AddConflict{
					Skill:       name,
					CurrentSrc:  current,
					ProposedSrc: newSrcDisplay,
				})
			}
		}
	}
	return plan
}

// AddSkillEvent is emitted during ApplyAddPlan for UI progress reporting.
type AddSkillEvent struct {
	Name    string
	Subpath string
	Kind    AddSourceKind
	Target  string
}

// AddResult records the outcome of applying an AddPlan.
type AddResult struct {
	AddedSkills []string
	ConfigPath  string
}

// ApplyAddPlan records all selected Skills in Config, saves Config,
// Materializes each Skill, and applies Availability.
func ApplyAddPlan(plan AddPlan, cfg *config.Config, onProgress func(AddSkillEvent)) (AddResult, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	availability := NewAvailability(cfg, plan.SkillsDir)
	names := sortedSkillKeys(plan.Skills)
	stateStore, _ := NewScopeStateStore(plan.SkillsDir)

	for _, name := range names {
		subpath := plan.Skills[name]
		switch plan.Source.Kind {
		case AddSourceRemote:
			config.AddRemoteSkillEntry(cfg, plan.Source.Key, name, subpath, plan.Source.RepoType, plan.Source.URL)
		case AddSourceSymlink:
			resolved := plan.Source.LocalPath
			if subpath != "" && subpath != "." {
				resolved = filepath.Join(plan.Source.LocalPath, filepath.FromSlash(subpath))
			}
			config.AddLocalSymlinkEntry(cfg, name, models.StoreLocalSourcePath(resolved, plan.SkillsDir), plan.Source.Description)
		case AddSourceCommand:
			config.AddLocalCommandEntry(cfg, name, plan.Source.Command, plan.Source.Check, plan.Source.Description)
		}
		if len(plan.Agents) > 0 {
			if err := availability.Include(name, plan.Agents...); err != nil {
				return AddResult{}, err
			}
		}
	}

	if err := config.SaveConfig(cfg, plan.ConfigPath); err != nil {
		return AddResult{}, err
	}

	var stepErr, availabilityErr error
	note := func(ev SyncEvent) {
		switch ev.Kind {
		case SyncAvailabilityFailed:
			availabilityErr = fmt.Errorf("%s", ev.Err)
		case SyncCheckFailed:
			stepErr = fmt.Errorf("command check %q failed, skipping %s", ev.Path, ev.Skill)
		case SyncCommandFailed, SyncSymlinkFailed, SyncCopyFailed:
			stepErr = fmt.Errorf("%s", ev.Err)
		case SyncPathMissing:
			stepErr = fmt.Errorf("path missing in repository: %s", ev.Path)
		}
	}

	for _, name := range names {
		subpath := plan.Skills[name]
		if onProgress != nil {
			target := ""
			if plan.Source.Kind == AddSourceSymlink {
				target = plan.Source.LocalPath
				if subpath != "" && subpath != "." {
					target = filepath.Join(target, filepath.FromSlash(subpath))
				}
			} else if plan.Source.Kind == AddSourceCommand {
				target = plan.Source.Command
			}
			onProgress(AddSkillEvent{
				Name:    name,
				Subpath: subpath,
				Kind:    plan.Source.Kind,
				Target:  target,
			})
		}
		stepErr, availabilityErr = nil, nil
		var err error
		switch plan.Source.Kind {
		case AddSourceRemote:
			one := map[string]string{name: subpath}
			err = newRemoteSource(availability, plan.Source.Key, config.RemoteRepo{Skills: one}, "").reconcile(plan.Source.RepoDir, one, note)
		case AddSourceSymlink, AddSourceCommand:
			item := planLocalItem(cfg, plan.SkillsDir, availability.ObserveAvailability(name), name)
			applyLocalItem(availability, plan.SkillsDir, item, note)
			err = availabilityErr
		}
		if err != nil {
			return AddResult{AddedSkills: names, ConfigPath: plan.ConfigPath}, fmt.Errorf("saved config but failed to apply availability for %s: %w", name, err)
		}
		if stepErr != nil {
			return AddResult{AddedSkills: names, ConfigPath: plan.ConfigPath}, fmt.Errorf("failed to materialize skill %s: %w", name, stepErr)
		}
		if plan.Source.Kind == AddSourceRemote && stateStore != nil {
			// Best effort: an unrecordable baseline costs the next Update its
			// Cache-update classification, but the Skill itself is on disk and
			// declared. Sync reports the Scope state failure with its own next
			// action.
			_ = stateStore.RecordApplied(name, plan.Source.Key, plan.Source.RepoDir,
				GetLocalRepoCommit(plan.Source.RepoDir), filepath.Join(plan.SkillsDir, name))
		}
	}

	return AddResult{AddedSkills: names, ConfigPath: plan.ConfigPath}, nil
}
