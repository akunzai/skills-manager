# Skills Manager Developer Guidelines

Skills manager CLI (`skills` / `skills-manager`) for AI coding agents (Claude Code, Codex, GitHub Copilot CLI, Antigravity CLI, etc.).

This project is written in Go (>=1.27) and compiled to standalone cross-platform binaries with zero language runtime dependencies (uses system `git` for remote repository operations).

## Commands

- Run every CI check locally: `mise run check` (gofmt, vet, `test -race`, build — in CI's order)
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
- Scope paths: @internal/models/scope.go
- GoReleaser config: @.goreleaser.yaml
- Issue tracker: @docs/agents/issue-tracker.md
- Triage labels: @docs/agents/triage-labels.md
- Domain glossary: @CONTEXT.md
- Domain docs: @docs/agents/domain.md
- Engine Sync: @internal/engine/sync.go
- Product UI, CLI output, and README design: @docs/agents/design.md
- Release SOP: @docs/agents/release.md
- Lessons learned: @docs/agents/lessons-learned.md
- Exit-code contract: @docs/adr/0002-exit-codes-express-state.md
- Agent skill & guide: @skills-manager/SKILL.md
- Gold-standard CLI test: @internal/cli/cli_test.go
- Gold-standard engine test: @internal/engine/sync_plan_test.go

## Claude Code Compatibility

`CLAUDE.md` is a symbolic link pointing to `AGENTS.md`. Edit `AGENTS.md` directly.

## Prevent Recurrence

- **Candidate**: Name who hits this again, in which file, on what change. No such scenario, nothing to propose.
- **Promote**: Offer the first tier that reaches them and only that one, pending confirmation — enforce it (assert/type/test) with its size quoted, else a comment at that site, else an agent-facing doc (`docs/agents/<topic>.md`, else `docs/agents/lessons-learned.md`) with one `@path` line under Pointers and one sentence on why the tiers above cannot hold it. Never both.
- **Prune**: When adding to a file, audit the rest of it in the same pass. Drop entries once stale (obsolete version, now enforced, duplicated, or a transcript) — not by a fixed count.
