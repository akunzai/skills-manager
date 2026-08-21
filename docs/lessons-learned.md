# Lessons Learned & Gotchas

Key development insights, architectural gotchas, and environment nuances discovered during the development of `skills-manager`.

## CLI & Testing

- **`[cobra/flag-isolation]`**: In Cobra CLI test suites, Persistent Flags (e.g. `--config`, `--global`, `--project`) retain their parsed state across multiple `RootCmd.Execute()` calls within the same test process. Test cases must explicitly reset flag variables and invoke `RootCmd.PersistentFlags().Set(name, "")` to prevent cross-test state leakage.
- **`[cobra/flag-isolation]`**: Subcommand flags leak the same way and are easier to miss: commands are built once in `init()`, so their flag targets are closure variables that survive every `Execute()`. A `--skill` left over from an earlier test silently renamed the skill a later one installed. Reset by walking `RootCmd.Commands()` and, per flag, calling `SliceValue.Replace(nil)` or `Value.Set(f.DefValue)` and clearing `f.Changed`.
- **`[cobra/silence-usage]`**: Cobra prints the usage block for *any* error returned from `RunE`, so a runtime failure reads as if the user mistyped the command — `doctor` reported its findings and then dumped every flag. Flag parsing and arg validation both finish before `RunE`, so set `cmd.SilenceUsage = true` at the top of `RunE` and clear it again only in genuine misuse checks. Setting it on the root command instead would also swallow usage for real flag errors, which share the same error path (`command.go`, `if !cmd.SilenceUsage && !c.SilenceUsage`).

## Terminal & UI

- **`[tty/raw-escape-sequences]`**: In raw terminal mode, multi-byte ANSI escape sequences (e.g. arrow keys `\x1b[A`, `Home` `\x1b[H`) arrive in packets. Rather than reading single bytes sequentially, read into a buffer (e.g. `os.File.Read(buf)` with length check) and inspect byte patterns to reliably distinguish standalone `Esc` keypresses from navigation escapes without timing jitter.

## Configuration & File System

- **`[config/symlinks]`**: In-place text edits (e.g. `sed -i ''` on macOS/BSD) fail on symlinked configuration files (e.g. `~/.agents/skills.json` symlinked to cloud storage / KeepSync) with `in-place editing only works for regular files`. Always write directly to the resolved target file or use direct atomic file writes (`os.WriteFile`) to preserve symlink integrity.
- **`[config/portable-sources]`**: Project-scoped `.agents/skills.json` is meant to be committed, so any path stored in it must resolve on someone else's checkout. `add -p --symlink ./x` recording an absolute path made a teammate's `sync` link back to the author's machine — and on the same machine it silently "worked", resolving into the *other* project rather than failing loudly. Store sources inside the project relative to the project root; keep sources outside it absolute.
- **`[symlink/project-relative]`**: When dispatching agent symlinks in project-scoped mode (e.g. `./.claude/skills/<name>`), target paths must use `filepath.Rel(agentDir, masterSkillPath)` to produce clean relative symlinks (`../../.agents/skills/<name>`) rather than absolute paths, ensuring repository portability across different machines, worktrees, and containers.

## Windows & Platform Compatibility

- **`[powershell/windowsapps-stub]`**: On Windows, Microsoft Store App Execution Aliases can create 0-byte stub executables in `PATH`. This causes `Get-Command` to return truthy while execution fails with exit code 9009. When locating external binaries in PowerShell installer scripts, test execution with error suppression (`2>$null`), check `$LASTEXITCODE -eq 0`, and validate output rather than relying solely on `Get-Command`.
