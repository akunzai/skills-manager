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
	// TrustRootErr is why Declare could not fetch the trust root its signed
	// Skills need.
	TrustRootErr error
	// Skipped is why each Skill a well-known index lists could not be
	// offered, such as an unknown type.
	Skipped []string

	spec     models.ParsedRepoSource // Branch is the one fetched
	cache    sourceCache
	cacheDir string
}

// RemoteIntakeSpec is the Source Remote intake reads for an Add of kind
// remote or well-known from raw. A git Source's url, branch, and subpath
// override what raw names; a well-known Source is its base URL alone, since
// ClassifyAddKind refuses those flags for it.
func RemoteIntakeSpec(kind AddSourceKind, raw, url, branch, subpath string) models.ParsedRepoSource {
	if kind == AddSourceWellKnown {
		source, _ := models.ParseWellKnownSource(raw)
		return models.ParsedRepoSource{SourceKey: source.SourceKey, URL: source.URL, RepoType: config.WellKnownType}
	}
	spec := models.ParseRepoSource(raw)
	spec.URL = cmp.Or(url, spec.URL)
	spec.Branch = cmp.Or(branch, spec.Branch)
	spec.Subpath = cmp.Or(subpath, spec.Subpath)
	return spec
}

// PrepareRemoteIntake refreshes spec's Source into its Cache and discovers its
// Skills, with their descriptions. A git Source is discovered under
// spec.Subpath; with no branch in spec it fetches the branch cfg declares for
// the Source, and a different branch is fetched as asked, since nothing is
// declared yet. A well-known Source offers each Skill its index lists under
// the name the index gives it, and downloads them only once declared. It
// prints nothing and never changes cfg.
func PrepareRemoteIntake(cfg *config.Config, spec models.ParsedRepoSource, cacheDir string) (*RemoteIntake, error) {
	if spec.RepoType == config.WellKnownType {
		return prepareWellKnownIntake(spec, cacheDir)
	}
	spec.Branch = cmp.Or(spec.Branch, cfg.Remote[spec.SourceKey].Branch)
	cache := NewCache(spec.SourceKey, spec.URL, spec.Branch, cacheDir)
	if err := cache.Refresh(true); err != nil {
		return nil, fmt.Errorf("refresh Source %s: %w", spec.SourceKey, err)
	}
	discovered, descriptions, err := discoverRemoteSkills(cache, spec.Subpath)
	if err != nil {
		return nil, fmt.Errorf("discover Skills in %s: %w", spec.SourceKey, err)
	}
	return &RemoteIntake{Discovered: discovered, Descriptions: descriptions, spec: spec, cache: cache, cacheDir: cacheDir}, nil
}

// prepareWellKnownIntake is PrepareRemoteIntake for a well-known Source.
func prepareWellKnownIntake(spec models.ParsedRepoSource, cacheDir string) (*RemoteIntake, error) {
	cache := NewWellKnownCache(spec.SourceKey, spec.URL, cacheDir)
	if err := cache.Refresh(true); err != nil {
		return nil, err
	}
	entries, skipped, err := cache.wellKnownEntries()
	if err != nil {
		return nil, fmt.Errorf("read index of %s: %w", spec.SourceKey, err)
	}
	intake := &RemoteIntake{
		Discovered:   make(DiscoveredSkills, len(entries)),
		Descriptions: make(DiscoveredSkillDescriptions, len(entries)),
		Skipped:      skipped,
		spec:         spec,
		cache:        cache,
		cacheDir:     cacheDir,
	}
	for _, entry := range entries {
		intake.Discovered[entry.Name] = []string{entry.Name}
		intake.Descriptions[entry.Name] = entry.Description
	}
	return intake, nil
}

// Declare records skills (name to subpath) from this Source in cfg, with the
// branch that was fetched, after covering them in the Cache together with the
// Skills cfg already declares from the Source. A Source cfg declares on
// another branch is refused before anything changes, as is one cfg declares
// on a branch this intake did not read: the Cache Skills are Materialized
// from must be the one cfg names. Declare changes cfg in memory only; the
// caller saves it.
func (in *RemoteIntake) Declare(cfg *config.Config, skills map[string]string) error {
	key := in.spec.SourceKey
	if declared, ok := cfg.Remote[key]; ok && (declared.Type == config.WellKnownType) != (in.spec.RepoType == config.WellKnownType) {
		return fmt.Errorf("Source %s is already declared as a %s Source; remove its Skills before adding it again", key, sourceKindName(declared.Type))
	}
	branch, err := AddBranch(cfg, key, in.spec.Branch)
	if err != nil {
		return err
	}
	if branch != in.spec.Branch {
		return fmt.Errorf("Source %s is declared on branch %q, but its default branch was read; add it again with --branch %s", key, branch, branch)
	}
	names := sortedSkillKeys(skills)
	subpaths := declaredSubpaths(cfg.Remote[key])
	for _, name := range names {
		subpaths = append(subpaths, skills[name])
	}
	if err := in.cache.Cover(subpaths...); err != nil {
		return fmt.Errorf("fetch selected Skills into the Cache: %w", err)
	}
	// A trust root that cannot be fetched now leaves the signed Skills
	// blocked, not undeclared; Add reports it, and Update fetches it later.
	in.TrustRootErr = ensureTrustRoot(in.cacheDir, true, skillDirs(in.cache, subpaths)...)
	url := in.spec.URL
	if in.spec.RepoType != config.WellKnownType {
		url = storedRemoteURL(key, url)
	}
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

// sourceKindName is how a message names a Source of RemoteRepo type t.
func sourceKindName(t string) string {
	if t == config.WellKnownType {
		return "well-known"
	}
	return "git"
}

func declaredSubpaths(repo config.RemoteRepo) []string {
	paths := make([]string, 0, len(repo.Skills))
	for _, name := range sortedSkillKeys(repo.Skills) {
		paths = append(paths, repo.Skills[name])
	}
	return paths
}
