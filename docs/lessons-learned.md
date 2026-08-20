# Lessons Learned & Gotchas

Key development insights, architectural gotchas, and environment nuances discovered during the development of `skills-manager`.

## Terminal & UI

- **`[tty/read-key]`**: In Raw TTY terminal mode, reading with `sys.stdin.read(1)` causes Python's userspace `io.TextIOWrapper` / `BufferedReader` to buffer subsequent bytes of multi-byte escape sequences (such as arrow keys `\x1b[A`). This causes OS-level `select.select([sys.stdin], ...)` to report 0 remaining bytes and time out, misinterpreting arrow keys as a single `\x1b` (Escape/Cancel). Always use `os.read(fd, 32)` on the raw file descriptor to receive the full packet directly without userspace buffering.

## Configuration & File System

- **`[config/symlinks]`**: In-place edits (e.g. `sed -i ''` on macOS/BSD) fail on symlinked configuration files (e.g. `~/.agents/skills.json` symlinked to cloud storage / KeepSync) with `in-place editing only works for regular files`. Always use direct Python file writes or resolve the symlink target first to preserve symlink integrity.

## Windows & Platform Compatibility

- **`[powershell/windowsapps-stub]`**: On Windows, Microsoft Store App Execution Aliases create 0-byte stub executables (`python.exe`, `python3.exe`) in PATH even when Python is not installed. This causes `Get-Command` to return truthy while execution fails with exit code 9009 and empty stdout. When locating Python or external CLI binaries in PowerShell scripts, always test execution with error suppression (`2>$null`), verify `$LASTEXITCODE -eq 0`, and validate stdout structure rather than relying solely on `Get-Command`.
