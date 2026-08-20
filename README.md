# Skills Manager

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Python: >=3.10](https://img.shields.io/badge/Python->=3.10-blue.svg)](https://www.python.org/)

Fast, zero-dependency global skills manager for AI coding agents (Claude Code, Codex, Cursor, Gemini CLI, Cline, Zed, OpenCode, etc.).

Replaces `npx skills` for global/user-level management with a deterministic `skills.json` configuration, fast Git shallow clone caching, local/CLI tool skill support, and multi-agent symlink synchronization.

---

## Installation

```bash
# One-line installer
curl -fsSL https://raw.githubusercontent.com/akunzai/skills-manager/main/install.sh | bash

# Or install from local clone
./install.sh
```

Packages a standalone executable directly to `~/.local/bin/skills`.

---

## Commands

```bash
# List installed skills (table or JSON)
skills ls
skills ls --json
skills ls -a claude-code

# Add remote git skill(s)
skills add mattpocock/skills --skill ask-matt
skills add akunzai/agent-skills --all

# Add local symlink skill
skills add --symlink ~/.local/share/terminal-browser/app/skills/default/terminal-browser --skill terminal-browser

# Add CLI command skill
skills add --command "agentsview skills install --force" --check "which agentsview" --skill agentsview-finding-history

# Remove skill(s) and unlink from agents
skills rm ponytail-review
skills rm ask-matt --agent codex

# Sync and restore all skills from ~/.agents/skills.json
skills sync
skills sync --force     # force re-fetch & re-link
skills sync --prune     # remove untracked skills
skills sync --dry-run

# Health check & auto-repair
skills doctor
skills doctor --fix
```

---

## Configuration (`~/.agents/skills.json`)

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "version": 1,
  "settings": {
    "defaultAgents": ["claude-code", "codex", "cursor", "cline", "gemini-cli", "opencode", "zed"],
    "excludeAgents": ["promptscript"],
    "agentExclusions": {
      "claude-code": []
    }
  },
  "remote": {
    "mattpocock/skills": {
      "type": "github",
      "skills": {
        "ask-matt": "skills/engineering/ask-matt",
        "code-review": "skills/engineering/code-review"
      }
    }
  },
  "local": {
    "terminal-browser": {
      "type": "symlink",
      "source": "~/.local/share/terminal-browser/app/skills/default/terminal-browser"
    },
    "agentsview-finding-history": {
      "type": "command",
      "check": "which agentsview",
      "command": "agentsview skills install --force"
    }
  },
  "postHooks": [
    {
      "name": "agentsview-claude-symlink",
      "run": "rm -rf ~/.claude/skills/agentsview-finding-history && ln -sf ~/.agents/skills/agentsview-finding-history ~/.claude/skills/agentsview-finding-history"
    }
  ]
}
```

---

## Development

```bash
# Run unit tests
python3 -m unittest discover -s tests

# Build zipapp executable manually
python3 -m zipapp src -m "skills_manager.cli:main" -p "/usr/bin/env python3" -o ~/.local/bin/skills
```

---

## License

[MIT License](LICENSE) © 2026 Charley Wu
