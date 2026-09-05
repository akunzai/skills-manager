package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const ScopeStateVersion = 1

// AppliedSkillState records the Cache content most recently applied to one
// remote Skill in a Scope.
type AppliedSkillState struct {
	Source         string            `json:"source"`
	CacheIdentity  string            `json:"cache_identity"`
	AppliedCommit  string            `json:"applied_commit"`
	ContentDigests map[string]string `json:"content_digests"`
}

// ScopeState is the versioned applied-state artifact for one Scope.
type ScopeState struct {
	Version   int                          `json:"version"`
	ScopePath string                       `json:"scope_path"`
	Skills    map[string]AppliedSkillState `json:"skills"`
}

// ScopeStateStore addresses applied state by the SHA-256 of a canonical Scope
// path. Artifacts live outside Project checkouts in the XDG state directory.
type ScopeStateStore struct {
	scopePath string
	path      string
}

type ScopeStateArtifact struct {
	Path      string
	ScopePath string
	Err       error
}

func ListScopeStateArtifacts() ([]ScopeStateArtifact, error) {
	dir, err := scopeStateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	artifacts := make([]ScopeStateArtifact, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, readErr := os.ReadFile(path)
		var state ScopeState
		if readErr == nil {
			readErr = json.Unmarshal(data, &state)
		}
		artifacts = append(artifacts, ScopeStateArtifact{Path: path, ScopePath: state.ScopePath, Err: readErr})
	}
	return artifacts, nil
}

// NewScopeStateStore constructs the store for scopePath.
func NewScopeStateStore(scopePath string) (*ScopeStateStore, error) {
	canonical, err := canonicalScopePath(scopePath)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(canonical))
	stateDir, err := scopeStateDir()
	if err != nil {
		return nil, err
	}
	return &ScopeStateStore{
		scopePath: canonical,
		path:      filepath.Join(stateDir, hex.EncodeToString(sum[:])+".json"),
	}, nil
}

// Path returns the local artifact path. It is intended for diagnosis and
// explicit repair; callers should otherwise use Load and Save.
func (s *ScopeStateStore) Path() string { return s.path }

// Load reads the Scope state. A missing artifact is an empty versioned state.
// Invalid artifacts are returned as errors and are never rewritten.
func (s *ScopeStateStore) Load() (ScopeState, error) {
	empty := s.emptyState()
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return ScopeState{}, fmt.Errorf("open Scope state: %w", err)
	}
	defer f.Close()

	var state ScopeState
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(&state); err != nil {
		return ScopeState{}, fmt.Errorf("decode Scope state %s: %w", s.path, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return ScopeState{}, fmt.Errorf("decode Scope state %s: %w", s.path, err)
	}
	if state.Version != ScopeStateVersion {
		return ScopeState{}, fmt.Errorf("unsupported Scope state version %d in %s", state.Version, s.path)
	}
	if state.ScopePath != s.scopePath {
		return ScopeState{}, fmt.Errorf("Scope state path %q does not match %q", state.ScopePath, s.scopePath)
	}
	if state.Skills == nil {
		state.Skills = make(map[string]AppliedSkillState)
	}
	return state, nil
}

// Save atomically replaces the Scope artifact with state.
func (s *ScopeStateStore) Save(state ScopeState) error {
	state.Version = ScopeStateVersion
	state.ScopePath = s.scopePath
	if state.Skills == nil {
		state.Skills = make(map[string]AppliedSkillState)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create Scope state directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".scope-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary Scope state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("set temporary Scope state permissions: %w", err)
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(state); err != nil {
		tmp.Close()
		return fmt.Errorf("encode Scope state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary Scope state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary Scope state: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace Scope state: %w", err)
	}
	return nil
}

// RecordApplied records what Materializing one remote Skill put on the Scope.
// Every command that Materializes a remote Skill must call it: without a
// baseline, classifyRemoteSkill (freshness.go) cannot tell a freshly applied
// Skill from one the user hand-edited, so the next Update reads as
// SkillUnknownBaseline and Sync blocks it instead of applying the update.
func (s *ScopeStateStore) RecordApplied(name, source, cacheIdentity, commit, scopePath string) error {
	digests, err := DigestSkillContent(scopePath)
	if err != nil {
		return err
	}
	state, err := s.Load()
	if err != nil {
		return err
	}
	if state.Skills == nil {
		state.Skills = make(map[string]AppliedSkillState)
	}
	state.Skills[name] = AppliedSkillState{
		Source:         source,
		CacheIdentity:  cacheIdentity,
		AppliedCommit:  commit,
		ContentDigests: digests,
	}
	return s.Save(state)
}

// DeleteSkill removes one Skill's applied baseline while retaining the Scope
// artifact. Corrupt state is preserved and returned as an error.
func (s *ScopeStateStore) DeleteSkill(name string) error {
	exists, err := s.exists()
	if err != nil || !exists {
		return err
	}
	state, err := s.Load()
	if err != nil {
		return err
	}
	delete(state.Skills, name)
	return s.Save(state)
}

// PruneSkills removes baselines not present in keep.
func (s *ScopeStateStore) PruneSkills(keep map[string]struct{}) error {
	exists, err := s.exists()
	if err != nil || !exists {
		return err
	}
	state, err := s.Load()
	if err != nil {
		return err
	}
	for name := range state.Skills {
		if _, ok := keep[name]; !ok {
			delete(state.Skills, name)
		}
	}
	return s.Save(state)
}

// Prune removes this Scope's entire applied-state entry. Missing entries are
// already pruned.
func (s *ScopeStateStore) Prune() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Scope state: %w", err)
	}
	return nil
}

func (s *ScopeStateStore) emptyState() ScopeState {
	return ScopeState{
		Version:   ScopeStateVersion,
		ScopePath: s.scopePath,
		Skills:    make(map[string]AppliedSkillState),
	}
}

func (s *ScopeStateStore) exists() (bool, error) {
	_, err := os.Stat(s.path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect Scope state: %w", err)
}

// DigestSkillContent returns the complete relative path to SHA-256 map for a
// materialized Skill. Symlinks hash their target strings and are not followed.
func DigestSkillContent(root string) (map[string]string, error) {
	digests := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() && (name == ".git" || name == "__pycache__") {
			return filepath.SkipDir
		}
		if entry.IsDir() || strings.HasSuffix(name, ".pyc") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		var content []byte
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			content = []byte(target)
		} else {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			content, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		sum := sha256.Sum256(content)
		digests[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("digest Skill content %s: %w", root, err)
	}
	return digests, nil
}

func canonicalScopePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Scope path: %w", err)
	}
	abs = filepath.Clean(abs)
	if evaluated, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(evaluated), nil
	}

	existing := abs
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve Scope path: %w", err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs, nil
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
	evaluated, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("resolve Scope path: %w", err)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		evaluated = filepath.Join(evaluated, missing[i])
	}
	return filepath.Clean(evaluated), nil
}

func scopeStateDir() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve state home: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(stateHome) {
		abs, err := filepath.Abs(stateHome)
		if err != nil {
			return "", fmt.Errorf("resolve state home: %w", err)
		}
		stateHome = abs
	}
	return filepath.Join(filepath.Clean(stateHome), "skills-manager", "scope-state"), nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
