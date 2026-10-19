package engine

import "github.com/akunzai/skills-manager/internal/config"

// sourceCache is where a remote Source's Skills are Materialized from: a git
// Cache, or a WellKnownCache for a Source served through a well-known index.
// Freshness, Sync planning, Update, and Remote intake read a Cache only
// through it, so neither kind leaks into the other (ADR-0005, ADR-0013).
type sourceCache interface {
	// dir is the root a Skill's subpath is resolved against.
	dir() string
	// head names what the Cache holds, or is "" when there is no Cache.
	head() string
	// atHead is what the Cache's snapshot of the Source has at subpath:
	// whether the Source has it, and the identity of its content.
	atHead(subpath string) headEntry
	// coverage says which subpaths are on disk.
	coverage() (sparseState, error)
	// identity is what a Baseline records the Skill was applied from.
	identity() string
	// appliedCommit is what a Baseline records as the version applied of
	// the Skill at subpath.
	appliedCommit(subpath string) string
	// where qualifies the Source in a message, such as the branch it is
	// read from, or is "".
	where() string
	// canCarrySignature is whether the Skill at subpath has somewhere to
	// carry its signature.
	canCarrySignature(subpath string) bool
	// history is how the Cache reads earlier versions of the Source, or not
	// ok when it keeps only the latest.
	history() (cacheHistory, bool)
	// described is the URL the Source is read from, and the branch, or ""
	// for a Source that has none.
	described() (url, branch string)
	// observe asks the Source what it has now, for Freshness to classify.
	observe(paths ...string) cacheFacts
	// RefreshWithProgress fetches what the Source has now and covers paths.
	RefreshWithProgress(force bool, progress UpdateProgress, paths ...string) error
	// Cover puts paths on disk without fetching a new snapshot of the Source.
	Cover(paths ...string) error
	// withSkillFiles calls fn with a directory holding the SKILL.md of every
	// Skill in the snapshot, putting them there first if it must.
	withSkillFiles(fn func(repoDir string) error) error
}

// cacheHistory reads a Source's earlier versions from its Cache.
type cacheHistory interface {
	// patch is the patch of subpath from one version to another, with paths
	// relative to subpath.
	patch(from, to, subpath string) (string, error)
	// blob is the file at path, relative to the Source's root, as version
	// has it.
	blob(version, path string) ([]byte, error)
}

// openSourceCache is the Cache a declared Source's Skills are Materialized
// from.
func openSourceCache(source string, repo config.RemoteRepo, cacheDir string) sourceCache {
	if repo.Type == config.WellKnownType {
		return NewWellKnownCache(source, repo.URL, cacheDir)
	}
	return NewCache(source, repo.URL, repo.Branch, cacheDir)
}

func (c Cache) identity() string { return c.dir() }

func (c Cache) appliedCommit(string) string { return c.head() }

// canCarrySignature is true: a Skill's directory can always hold one.
func (c Cache) canCarrySignature(string) bool { return true }

// history is the Cache itself: git keeps every commit it fetched.
func (c Cache) history() (cacheHistory, bool) { return c, true }

func (c Cache) described() (string, string) { return c.url, c.resolvedBranch() }

func (c Cache) where() string {
	branch := c.resolvedBranch()
	switch {
	case branch == "":
		return ""
	case c.branch != "":
		return " (branch " + branch + ")"
	default:
		return " (default branch " + branch + ")"
	}
}
