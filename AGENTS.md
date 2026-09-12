# Skills Manager Developer Guidelines

Skills manager CLI (`skills` / `skills-manager`) for AI coding agents (Claude Code, Codex, GitHub Copilot CLI, Antigravity CLI, etc.).

This project is written in Go (>=1.27) and compiled to standalone cross-platform binaries with zero language runtime dependencies (uses system `git` for remote repository operations).

## Pointers

- When filing or triaging an issue, read `docs/agents/issue-tracker.md`
- When opening a pull or merge request, read `docs/agents/pull-request.md`
- Before running or reporting verification, read `docs/agents/verification.md`
- When designing product UI, CLI output, or README, read `docs/agents/design.md`
- When preparing a release, read `docs/agents/release.md`
- Schema definition: `skills.schema.json`
- CLI entrypoint: `cmd/skills/main.go`
- CLI commands: `internal/cli/root.go`
- Engine & Git caching: `internal/engine/update.go`
- Terminal UI prompt: `internal/tui/prompt.go`
- Updater module: `internal/updater/updater.go`
- Config manager: `internal/config/config.go`
- Core models & paths: `internal/models/models.go`
- Scope paths: `internal/models/scope.go`
- GoReleaser config: `.goreleaser.yaml`
- Triage labels: `docs/agents/triage-labels.md`
- Domain glossary: `CONTEXT.md`
- Domain docs: `docs/agents/domain.md`
- Engine sync: `internal/engine/sync.go`
- Lessons learned: `docs/agents/lessons-learned.md`
- Exit-code contract: `docs/adr/0002-exit-codes-express-state.md`
- Windows Availability mechanism: `docs/adr/0003-windows-availability-is-copied-not-junctioned.md`
- Agent skill & guide: `skills-manager/SKILL.md`
- Gold-standard CLI test: `internal/cli/cli_test.go`
- Gold-standard engine test: `internal/engine/sync_plan_test.go`

## Claude Code Compatibility

`CLAUDE.md` is a symbolic link pointing to `AGENTS.md`. Edit `AGENTS.md` directly.

## Prevent Recurrence

- **Candidate**: Name who hits this again, in which file, on what change. No such scenario, nothing to propose.
- **Promote**: Offer the first tier that reaches them and only that one, pending confirmation — enforce it (assert/type/test) with its size quoted, else a comment at that site, else an agent-facing doc (`docs/agents/<topic>.md`, else `docs/agents/lessons-learned.md`) with one backtick-path line under Pointers and one sentence on why the tiers above cannot hold it. Never both.
- **Prune**: When adding to a file, audit the rest of it in the same pass. Drop entries once stale (obsolete version, now enforced, duplicated, or a transcript) — not by a fixed count.
