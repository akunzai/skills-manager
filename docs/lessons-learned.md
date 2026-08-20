# Lessons Learned & Gotchas

Key development insights, architectural gotchas, and environment nuances discovered during the development of `skills-manager`.

## CLI & Testing

- **`[cobra/flag-isolation]`**: In Cobra CLI test suites, Persistent Flags (e.g. `--config`, `--global`, `--project`) retain their parsed state across multiple `RootCmd.Execute()` calls within the same test process. Test cases must explicitly reset flag variables and invoke `RootCmd.PersistentFlags().Set(name, "")` to prevent cross-test state leakage.

## Terminal & UI

- **`[tty/raw-escape-sequences]`**: In raw terminal mode, multi-byte ANSI escape sequences (e.g. arrow keys `\x1b[A`, `Home` `\x1b[H`) arrive in packets. Rather than reading single bytes sequentially, read into a buffer (e.g. `os.File.Read(buf)` with length check) and inspect byte patterns to reliably distinguish standalone `Esc` keypresses from navigation escapes without timing jitter.

## Configuration & File System

- **`[config/symlinks]`**: In-place text edits (e.g. `sed -i ''` on macOS/BSD) fail on symlinked configuration files (e.g. `~/.agents/skills.json` symlinked to cloud storage / KeepSync) with `in-place editing only works for regular files`. Always write directly to the resolved target file or use direct atomic file writes (`os.WriteFile`) to preserve symlink integrity.
- **`[symlink/project-relative]`**: When dispatching agent symlinks in project-scoped mode (e.g. `./.claude/skills/<name>`), target paths must use `filepath.Rel(agentDir, masterSkillPath)` to produce clean relative symlinks (`../../.agents/skills/<name>`) rather than absolute paths, ensuring repository portability across different machines, worktrees, and containers.

## Windows & Platform Compatibility

- **`[powershell/windowsapps-stub]`**: On Windows, Microsoft Store App Execution Aliases can create 0-byte stub executables in `PATH`. This causes `Get-Command` to return truthy while execution fails with exit code 9009. When locating external binaries in PowerShell installer scripts, test execution with error suppression (`2>$null`), check `$LASTEXITCODE -eq 0`, and validate output rather than relying solely on `Get-Command`.
