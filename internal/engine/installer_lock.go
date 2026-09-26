package engine

import (
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/akunzai/skills-manager/internal/models"
)

// InstallerLockRecord is what an installer lock file records about one Skill:
// the git Source it was installed from, when it was one. Adopt reads these
// files and never writes them.
type InstallerLockRecord struct {
	// Source is the Source key, or empty when the record names no git
	// repository (a local path, a package, a website).
	Source   string
	RepoType string
	// URL is the clone URL.
	URL    string
	Branch string
	// Subpath is the Skill's directory inside the repository, or empty when
	// the record does not say.
	Subpath string
}

// Remote reports whether the record names a git repository Adopt can declare
// as a remote Source.
func (r InstallerLockRecord) Remote() bool { return r.Source != "" }

// installerLockFile is the shape installer lock files share: Skill name to
// entry under "skills". Anything else in the file, and any other field of an
// entry, is ignored, so a newer version still reads.
type installerLockFile struct {
	Skills map[string]struct {
		Source     string `json:"source"`
		SourceType string `json:"sourceType"`
		SourceURL  string `json:"sourceUrl"`
		Ref        string `json:"ref"`
		SkillPath  string `json:"skillPath"`
	} `json:"skills"`
}

// InstallerLockPath is where an installer lock file records the Skills it
// installed into skillsDir: at Global Scope $XDG_STATE_HOME/skills/.skill-lock.json
// when XDG_STATE_HOME is set, otherwise ~/.agents/.skill-lock.json; at Project
// Scope skills-lock.json in the project root.
func InstallerLockPath(skillsDir string) string {
	if models.IsProjectScope(skillsDir) {
		return filepath.Join(models.ScopeRoot(skillsDir), "skills-lock.json")
	}
	if state := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); state != "" {
		return filepath.Join(models.ExpandUser(state), "skills", ".skill-lock.json")
	}
	return filepath.Join(models.UserHomeDir(), ".agents", ".skill-lock.json")
}

// ReadInstallerLock reads the installer lock file at lockPath. A missing file
// records nothing.
func ReadInstallerLock(lockPath string) (map[string]InstallerLockRecord, error) {
	data, err := os.ReadFile(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file installerLockFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read installer lock file %s: %w", lockPath, err)
	}
	records := make(map[string]InstallerLockRecord, len(file.Skills))
	for name, entry := range file.Skills {
		record := InstallerLockRecord{}
		switch strings.ToLower(entry.SourceType) {
		case "github", "gitlab", "git":
			parsed := models.ParseRepoSource(cmp.Or(entry.Source, entry.SourceURL))
			record = InstallerLockRecord{
				Source:   parsed.SourceKey,
				RepoType: parsed.RepoType,
				URL:      cmp.Or(entry.SourceURL, parsed.URL),
				Branch:   cmp.Or(entry.Ref, parsed.Branch),
				Subpath:  skillDirSubpath(entry.SkillPath),
			}
		}
		records[name] = record
	}
	return records, nil
}

// skillDirSubpath normalises a recorded path to a Skill's SKILL.md, or to its
// directory, to the directory's repository subpath. A path that escapes the
// repository says nothing usable.
func skillDirSubpath(skillPath string) string {
	p := strings.TrimSpace(filepath.ToSlash(skillPath))
	if p == "" {
		return ""
	}
	if strings.EqualFold(path.Base(p), "SKILL.md") {
		p = path.Dir(p)
	}
	p = path.Clean(p)
	if path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}
