package updater

import "testing"

func TestClassifyExecutablePath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		goos        string
		wantName    string
		wantCommand string
	}{
		{
			name:        "macOS Apple Silicon Homebrew prefix",
			path:        "/opt/homebrew/Cellar/skills-manager/0.19.0/bin/skills",
			goos:        "darwin",
			wantName:    "Homebrew",
			wantCommand: "brew upgrade akunzai/tap/skills-manager",
		},
		{
			name:        "macOS Intel Homebrew prefix",
			path:        "/usr/local/Cellar/skills-manager/0.19.0/bin/skills",
			goos:        "darwin",
			wantName:    "Homebrew",
			wantCommand: "brew upgrade akunzai/tap/skills-manager",
		},
		{
			name:        "Linuxbrew prefix",
			path:        "/home/linuxbrew/.linuxbrew/Cellar/skills-manager/0.19.0/bin/skills",
			goos:        "linux",
			wantName:    "Homebrew",
			wantCommand: "brew upgrade akunzai/tap/skills-manager",
		},
		{
			name:        "custom Homebrew --prefix",
			path:        "/srv/tools/brew/Cellar/skills-manager/0.19.0/bin/skills",
			goos:        "linux",
			wantName:    "Homebrew",
			wantCommand: "brew upgrade akunzai/tap/skills-manager",
		},
		{
			name:        "symlink into Cellar, already resolved",
			path:        "/opt/homebrew/Cellar/skills-manager/0.19.1/bin/skills",
			goos:        "darwin",
			wantName:    "Homebrew",
			wantCommand: "brew upgrade akunzai/tap/skills-manager",
		},
		{
			name:        "Scoop per-user install",
			path:        `C:\Users\alice\scoop\apps\skills-manager\0.19.0\skills.exe`,
			goos:        "windows",
			wantName:    "Scoop",
			wantCommand: "scoop update skills-manager",
		},
		{
			name:        "Scoop current version symlink",
			path:        `C:\Users\alice\scoop\apps\skills-manager\current\skills.exe`,
			goos:        "windows",
			wantName:    "Scoop",
			wantCommand: "scoop update skills-manager",
		},
		{
			name:        "Scoop global install under ProgramData",
			path:        `C:\ProgramData\scoop\apps\skills-manager\0.19.0\skills.exe`,
			goos:        "windows",
			wantName:    "Scoop",
			wantCommand: "scoop update skills-manager",
		},
		{
			name:        "Scoop segments matched case-insensitively on Windows",
			path:        `C:\Users\alice\Scoop\Apps\skills-manager\current\skills.exe`,
			goos:        "windows",
			wantName:    "Scoop",
			wantCommand: "scoop update skills-manager",
		},
		{
			name: "differently-cased scoop path is not matched off Windows",
			path: `/home/alice/Scoop/Apps/skills-manager/current/skills`,
			goos: "linux",
		},
		{
			name: "default ~/.local/bin install",
			path: "/home/alice/.local/bin/skills",
			goos: "linux",
		},
		{
			name: "default Windows install path",
			path: `C:\Users\alice\.local\bin\skills.exe`,
			goos: "windows",
		},
		{
			name: "scoop shim without an apps segment",
			path: `C:\Users\alice\scoop\shims\skills.exe`,
			goos: "windows",
		},
		{
			name: "apps segment without a preceding scoop segment",
			path: `C:\Users\alice\tools\apps\skills-manager\skills.exe`,
			goos: "windows",
		},
		{
			name: "empty path",
			path: "",
			goos: "linux",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyExecutablePath(tt.path, tt.goos)
			if tt.wantName == "" {
				if got != nil {
					t.Fatalf("ClassifyExecutablePath(%q, %q) = %+v; want nil", tt.path, tt.goos, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ClassifyExecutablePath(%q, %q) = nil; want %s", tt.path, tt.goos, tt.wantName)
			}
			if got.Name != tt.wantName || got.Command != tt.wantCommand {
				t.Fatalf("ClassifyExecutablePath(%q, %q) = %+v; want {%s %s}", tt.path, tt.goos, got, tt.wantName, tt.wantCommand)
			}
		})
	}
}
