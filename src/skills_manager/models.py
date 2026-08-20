"""Data structures and constants for agent-skills-manager."""

import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional

# Default Paths
DEFAULT_AGENTS_DIR = Path(os.environ.get("AGENTS_HOME", "~/.agents")).expanduser()
DEFAULT_SKILLS_DIR = DEFAULT_AGENTS_DIR / "skills"
DEFAULT_CONFIG_FILE = DEFAULT_AGENTS_DIR / "skills.json"
DEFAULT_CACHE_DIR = DEFAULT_AGENTS_DIR / ".cache" / "repos"

# Agent Directory Mappings
KNOWN_AGENTS: Dict[str, Path] = {
    "claude-code": Path("~/.claude/skills").expanduser(),
    "codex": Path("~/.codex/skills").expanduser(),
    "cursor": Path("~/.cursor/skills").expanduser(),
    "cline": Path("~/.cline/skills").expanduser(),
    "gemini-cli": Path("~/.gemini/skills").expanduser(),
    "antigravity-cli": Path("~/.gemini/antigravity-cli/skills").expanduser(),
    "opencode": Path("~/.config/opencode/skills").expanduser(),
    "zed": Path("~/.config/zed/skills").expanduser(),
    "amp": Path("~/.amp/skills").expanduser(),
    "deepagents": Path("~/.deepagents/skills").expanduser(),
    "kimi-code-cli": Path("~/.kimi/skills").expanduser(),
    "warp": Path("~/.warp/skills").expanduser(),
}

AGENT_ALIASES: Dict[str, str] = {
    "claude": "claude-code",
    "gemini": "gemini-cli",
    "antigravity": "gemini-cli",
    "kimi": "kimi-code-cli",
    "kimi-code": "kimi-code-cli",
}


def normalize_agent_name(name: str) -> str:
    """Normalize agent name or alias to canonical form."""
    low = name.strip().lower()
    return AGENT_ALIASES.get(low, low)


@dataclass
class SkillItem:
    """Represents an installed or configured skill."""
    name: str
    source_type: str  # "github", "symlink", "command", "untracked"
    source: str
    subpath: Optional[str] = None
    installed_path: Optional[Path] = None
    is_installed: bool = False
    is_valid_skill: bool = False
    linked_agents: List[str] = field(default_factory=list)
    description: Optional[str] = None

    def to_dict(self) -> Dict[str, Any]:
        return {
            "name": self.name,
            "sourceType": self.source_type,
            "source": self.source,
            "subpath": self.subpath,
            "installed": self.is_installed,
            "valid": self.is_valid_skill,
            "path": str(self.installed_path) if self.installed_path else None,
            "agents": self.linked_agents,
            "description": self.description,
        }
