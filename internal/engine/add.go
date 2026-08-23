package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	AllowRename bool   // whether a single discovered skill can be renamed by --skill
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

func NewSymlinkAddSource(absPath, description string, allowRename bool) AddSource {
	return AddSource{
		Kind:        AddSourceSymlink,
		Key:         absPath,
		LocalPath:   absPath,
		Description: description,
		AllowRename: allowRename,
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

	var stepErr error
	note := func(ev SyncEvent) {
		switch ev.Kind {
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
		stepErr = nil
		var err error
		switch plan.Source.Kind {
		case AddSourceRemote:
			one := map[string]string{name: subpath}
			err = newRemoteSource(availability, plan.Source.Key, config.RemoteRepo{Skills: one}, "").reconcile(plan.Source.RepoDir, false, one, note)
		case AddSourceSymlink:
			err = reconcileLocalSymlink(availability, name, false, note)
		case AddSourceCommand:
			err = reconcileCommand(availability, name, false, note)
		}
		if err != nil {
			return AddResult{AddedSkills: names, ConfigPath: plan.ConfigPath}, fmt.Errorf("saved config but failed to apply availability for %s: %w", name, err)
		}
		if stepErr != nil {
			return AddResult{AddedSkills: names, ConfigPath: plan.ConfigPath}, fmt.Errorf("failed to materialize skill %s: %w", name, stepErr)
		}
	}

	return AddResult{AddedSkills: names, ConfigPath: plan.ConfigPath}, nil
}

// ResolveDiscoveredSkills filters discovered Skills according to --all, --path,
// and --skill flags. It is completely deterministic and pure.
// When no selection flag is set, it returns (nil, nil, nil), signaling that
// interactive selection or sole-skill defaulting is required.
func ResolveDiscoveredSkills(
	discovered map[string]string,
	rootDir string,
	all bool,
	pathOverride string,
	skills []string,
	allowRename bool,
) (skillsToAdd map[string]string, unmatched []string, err error) {
	if all {
		skillsToAdd = make(map[string]string, len(discovered))
		for k, v := range discovered {
			skillsToAdd[k] = v
		}
		return skillsToAdd, nil, nil
	}

	if pathOverride != "" && len(skills) == 0 {
		skillsToAdd = make(map[string]string)
		cleanSub := filepath.ToSlash(strings.Trim(pathOverride, "/"))
		for k, v := range discovered {
			if filepath.ToSlash(strings.Trim(v, "/")) == cleanSub {
				skillsToAdd[k] = v
			}
		}
		if len(skillsToAdd) == 0 {
			subDir := filepath.Join(rootDir, filepath.FromSlash(pathOverride))
			if _, statErr := os.Stat(filepath.Join(subDir, "SKILL.md")); statErr == nil {
				skillsToAdd[filepath.Base(subDir)] = pathOverride
			} else {
				return nil, nil, fmt.Errorf("specified path '%s' does not contain SKILL.md", pathOverride)
			}
		}
		return skillsToAdd, nil, nil
	}

	if len(skills) > 0 {
		skillsToAdd = make(map[string]string)
		if len(discovered) == 1 && len(skills) == 1 && allowRename {
			for _, sub := range discovered {
				skillsToAdd[skills[0]] = sub
			}
			return skillsToAdd, nil, nil
		}
		for _, sk := range skills {
			if sub, ok := discovered[sk]; ok {
				skillsToAdd[sk] = sub
			} else if pathOverride != "" {
				skillsToAdd[sk] = pathOverride
			} else {
				matched := false
				for k, v := range discovered {
					if strings.EqualFold(k, sk) {
						skillsToAdd[k] = v
						matched = true
						break
					}
				}
				if !matched {
					unmatched = append(unmatched, sk)
				}
			}
		}
		return skillsToAdd, unmatched, nil
	}

	return nil, nil, nil
}

// Adder is a convenience wrapper preserving legacy Adder callers by delegating
// directly to BuildAddPlan and ApplyAddPlan.
type Adder struct {
	cfg        *config.Config
	configPath string
	skillsDir  string
	source     AddSource
}

func NewRemoteAdder(cfg *config.Config, configPath, skillsDir, repoDir, sourceKey, repoType, url string) *Adder {
	return &Adder{
		cfg:        cfg,
		configPath: configPath,
		skillsDir:  skillsDir,
		source:     NewRemoteAddSource(sourceKey, repoType, url, repoDir),
	}
}

func NewSymlinkAdder(cfg *config.Config, configPath, skillsDir, absSourcePath, description string) *Adder {
	return &Adder{
		cfg:        cfg,
		configPath: configPath,
		skillsDir:  skillsDir,
		source:     NewSymlinkAddSource(absSourcePath, description, false),
	}
}

func NewCommandAdder(cfg *config.Config, configPath, skillsDir, command, check, description string) *Adder {
	return &Adder{
		cfg:        cfg,
		configPath: configPath,
		skillsDir:  skillsDir,
		source:     NewCommandAddSource(command, check, description),
	}
}

// Run executes the Add operation using BuildAddPlan and ApplyAddPlan.
func (a *Adder) Run(skills map[string]string, agents []string, progress func(name, subpath string)) error {
	plan := BuildAddPlan(a.cfg, a.configPath, a.skillsDir, a.source, skills, agents)
	_, err := ApplyAddPlan(plan, a.cfg, func(ev AddSkillEvent) {
		if progress != nil {
			progress(ev.Name, ev.Subpath)
		}
	})
	return err
}
