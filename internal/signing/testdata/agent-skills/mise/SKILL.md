---
name: mise
description: >-
  mise: set up a project's toolchain in mise.toml, migrate Makefile or npm
  scripts to tasks, or wire mise into CI or containers.
license: MIT
metadata:
  capabilities: shell, network, installs-tools, writes-outside-repo, runs-repo-commands
---

# mise

One committed `mise.toml` is the single source for tools, runtimes, and
tasks. Official surface: https://mise.jdx.dev/. This skill is the
conventions.

## Host

1. Author repo-root `mise.toml` with `[tools]` and `[tasks.*]`. That
   filename is the default; other mise config paths (`.mise.toml`,
   `mise/config.toml`, …) only when the user names the file. Edit an
   existing mise config in place rather than adding a second one.
   Versions live only there — after a migrate, drop `package.json`
   `packageManager` / corepack. Prefer `aqua:` and `github:` (built in,
   provenance-verified); `ubi:owner/repo` becomes `github:owner/repo`.
   Enable idiomatic files only for interop:
   `mise settings add idiomatic_version_file_enable_tools node`.
   Done when the file is repo-root `mise.toml` (unless the user named
   another path) and tool versions are declared only there, except
   idiomatic files enabled for interop.

2. **Prefix** runtimes; `latest` for linters. `go = "1.27"`,
   `python = "3.14"`, `java = "temurin-25"`, `node = "lts"` install the
   newest matching release. Exact patches stay frozen. Go ≤1.20 needs
   `prefix:1.20` because `1.20` is an exact tag
   ([Go](https://mise.jdx.dev/lang/go.html)). npm ranges (`1.27.x`,
   `^1.27`) are rejected.

```toml
[tools]
shellcheck = "latest"
actionlint = "latest"
uv = "latest"
go = "1.27"
node = "lts"
java = "temurin-25"
python = "3.14"

[tasks.test]
description = "Run all test scripts"
run = "..."

[tasks.lint]
description = "Run all linters"
depends = ["lint-shell", "lint-actions"]
```

3. Translate Makefile or npm script targets into `[tasks]` verbatim:
   same names, same shell (pipes, loops, `$()`), `depends` for the old
   order. Drive work with `mise run <task>`.

4. First checkout: `mise trust`, then `mise install`. Done when
   `mise run <task>` runs on the declared tools. Untrusted configs make
   install and tasks silently no-op; `jdx/mise-action` trusts in CI.
   On a Gatekeeper warning for a precompiled binary (e.g. PHP):
   `xattr -d com.apple.quarantine ~/.local/share/mise/installs/<tool>/<version>/...`.

## CI

`jdx/mise-action@v4` reads `mise.toml` and replaces per-tool setup
actions (`actions/setup-python`, `astral-sh/setup-uv`, …). Run work
through tasks:

```yaml
- uses: jdx/mise-action@v4
- run: mise run test
- run: mise run lint
```

Scope a job with `install_args` (`node aube`) so versions still come
only from `mise.toml` and a JS job does not pull unrelated SDKs. Done
when the job has no duplicate setup action for a tool already in
`mise.toml`.

## Containers

Pin the mise binary by version and sha256 (`curl | bash` is not a pin).
`mise install --system` lands in `/usr/local/share/mise`; make it
world-readable so a uid-dropped runtime user sees the tools. Done when
that user can run the declared binaries.

## Related

- [`aube`](../aube/SKILL.md) — Node.js package manager installed through
  mise.
