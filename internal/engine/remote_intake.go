package engine

import (
	"cmp"
	"fmt"
	neturl "net/url"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// RemoteIntake is one remote Source fetched into its Cache with its Skills
// discovered, ready to declare the chosen ones in a Config. PrepareRemoteIntake
// is the only way to create one.
type RemoteIntake struct {
	// Discovered is every Skill the Source offers under the requested subpath.
	Discovered DiscoveredSkills
	// Descriptions is each Discovered candidate path's SKILL.md description.
	Descriptions DiscoveredSkillDescriptions

	spec  models.ParsedRepoSource // Branch is the one fetched
	cache Cache
	dir   string
}

// PrepareRemoteIntake refreshes spec's Source into its Cache and discovers its
// Skills under spec.Subpath, with their descriptions. With no branch in spec
// it fetches the branch cfg declares for the Source; a different branch is
// fetched as asked, since nothing is declared yet. It prints nothing and
// never changes cfg.
func PrepareRemoteIntake(cfg *config.Config, spec models.ParsedRepoSource, cacheDir string) (*RemoteIntake, error) {
	spec.Branch = cmp.Or(spec.Branch, cfg.Remote[spec.SourceKey].Branch)
	cache := NewCache(spec.SourceKey, spec.URL, spec.Branch, cacheDir)
	dir, err := cache.Refresh(true)
	if err != nil {
		return nil, fmt.Errorf("refresh Source %s: %w", spec.SourceKey, err)
	}
	discovered, descriptions, err := discoverRemoteSkills(cache, spec.Subpath)
	if err != nil {
		return nil, fmt.Errorf("discover Skills in %s: %w", spec.SourceKey, err)
	}
	return &RemoteIntake{Discovered: discovered, Descriptions: descriptions, spec: spec, cache: cache, dir: dir}, nil
}

// Declare records skills (name to subpath) from this Source in cfg, with the
// branch that was fetched, after covering them in the Cache together with the
// Skills cfg already declares from the Source. A Source cfg declares on
// another branch is refused before anything changes. Declare changes cfg in
// memory only; the caller saves it.
func (in *RemoteIntake) Declare(cfg *config.Config, skills map[string]string) error {
	key := in.spec.SourceKey
	if _, err := AddBranch(cfg, key, in.spec.Branch); err != nil {
		return err
	}
	names := sortedSkillKeys(skills)
	subpaths := declaredSubpaths(cfg.Remote[key])
	for _, name := range names {
		subpaths = append(subpaths, skills[name])
	}
	if err := in.cache.Cover(subpaths...); err != nil {
		return fmt.Errorf("fetch selected Skills into the Cache: %w", err)
	}
	url := storedRemoteURL(key, in.spec.URL)
	for _, name := range names {
		config.AddRemoteSkillEntry(cfg, key, name, skills[name], in.spec.RepoType, url)
	}
	if in.spec.Branch != "" && len(names) > 0 {
		repo := cfg.Remote[key]
		repo.Branch = in.spec.Branch
		cfg.Remote[key] = repo
	}
	return nil
}

// storedRemoteURL is the URL Config records for a Source: none when it is the
// one the Source key implies, which is what an unrecorded URL resolves to.
// The two are compared normalized, so a URL spelled without .git, with a
// trailing /, or with the host in another case is still the implied one.
func storedRemoteURL(key, url string) string {
	if normalizeRemoteURL(url) == normalizeRemoteURL(models.ParseRepoSource(key).URL) {
		return ""
	}
	return url
}

// normalizeRemoteURL drops a trailing / and .git and lowercases the host. The
// path keeps its case.
func normalizeRemoteURL(raw string) string {
	s := strings.TrimRight(raw, "/")
	s = strings.TrimRight(strings.TrimSuffix(s, ".git"), "/")
	if u, err := neturl.Parse(s); err == nil && u.Host != "" {
		u.Host = strings.ToLower(u.Host)
		return u.String()
	}
	return s
}

func declaredSubpaths(repo config.RemoteRepo) []string {
	paths := make([]string, 0, len(repo.Skills))
	for _, name := range sortedSkillKeys(repo.Skills) {
		paths = append(paths, repo.Skills[name])
	}
	return paths
}
