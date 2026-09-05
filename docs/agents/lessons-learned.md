# Lessons Learned & Gotchas

Key development insights, architectural gotchas, and environment nuances discovered during the development of `skills-manager`.

## CLI & Testing

- **`[cobra/flag-isolation]`**: In Cobra test suites, both persistent flags and subcommand closure targets leak across `RootCmd.Execute()` calls within the same process. Test cases must call `resetRootCmdFlags()` (`cli_test.go`), which walks commands to restore default values and clear `f.Changed`.
- **`[testing/network-isolation]`**: Calling `ObserveFreshness()` (`engine/remote_source.go`) on unmocked remote source keys without an explicit branch triggers `git ls-remote --symref` to detect the default branch, leaking live network requests into unit tests. Always specify a branch in test fixtures when asserting local cache path logic.

## Terminal & UI

- **`[tty/raw-escape-sequences]`**: In raw terminal mode, multi-byte ANSI escape sequences (e.g. arrow keys `\x1b[A`, `Home` `\x1b[H`) arrive in packets. Rather than reading single bytes sequentially, read into a buffer (e.g. `os.File.Read(buf)` with length check) and inspect byte patterns to reliably distinguish standalone `Esc` keypresses from navigation escapes without timing jitter.

## Configuration & File System

- **`[config/symlinks]`**: In-place text edits (e.g. `sed -i ''` on macOS/BSD) fail on symlinked configuration files (e.g. `~/.agents/skills.json` symlinked to cloud storage / KeepSync) with `in-place editing only works for regular files`. Always write directly to the resolved target file or use direct atomic file writes (`os.WriteFile`) to preserve symlink integrity.
- **`[macos/bsd-sed-word-boundary]`**: BSD `sed` (macOS default) does not support `\b`, so a bulk rename like `sed -i '' 's/\bfoo\b/Bar/g' *.go` matches nothing and exits 0 — the rename silently no-ops, and `go build` still passes because the untouched code is still valid. Nothing downstream catches it. Use `python3`/`perl` for word-boundary rewrites, and verify a rename landed by grepping for the old identifier rather than trusting the exit code.

## Windows & Platform Compatibility

- **`[powershell/windowsapps-stub]`**: On Windows, Microsoft Store App Execution Aliases can create 0-byte stub executables in `PATH`. This causes `Get-Command` to return truthy while execution fails with exit code 9009. When locating external binaries in PowerShell installer scripts, test execution with error suppression (`2>$null`), check `$LASTEXITCODE -eq 0`, and validate output rather than relying solely on `Get-Command`.
