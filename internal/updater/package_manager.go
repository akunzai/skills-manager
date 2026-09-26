package updater

import "strings"

// PackageManagerInstall names the package manager that owns this
// executable's install and the command self-update should point at instead
// of replacing the binary itself.
type PackageManagerInstall struct {
	Name    string
	Command string
}

// Homebrew's acceptance policy forbids a formula from updating itself, and a
// Scoop manifest owns the binary the same way, so self-update must refuse to
// replace either and name the upgrade command instead.
const (
	HomebrewUpgradeCommand = "brew upgrade akunzai/tap/skills-manager"
	ScoopUpgradeCommand    = "scoop update skills-manager"
)

// ClassifyExecutablePath reports which package manager, if any, owns path -
// the running executable's path with symlinks already resolved
// (GetCurrentExecutablePath does this). It is a pure function of path and
// goos, so every case is testable without a real Homebrew or Scoop install.
//
// Homebrew is detected by a "Cellar" path segment rather than a hard-coded
// prefix: the prefix varies (/opt/homebrew, /usr/local,
// /home/linuxbrew/.linuxbrew, or a custom --prefix), but every install
// resolves through <prefix>/Cellar/<formula>/<version>/... regardless.
// HOMEBREW_PREFIX and HOMEBREW_CELLAR are deliberately not consulted: they
// describe the invoking shell's environment, not the file being classified,
// and self-update can be invoked from a script, a cron job, or another
// shell that does not carry them even when the executable itself sits under
// Cellar.
//
// Scoop is detected by the path segment sequence "scoop" ... "apps",
// matched case-insensitively only when goos is "windows" - Scoop only
// installs there, and Windows paths are case-insensitive on disk.
func ClassifyExecutablePath(path, goos string) *PackageManagerInstall {
	segments := pathSegments(path)

	for _, seg := range segments {
		if seg == "Cellar" {
			return &PackageManagerInstall{Name: "Homebrew", Command: HomebrewUpgradeCommand}
		}
	}

	if isScoopApps(segments, goos == "windows") {
		return &PackageManagerInstall{Name: "Scoop", Command: ScoopUpgradeCommand}
	}

	return nil
}

// pathSegments splits path on either separator, so a Windows-style path
// classifies correctly even when fed to a build running on another OS (as
// package_manager_test.go does).
func pathSegments(path string) []string {
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
}

// isScoopApps reports whether segments contains "scoop" followed later by
// "apps", such as .../scoop/apps/skills-manager/current/skills.exe. A
// "scoop" segment with no later "apps" (the shims directory, for instance)
// does not match.
func isScoopApps(segments []string, caseInsensitive bool) bool {
	sawScoop := false
	for _, seg := range segments {
		if !sawScoop {
			sawScoop = equalSegment(seg, "scoop", caseInsensitive)
			continue
		}
		if equalSegment(seg, "apps", caseInsensitive) {
			return true
		}
	}
	return false
}

func equalSegment(seg, want string, caseInsensitive bool) bool {
	if caseInsensitive {
		return strings.EqualFold(seg, want)
	}
	return seg == want
}
