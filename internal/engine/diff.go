package engine

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
)

// NoBaselineReason is why a remote Skill's diff has no Baseline to split
// upstream from local changes.
type NoBaselineReason string

const (
	NoBaselineNotRecorded NoBaselineReason = "not_recorded"
	// NoBaselineStateUnreadable is a Scope state that could not be read.
	NoBaselineStateUnreadable NoBaselineReason = "state_unreadable"
	// NoBaselineOtherCache is a Baseline applied from another Source or
	// Cache, whose commit this Cache need not have.
	NoBaselineOtherCache NoBaselineReason = "other_cache"
)

// FileDiff is one file's part of a unified patch.
type FileDiff struct {
	Path    string
	Added   int
	Removed int
	Binary  bool
	// Patch is the file's unified diff, headers included.
	Patch string
}

// SkillDiff is what changed for one remote Skill: Upstream from its Baseline
// commit to the Cache commit, and Local from the Baseline content to the
// Scope copy. Without a Baseline the two cannot be told apart, so Upstream
// compares the Scope copy with the Cache and Local is nil.
type SkillDiff struct {
	Skill          string
	Source         string
	Subpath        string
	ScopePath      string
	CacheCommit    string
	BaselineCommit string
	NoBaseline     NoBaselineReason
	StateError     string
	Upstream       []FileDiff
	Local          []FileDiff
}

// Empty is whether neither section has a difference.
func (d *SkillDiff) Empty() bool {
	return len(d.Upstream) == 0 && len(d.Local) == 0
}

// DiffSkill compares one remote Skill's Baseline, Cache and Scope copy. It
// reads the Cache as it is and writes nothing to the Scope, its Config, its
// state, or the Cache.
func DiffSkill(cfg *config.Config, name, skillsDir, cacheDir string) (*SkillDiff, error) {
	kind, source, found := config.FindSkillSource(cfg, name)
	switch {
	case !found:
		return nil, fmt.Errorf("%s is not declared in this Scope's Config", name)
	case kind == config.SkillCommand:
		return nil, fmt.Errorf("%s is a command Skill: its installer provides it, so there is no Source to diff against", name)
	case kind == config.SkillSymlink:
		return nil, fmt.Errorf("%s is a local Skill: the Scope links to its Source directly, so there is nothing to diff", name)
	}
	repo := cfg.Remote[source]
	subpath := repo.Skills[name]
	cache := NewCache(source, repo.URL, repo.Branch, cacheDir)
	cacheRepoDir := cache.dir()
	head := cache.head()
	if head == "" {
		return nil, fmt.Errorf("Cache missing for Source %s; run 'skills diff --fetch %s' or 'skills update' first", source, name)
	}
	if coverage, err := cache.coverage(); err != nil {
		return nil, err
	} else if len(coverage.missing([]string{subpath})) > 0 && cache.atHead(subpath).exists() {
		return nil, fmt.Errorf("Cache for Source %s does not cover %s; run 'skills diff --fetch %s' or 'skills update' first", source, name, name)
	}

	d := &SkillDiff{Skill: name, Source: source, Subpath: subpath, ScopePath: filepath.Join(skillsDir, name), CacheCommit: head}
	baselines := OpenBaselines(skillsDir)
	applied, recorded := baselines.Applied(name)
	switch {
	case baselines.Err() != nil:
		d.NoBaseline, d.StateError = NoBaselineStateUnreadable, baselines.Err().Error()
	case !recorded || applied.AppliedCommit == "":
		d.NoBaseline = NoBaselineNotRecorded
	case applied.Source != source || applied.CacheIdentity != cacheRepoDir:
		d.NoBaseline = NoBaselineOtherCache
	default:
		d.BaselineCommit = applied.AppliedCommit
	}

	scope, err := digestIfPresent(d.ScopePath)
	if err != nil {
		return nil, err
	}
	fromScope := func(rel string) ([]byte, error) { return readSkillFile(d.ScopePath, rel) }
	if d.BaselineCommit == "" {
		cachePath := filepath.Join(cacheRepoDir, filepath.FromSlash(subpath))
		cached, err := digestIfPresent(cachePath)
		if err != nil {
			return nil, err
		}
		d.Upstream, err = diffFiles(scope, fromScope, cached, func(rel string) ([]byte, error) { return readSkillFile(cachePath, rel) })
		return d, err
	}

	patch, err := cache.diffCommits(d.BaselineCommit, head, subpath)
	if err != nil {
		return nil, err
	}
	d.Upstream = parsePatch(patch)
	fromBaseline := func(rel string) ([]byte, error) { return cache.readBlob(d.BaselineCommit, path.Join(subpath, rel)) }
	d.Local, err = diffFiles(applied.ContentDigests, fromBaseline, scope, fromScope)
	return d, err
}

// digestIfPresent is DigestSkillContent with a missing directory read as empty.
func digestIfPresent(root string) (map[string]string, error) {
	digests, err := DigestSkillContent(root)
	if errors.Is(err, os.ErrNotExist) || os.IsNotExist(rootPathError(err)) {
		return map[string]string{}, nil
	}
	return digests, err
}

// readSkillFile reads rel under root as DigestSkillContent sees it: a
// symlink as its target.
func readSkillFile(root, rel string) ([]byte, error) {
	file := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(file)
		return []byte(filepath.ToSlash(target)), err
	}
	return os.ReadFile(file)
}

// diffFiles is the patch from one set of Skill files to another. Only the
// files whose digests differ are read and written to a temporary directory
// for git to compare.
func diffFiles(from map[string]string, readFrom func(string) ([]byte, error), to map[string]string, readTo func(string) ([]byte, error)) ([]FileDiff, error) {
	changes := compareDigestMaps(from, to)
	if len(changes.Added)+len(changes.Removed)+len(changes.Modified) == 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "skills-diff-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	write := func(tree, rel string, read func(string) ([]byte, error)) error {
		if !filepath.IsLocal(filepath.FromSlash(rel)) {
			return fmt.Errorf("refusing to diff path outside the Skill: %s", rel)
		}
		content, err := read(rel)
		if err != nil {
			return err
		}
		file := filepath.Join(dir, tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		return os.WriteFile(file, content, 0o644)
	}
	for _, tree := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(dir, tree), 0o755); err != nil {
			return nil, err
		}
	}
	for _, rel := range slices.Concat(changes.Removed, changes.Modified) {
		if err := write("a", rel, readFrom); err != nil {
			return nil, err
		}
	}
	for _, rel := range slices.Concat(changes.Added, changes.Modified) {
		if err := write("b", rel, readTo); err != nil {
			return nil, err
		}
	}
	patch, err := diffDirs(dir)
	if err != nil {
		return nil, err
	}
	return parsePatch(patch), nil
}

// parsePatch splits a git patch into its files and counts each file's
// added and removed lines.
func parsePatch(patch string) []FileDiff {
	var files []FileDiff
	inHunk := false
	for line := range strings.Lines(patch) {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			files = append(files, FileDiff{Path: patchPath(rest)})
			inHunk = false
		}
		if len(files) == 0 {
			continue
		}
		file := &files[len(files)-1]
		file.Patch += line
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case !inHunk && strings.HasPrefix(line, "Binary files "):
			file.Binary = true
		case inHunk && strings.HasPrefix(line, "+"):
			file.Added++
		case inHunk && strings.HasPrefix(line, "-"):
			file.Removed++
		}
	}
	return files
}

// patchPath is the path a "diff --git" header names, without its a/ prefix.
func patchPath(rest string) string {
	src, ok := diffGitPath(rest)
	if !ok {
		return strings.TrimSpace(rest)
	}
	if unquoted, err := strconv.Unquote(src); err == nil {
		src = unquoted
	}
	return strings.TrimPrefix(src, "a/")
}
