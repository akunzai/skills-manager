# Skills Manager Developer Guidelines

Skills manager CLI (`skills` / `skills-manager`) for AI coding agents (Claude Code, Codex, GitHub Copilot CLI, Antigravity CLI, etc.).

This project is written in Go (>=1.27) and compiled to standalone cross-platform binaries with zero language runtime dependencies (uses system `git` for remote repository operations).

## Commands

- Run tests: `go test -v ./...`
- Build / install standalone binary: `./install.sh`
- Local binary build: `go build -o skills ./cmd/skills`
- Run local CLI: `go run ./cmd/skills <command>`

## Pointers

- Schema definition: @skills.schema.json
- CLI entrypoint: @cmd/skills/main.go
- CLI commands: @internal/cli/root.go
- Engine & Git caching: @internal/engine/update.go
- Terminal UI prompt: @internal/tui/prompt.go
- Updater module: @internal/updater/updater.go
- Config manager: @internal/config/config.go
- Core models & paths: @internal/models/models.go
- GoReleaser config: @.goreleaser.yaml
- Issue tracker: @docs/agents/issue-tracker.md
- Triage labels: @docs/agents/triage-labels.md
- Domain docs: @docs/agents/domain.md
- Release SOP: @docs/release.md
- Lessons learned: @docs/lessons-learned.md

## Claude Code Compatibility

`CLAUDE.md` is a symbolic link pointing to `AGENTS.md`. Edit `AGENTS.md` directly.

## Self-Reflection

- **Candidate**: Distill a non-obvious gotcha into ≤ 2 context-tagged bullets. Propose it before writing.
- **Promote**: On confirmation, write it to a dedicated file — merge an existing topic doc, else `docs/<topic>.md`, else `docs/lessons-learned.md`. Add or update one `@path` line under Pointers.
- **Prune**: Drop entries once stale (obsolete version, now enforced, duplicated, or a transcript) — not by a fixed count.
