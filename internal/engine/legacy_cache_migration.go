package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

type legacyCacheMigrationStatus uint8

const (
	legacyCacheRebuilt legacyCacheMigrationStatus = iota
	legacyCacheFailed
	legacyCacheRecoveryNeeded
)

type legacyCacheMigrationPlan struct {
	Root              string
	Sources           []string
	LegacyFingerprint string
}

type legacyCacheMigrationResult struct {
	Plan      legacyCacheMigrationPlan
	Status    legacyCacheMigrationStatus
	Err       error
	Artifacts []string
}

type legacyCacheMigrationEvent struct {
	Root   string
	Source string
	Phase  legacyCacheMigrationPhase
	Status legacyCacheMigrationStatus
	Err    error
}

type legacyCacheMigrationPhase uint8

const (
	legacyCacheMigrationStaging legacyCacheMigrationPhase = iota
	legacyCacheMigrationFinished
)

type legacyCacheDefaultBranch struct {
	Source string
	URL    string
	Branch string
}

type legacyCacheMigrationOps struct {
	mkdirTemp       func(string, string) (string, error)
	mkdirAll        func(string, fs.FileMode) error
	remove          func(string) error
	removeAll       func(string) error
	rename          func(string, string) error
	runGit          func(string, ...string) (string, string, error)
	ensureGitRepo   func(string, string, string, bool, string) (string, error)
	localRepoCommit func(string) string
	repoFingerprint func(string) string
}

type legacyCacheMigrator struct {
	cfg      *config.Config
	cacheDir string
	ops      legacyCacheMigrationOps
}

func newLegacyCacheMigrator(cfg *config.Config, cacheDir string) *legacyCacheMigrator {
	return &legacyCacheMigrator{
		cfg:      cfg,
		cacheDir: cacheDir,
		ops: legacyCacheMigrationOps{
			mkdirTemp:       os.MkdirTemp,
			mkdirAll:        os.MkdirAll,
			remove:          os.Remove,
			removeAll:       RemoveAll,
			rename:          os.Rename,
			runGit:          RunGit,
			ensureGitRepo:   EnsureGitRepo,
			localRepoCommit: GetLocalRepoCommit,
			repoFingerprint: legacyCacheFingerprint,
		},
	}
}

func (m *legacyCacheMigrator) detect() ([]legacyCacheMigrationPlan, []string, error) {
	byRoot := make(map[string][]string)
	for source := range m.cfg.Remote {
		root := filepath.Join(m.cacheDir, filepath.FromSlash(models.ParseRepoSource(source).SourceKey))
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			byRoot[root] = append(byRoot[root], source)
		}
	}
	plans := make([]legacyCacheMigrationPlan, 0, len(byRoot))
	for root, sources := range byRoot {
		slices.Sort(sources)
		plans = append(plans, legacyCacheMigrationPlan{Root: root, Sources: sources, LegacyFingerprint: m.ops.repoFingerprint(root)})
	}
	slices.SortFunc(plans, func(a, b legacyCacheMigrationPlan) int { return strings.Compare(a.Root, b.Root) })

	var artifacts []string
	err := filepath.WalkDir(m.cacheDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path != m.cacheDir && entry.IsDir() &&
			(strings.HasPrefix(entry.Name(), ".legacy-cache-") || strings.HasPrefix(entry.Name(), ".doctor-cache-")) {
			artifacts = append(artifacts, path)
			return filepath.SkipDir
		}
		return nil
	})
	if os.IsNotExist(err) {
		err = nil
	}
	slices.Sort(artifacts)
	return plans, artifacts, err
}

func (m *legacyCacheMigrator) apply(plans []legacyCacheMigrationPlan, onEvent func(legacyCacheMigrationEvent)) []legacyCacheMigrationResult {
	results := make([]legacyCacheMigrationResult, 0, len(plans))
	for _, plan := range plans {
		result := m.applyOne(plan, onEvent)
		results = append(results, result)
		if onEvent != nil {
			onEvent(legacyCacheMigrationEvent{Root: plan.Root, Phase: legacyCacheMigrationFinished, Status: result.Status, Err: result.Err})
		}
	}
	return results
}

func (m *legacyCacheMigrator) applyOne(plan legacyCacheMigrationPlan, onEvent func(legacyCacheMigrationEvent)) (result legacyCacheMigrationResult) {
	result = legacyCacheMigrationResult{Plan: plan, Status: legacyCacheFailed}
	staging, err := m.ops.mkdirTemp(m.cacheDir, ".doctor-cache-")
	if err != nil {
		result.Err = err
		return result
	}
	removeStaging := true
	defer func() {
		if removeStaging {
			if cleanupErr := m.ops.removeAll(staging); cleanupErr != nil {
				result.Status = legacyCacheRecoveryNeeded
				result.Artifacts = append(result.Artifacts, staging)
				result.Err = errors.Join(result.Err, fmt.Errorf("remove migration staging: %w", cleanupErr))
			}
		}
	}()

	stagedSource := ""
	var defaultBranches []legacyCacheDefaultBranch
	for _, source := range plan.Sources {
		if onEvent != nil {
			onEvent(legacyCacheMigrationEvent{Root: plan.Root, Source: source, Phase: legacyCacheMigrationStaging})
		}
		repo := m.cfg.Remote[source]
		sourceKey := models.ParseRepoSource(source).SourceKey
		stagedSource = filepath.Join(staging, filepath.FromSlash(sourceKey))
		current := resolveCacheRepo(source, repo.URL, repo.Branch, m.cacheDir)
		if m.ops.localRepoCommit(current.Dir) != "" {
			staged := resolveCacheRepo(source, repo.URL, current.Branch, staging)
			if err := m.ops.mkdirAll(filepath.Dir(staged.Dir), 0o755); err != nil {
				result.Err = err
				return result
			}
			stdout, stderr, cloneErr := m.ops.runGit("", "clone", "--local", current.Dir, staged.Dir)
			if cloneErr != nil {
				result.Err = gitOpErr("stage existing Cache from", current.Dir, stdout, stderr, cloneErr)
				return result
			}
			if stdout, stderr, setURLErr := m.ops.runGit(staged.Dir, "remote", "set-url", "origin", current.URL); setURLErr != nil {
				result.Err = gitOpErr("restore origin URL for", current.URL, stdout, stderr, setURLErr)
				return result
			}
			if repo.Branch == "" && models.ParseRepoSource(source).Branch == "" {
				if err := recordDefaultBranch(source, current.URL, staging, current.Branch); err != nil {
					result.Err = err
					return result
				}
			}
		} else if _, err := m.ops.ensureGitRepo(source, repo.URL, repo.Branch, false, staging); err != nil {
			result.Err = fmt.Errorf("rebuild Source %s: %w", source, err)
			return result
		}
		if repo.Branch == "" && models.ParseRepoSource(source).Branch == "" {
			resolvedURL := resolveCacheRepo(source, repo.URL, repo.Branch, staging).URL
			defaultBranches = append(defaultBranches, legacyCacheDefaultBranch{
				Source: source,
				URL:    resolvedURL,
				Branch: cachedDefaultBranch(source, resolvedURL, staging),
			})
		}
	}
	if len(plan.Sources) == 0 {
		result.Err = fmt.Errorf("no configured Source matches legacy Cache %s", plan.Root)
		return result
	}
	if got := m.ops.repoFingerprint(plan.Root); got != plan.LegacyFingerprint {
		result.Err = fmt.Errorf("legacy Cache changed after planning: %s", plan.Root)
		return result
	}
	for _, identity := range defaultBranches {
		if err := recordDefaultBranch(identity.Source, identity.URL, m.cacheDir, identity.Branch); err != nil {
			result.Err = err
			return result
		}
	}

	backup, err := m.ops.mkdirTemp(filepath.Dir(plan.Root), ".legacy-cache-")
	if err != nil {
		result.Err = err
		return result
	}
	if err := m.ops.remove(backup); err != nil {
		result.Status = legacyCacheRecoveryNeeded
		result.Artifacts = []string{backup}
		result.Err = fmt.Errorf("prepare legacy Cache backup: %w", err)
		return result
	}
	if err := m.ops.rename(plan.Root, backup); err != nil {
		result.Err = err
		return result
	}
	if err := m.ops.rename(stagedSource, plan.Root); err != nil {
		if rollbackErr := m.ops.rename(backup, plan.Root); rollbackErr != nil {
			removeStaging = false
			result.Status = legacyCacheRecoveryNeeded
			result.Artifacts = []string{backup, staging}
			result.Err = fmt.Errorf("install replacement: %w; restore legacy Cache: %v", err, rollbackErr)
			return result
		}
		result.Err = err
		return result
	}
	if err := m.ops.removeAll(backup); err != nil {
		result.Status = legacyCacheRecoveryNeeded
		result.Artifacts = []string{backup}
		result.Err = fmt.Errorf("remove legacy Cache backup: %w", err)
		return result
	}
	result.Status = legacyCacheRebuilt
	return result
}

func legacyCacheFingerprint(dir string) string {
	commit := GetLocalRepoCommit(dir)
	status, _, err := RunGit(dir, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return commit + "\x00unreadable"
	}
	return commit + "\x00" + status
}
