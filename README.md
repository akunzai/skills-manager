# Skills Manager

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go: >=1.27](https://img.shields.io/badge/Go->=1.27-00ADD8.svg?logo=go)](https://golang.org/)

Fast, zero-runtime skills manager for AI coding agents (Claude Code, Codex, GitHub Copilot CLI, Antigravity CLI, etc.).

Deterministic `skills.json` configuration, centralized Git shallow clone caching, and automatic multi-agent symlink synchronization.

---

## ⚡ Quick Install

```bash
# macOS / Linux (bash / zsh)
curl -fsSL https://raw.githubusercontent.com/akunzai/skills-manager/main/install.sh | bash

# Windows (PowerShell)
irm https://raw.githubusercontent.com/akunzai/skills-manager/main/install.ps1 | iex
```

---

## 🚀 Quick Start

```bash
# Install skills from GitHub
skills add akunzai/agent-skills

# List installed skills
skills ls

# Check for updates & upgrade
skills outdated
skills update

# Sync / restore declared skills
skills sync
```

---

## 🌐 Global & Project Scopes

| Scope | Flag | Config File | Skills Directory | Agent Symlinks |
| :--- | :--- | :--- | :--- | :--- |
| **Global** (Default) | `-g` / `--global` | `~/.agents/skills.json` | `~/.agents/skills/` | `~/.claude/skills/`, etc. |
| **Project** | `-p` / `--project` | `./.agents/skills.json` | `./.agents/skills/` | `./.claude/skills/`, etc. |

> **Note**: Git caches are centralized in `~/.local/state/skills-manager/repo-cache`, keeping project workspaces 100% clean.

---

<details>
<summary>📖 <strong>Full Command Reference</strong></summary>

### Adding Skills
```bash
# Interactive selection
skills add akunzai/agent-skills

# Specific skill(s) or all skills
skills add akunzai/agent-skills -s agents-md -s tidy-commits
skills add akunzai/agent-skills --all

# GitLab or Git URLs
skills add gitlab:my-org/my-skills
skills add https://github.com/owner/repo/tree/main/skills/foo

# Local folder symlink
skills add --symlink ~/code/my-skill --skill my-skill

# Custom CLI installer command
skills add --command "agentsview skills install" --check "which agentsview" --skill finding-history

# Project-scoped installation
skills add -p akunzai/agent-skills -s agents-md
```

### Managing & Inspecting Skills
```bash
# List skills (table or JSON)
skills ls
skills ls --json
skills ls -a claude
skills ls -s akunzai
skills ls -p # project scope

# Interactive removal
skills rm
skills rm tidy-commits
skills rm -p agents-md  # project scope
```

### Sync & Maintenance
```bash
# Sync / restore declared skills
skills sync
skills sync --force     # force re-fetch & re-link
skills sync --prune     # remove untracked skills & unconfigured links
skills sync --dry-run
skills sync -p          # restore project skills

# Check outdated & update
skills outdated
skills outdated -p
skills update
skills update akunzai/agent-skills
skills update -p

# Health check & auto-repair
skills doctor
skills doctor --fix
skills doctor -p

# Self-update binary
skills self-update
skills self-update --check
```

</details>

<details>
<summary>⚙️ <strong>Configuration (<code>skills.json</code>)</strong></summary>

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "version": 1,
  "settings": {
    "defaultAgents": ["claude"]
  },
  "remote": {
    "akunzai/agent-skills": {
      "type": "github",
      "skills": {
        "agents-md": "skills/agents-md",
        "tidy-commits": "skills/tidy-commits"
      }
    }
  },
  "local": {
    "my-tool": {
      "type": "symlink",
      "source": "~/tools/my-tool"
    }
  }
}
```

</details>

<details>
<summary>🐚 <strong>Shell Autocompletion</strong></summary>

```bash
# Zsh (add to ~/.zshrc)
source <(skills completion zsh)

# Bash (add to ~/.bashrc)
source <(skills completion bash)

# Fish
skills completion fish | source

# PowerShell
skills completion powershell | Out-String | Invoke-Expression
```

</details>
