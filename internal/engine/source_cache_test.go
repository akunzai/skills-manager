package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akunzai/skills-manager/internal/wellknowntest"
)

// sourceCacheCase is one kind of Cache behind the sourceCache seam, opened on
// a Source that publishes one Skill whose SKILL.md is first.
type sourceCacheCase struct {
	name string
	// open publishes the Source and opens its Cache, not yet fetched.
	open func(t *testing.T, first string) sourceCacheFixture
}

type sourceCacheFixture struct {
	cache   sourceCache
	subpath string
	// url is where the Source is read from.
	url string
	// version is what the Source now publishes for subpath, which a
	// Baseline records as the version applied.
	version func() string
	// publish replaces the Skill's SKILL.md at the Source.
	publish func(t *testing.T, body string)
}

func sourceCacheCases() []sourceCacheCase {
	return []sourceCacheCase{
		{name: "git", open: openGitSourceCache},
		{name: "well-known", open: openWellKnownSourceCache},
	}
}

func openGitSourceCache(t *testing.T, first string) sourceCacheFixture {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	write := func(body string) {
		writeDiffFile(t, filepath.Join(origin, "skills", "sample", "SKILL.md"), body)
	}
	write(first)
	mustGit(t, origin, "init")
	mustGit(t, origin, "config", "user.email", "test@example.com")
	mustGit(t, origin, "config", "user.name", "test")
	mustGit(t, origin, "config", "uploadpack.allowFilter", "true")
	commit := func(message string) {
		mustGit(t, origin, "add", "-A")
		mustGit(t, origin, "commit", "-m", message)
	}
	commit("init")
	url := localFileURL(origin)
	branch := mustGit(t, origin, "symbolic-ref", "--short", "HEAD")
	return sourceCacheFixture{
		cache:   NewCache("owner/repo", url, branch, filepath.Join(root, "cache")),
		subpath: "skills/sample",
		url:     url,
		version: func() string { return mustGit(t, origin, "rev-parse", "HEAD") },
		publish: func(t *testing.T, body string) {
			write(body)
			commit("publish")
		},
	}
}

func openWellKnownSourceCache(t *testing.T, first string) sourceCacheFixture {
	t.Helper()
	server, cache, _ := newWellKnownTestCache(t, wellknowntest.SkillMDBody("sample", "Sample", first))
	return sourceCacheFixture{
		cache:   cache,
		subpath: "sample",
		url:     server.URL,
		version: func() string { return server.Entries[0].Digest },
		publish: func(t *testing.T, body string) {
			server.SetEntries(wellknowntest.SkillMDBody("sample", "Sample", body))
		},
	}
}

func TestSourceCacheHoldsNothingBeforeItIsFetched(t *testing.T) {
	for _, tc := range sourceCacheCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.open(t, "# Sample\n")
			if head := f.cache.head(); head != "" {
				t.Fatalf("head() = %q; want none before a fetch", head)
			}
			if f.cache.atHead(f.subpath).exists() {
				t.Fatalf("atHead(%s) exists before a fetch", f.subpath)
			}
		})
	}
}

func TestSourceCacheCoversAFetchedSkillAtTheVersionItPublishes(t *testing.T) {
	for _, tc := range sourceCacheCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.open(t, "# Sample\n")
			if err := f.cache.RefreshWithProgress(true, nil); err != nil {
				t.Fatal(err)
			}
			if err := f.cache.Cover(f.subpath); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(f.cache.dir(), filepath.FromSlash(f.subpath), "SKILL.md"))
			if err != nil || string(data) != "# Sample\n" {
				t.Fatalf("SKILL.md under dir() = %q, %v; want the published one", data, err)
			}
			coverage, err := f.cache.coverage()
			if err != nil || len(coverage.missing([]string{f.subpath})) > 0 {
				t.Fatalf("coverage() = %+v, %v; want it to cover %s", coverage, err, f.subpath)
			}
			if got, want := f.cache.appliedCommit(f.subpath), f.version(); got != want {
				t.Fatalf("appliedCommit(%s) = %q; want the published version %q", f.subpath, got, want)
			}
			if url, _ := f.cache.described(); url != f.url {
				t.Fatalf("described() URL = %q; want %q", url, f.url)
			}
		})
	}
}

func TestSourceCacheSaysWhichSkillsCanCarryASignature(t *testing.T) {
	t.Run("git", func(t *testing.T) {
		f := openGitSourceCache(t, "# Sample\n")
		if err := f.cache.RefreshWithProgress(true, nil, f.subpath); err != nil {
			t.Fatal(err)
		}
		if !f.cache.canCarrySignature(f.subpath) {
			t.Fatalf("canCarrySignature(%s) = false; a git Skill's directory can hold its signature", f.subpath)
		}
	})
	t.Run("well-known", func(t *testing.T) {
		_, cache, _ := newWellKnownTestCache(t,
			wellknowntest.SkillMD("single", "Single"),
			wellknowntest.TarGz(t, "bundle", "Bundle", wellknowntest.File{Name: "SKILL.md", Body: wellknowntest.SkillDoc("bundle")}),
		)
		if err := cache.RefreshWithProgress(true, nil, "single", "bundle"); err != nil {
			t.Fatal(err)
		}
		var seam sourceCache = cache
		if seam.canCarrySignature("single") {
			t.Fatal("canCarrySignature(single) = true; a single SKILL.md entry has nowhere to carry one")
		}
		if !seam.canCarrySignature("bundle") {
			t.Fatal("canCarrySignature(bundle) = false; an archive can carry skill.oms.sig")
		}
	})
}

func TestSourceCacheKeepsEarlierVersionsOnlyWhenTheSourceDoes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		open     func(t *testing.T, first string) sourceCacheFixture
		versions bool
	}{
		{"git", openGitSourceCache, true},
		{"well-known", openWellKnownSourceCache, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.open(t, "# Sample\nfirst\n")
			if err := f.cache.RefreshWithProgress(true, nil, f.subpath); err != nil {
				t.Fatal(err)
			}
			earlier := f.cache.appliedCommit(f.subpath)
			f.publish(t, "# Sample\nsecond\n")
			if err := f.cache.RefreshWithProgress(true, nil, f.subpath); err != nil {
				t.Fatal(err)
			}
			history, ok := f.cache.history()
			if ok != tc.versions {
				t.Fatalf("history() ok = %v; want %v", ok, tc.versions)
			}
			if !ok {
				return
			}
			patch, err := history.patch(earlier, f.cache.head(), f.subpath)
			if err != nil || !strings.Contains(patch, "-first") || !strings.Contains(patch, "+second") {
				t.Fatalf("patch(earlier, head) = %q, %v; want first replaced by second", patch, err)
			}
			blob, err := history.blob(earlier, f.subpath+"/SKILL.md")
			if err != nil || string(blob) != "# Sample\nfirst\n" {
				t.Fatalf("blob(earlier) = %q, %v; want the earlier SKILL.md", blob, err)
			}
		})
	}
}

// TestOnlyTheCacheKindsNameTheirOwnType keeps the sourceCache seam whole: a
// caller that asks which kind of Cache it holds would decide per kind what
// the two adapters exist to decide (ADR-0013).
func TestOnlyTheCacheKindsNameTheirOwnType(t *testing.T) {
	adapters := map[string]bool{"cache.go": true, "wellknown_cache.go": true, "source_cache.go": true}
	kinds := map[string]bool{"Cache": true, "WellKnownCache": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if adapters[name] || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			var types []ast.Expr
			switch n := n.(type) {
			case *ast.TypeAssertExpr:
				types = append(types, n.Type)
			case *ast.TypeSwitchStmt:
				for _, clause := range n.Body.List {
					types = append(types, clause.(*ast.CaseClause).List...)
				}
			}
			for _, expr := range types {
				if star, ok := expr.(*ast.StarExpr); ok {
					expr = star.X
				}
				if ident, ok := expr.(*ast.Ident); ok && kinds[ident.Name] {
					t.Errorf("%s asserts a sourceCache is a %s; ask the seam instead", fset.Position(expr.Pos()), ident.Name)
				}
			}
			return true
		})
	}
}
