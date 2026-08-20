# Skills Manager Developer Guidelines

Global skills manager CLI (`skills` / `skills-manager`) for AI coding agents.

This project uses Python (>=3.10) with standard library only (no third-party dependencies).

## Commands

- Run tests: `python3 -m unittest discover -s tests`
- Build / install standalone binary: `./install.sh`
- Manual zipapp build: `python3 -m zipapp src -m "skills_manager.cli:main" -p "/usr/bin/env python3" -o ~/.local/bin/skills`
- Run local CLI: `PYTHONPATH=src python3 -m skills_manager.cli <command>`

## Pointers

- Schema definition: @skills.schema.json
- CLI definition: @src/skills_manager/cli.py
- Engine & Git caching: @src/skills_manager/engine.py
- Terminal UI prompt: @src/skills_manager/ui.py
- Updater module: @src/skills_manager/updater.py
- Config manager: @src/skills_manager/config.py
- Core models & paths: @src/skills_manager/models.py
- Unit tests: @tests/test_manager.py
- Issue tracker: @docs/agents/issue-tracker.md
- Triage labels: @docs/agents/triage-labels.md
- Domain docs: @docs/agents/domain.md
- Release SOP: @docs/release.md

## Claude Code Compatibility

`CLAUDE.md` is a symbolic link pointing to `AGENTS.md`. Edit `AGENTS.md` directly.

## Self-Reflection

- **Candidate**: Distill a non-obvious gotcha into ≤ 2 context-tagged bullets. Propose it before writing.
- **Promote**: On confirmation, write it to a dedicated file — merge an existing topic doc, else `docs/<topic>.md`, else `docs/lessons-learned.md`. Add or update one `@path` line under Pointers.
- **Prune**: Drop entries once stale (obsolete version, now enforced, duplicated, or a transcript) — not by a fixed count.
