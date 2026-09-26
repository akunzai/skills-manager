package engine

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/akunzai/skills-manager/internal/config"
	"github.com/akunzai/skills-manager/internal/models"
)

// CacheRemoval is what --fix did to one detected legacy Cache artifact: a
// branchless Cache root at <cache>/<SourceKey> for a still-declared remote
// Source (skills-manager v0.6 or earlier), or a `.legacy-cache-*` /
// `.doctor-cache-*` directory an interrupted rebuild from v0.8–v0.18 left
// behind (#177). Removing it is enough: nothing here rebuilds a Cache, so the
// next `skills update` fetches a fresh branch-aware Cache in its place
// (ADR-0004). A removal failure is reported and counted like any other
// failed repair (ADR-0002); it is never "recovery needed" — a plain removal
// has no partial state to recover from.
type CacheRemoval struct {
	Path   string
	Repair ItemRepair
}

// legacyCacheDetector finds legacy Cache artifacts and removes them, without
// the network.
type legacyCacheDetector struct {
	cfg       *config.Config
	cacheDir  string
	removeAll func(string) error
}

func newLegacyCacheDetector(cfg *config.Config, cacheDir string) *legacyCacheDetector {
	return &legacyCacheDetector{cfg: cfg, cacheDir: cacheDir, removeAll: RemoveAll}
}

// detectRoots finds a branchless Cache root — a `.git` directly at
// <cache>/<SourceKey> — for each declared remote Source, in sorted order.
// The branch-aware layout nests under this same path (resolveCacheRepo in
// git.go), so removing a detected root also clears whatever branch-aware
// Cache had been rebuilt inside it; the next `skills update` refetches it.
func (d *legacyCacheDetector) detectRoots() []string {
	roots := make(map[string]struct{})
	for source := range d.cfg.Remote {
		root := filepath.Join(d.cacheDir, filepath.FromSlash(models.ParseRepoSource(source).SourceKey))
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			roots[root] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(roots))
}

// detectRecoveryArtifacts finds every `.legacy-cache-*` / `.doctor-cache-*`
// directory under the Cache directory root: staging and backup trees an
// earlier release's interrupted rebuild left behind.
func (d *legacyCacheDetector) detectRecoveryArtifacts() ([]string, error) {
	var artifacts []string
	err := filepath.WalkDir(d.cacheDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if path != d.cacheDir && entry.IsDir() &&
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
	return artifacts, err
}

// remove deletes every given path independently: one that cannot be removed
// does not stop the others.
func (d *legacyCacheDetector) remove(paths []string) []CacheRemoval {
	removals := make([]CacheRemoval, 0, len(paths))
	for _, path := range paths {
		removals = append(removals, CacheRemoval{Path: path, Repair: itemRepairFromErr(d.removeAll(path))})
	}
	return removals
}
