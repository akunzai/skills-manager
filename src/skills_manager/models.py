"""Data structures and constants for agent-skills-manager."""

import os
import re
import urllib.parse
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional

# Default Paths
DEFAULT_AGENTS_DIR = Path(os.environ.get("AGENTS_HOME", "~/.agents")).expanduser()
DEFAULT_SKILLS_DIR = DEFAULT_AGENTS_DIR / "skills"
DEFAULT_CONFIG_FILE = DEFAULT_AGENTS_DIR / "skills.json"
DEFAULT_CACHE_DIR = DEFAULT_AGENTS_DIR / ".cache" / "repos"

def _resolve_env_path(env_var: str, default_subpath: str) -> Path:
    val = os.environ.get(env_var)
    if val and val.strip():
        return Path(val.strip()).expanduser()
    return Path(default_subpath).expanduser()


def get_known_agents() -> Dict[str, Path]:
    """
    Return dictionary mapping non-universal agent names to their global skills directory.
    Harnesses that natively support ~/.agents/skills (e.g. codex, opencode, cursor, cline, zed, warp, amp)
    are excluded to simplify implementation and avoid redundant symlinks.
    """
    xdg_config = Path(os.environ.get("XDG_CONFIG_HOME", "~/.config")).expanduser()
    claude_home = _resolve_env_path("CLAUDE_CONFIG_DIR", "~/.claude")
    vibe_home = _resolve_env_path("VIBE_HOME", "~/.vibe")
    hermes_home = _resolve_env_path("HERMES_HOME", "~/.hermes")
    autohand_home = _resolve_env_path("AUTOHAND_HOME", "~/.autohand")
    grok_home = _resolve_env_path("GROK_HOME", "~/.grok")

    # OpenClaw directory check
    openclaw_dir = Path("~/.openclaw/skills").expanduser()
    if Path("~/.clawdbot").expanduser().exists():
        openclaw_dir = Path("~/.clawdbot/skills").expanduser()
    elif Path("~/.moltbot").expanduser().exists():
        openclaw_dir = Path("~/.moltbot/skills").expanduser()

    return {
        "adal": Path("~/.adal/skills").expanduser(),
        "aider-desk": Path("~/.aider-desk/skills").expanduser(),
        "astrbot": Path("~/.astrbot/data/skills").expanduser(),
        "augment": Path("~/.augment/skills").expanduser(),
        "autohand-code": autohand_home / "skills",
        "bob": Path("~/.bob/skills").expanduser(),
        "claude-code": claude_home / "skills",
        "codearts-agent": Path("~/.codeartsdoer/skills").expanduser(),
        "codebuddy": Path("~/.codebuddy/skills").expanduser(),
        "codemaker": Path("~/.codemaker/skills").expanduser(),
        "codestudio": Path("~/.codestudio/skills").expanduser(),
        "command-code": Path("~/.commandcode/skills").expanduser(),
        "continue": Path("~/.continue/skills").expanduser(),
        "cortex": Path("~/.snowflake/cortex/skills").expanduser(),
        "crush": Path("~/.config/crush/skills").expanduser(),
        "devin": xdg_config / "devin" / "skills",
        "droid": Path("~/.factory/skills").expanduser(),
        "forgecode": Path("~/.forge/skills").expanduser(),
        "goose": xdg_config / "goose" / "skills",
        "grok": grok_home / "skills",
        "hermes-agent": hermes_home / "skills",
        "iflow-cli": Path("~/.iflow/skills").expanduser(),
        "inference-sh": Path("~/.inferencesh/skills").expanduser(),
        "jazz": Path("~/.jazz/skills").expanduser(),
        "junie": Path("~/.junie/skills").expanduser(),
        "kilo": Path("~/.kilocode/skills").expanduser(),
        "kimchi": Path("~/.config/kimchi/harness/skills").expanduser(),
        "kiro-cli": Path("~/.kiro/skills").expanduser(),
        "kode": Path("~/.kode/skills").expanduser(),
        "lingma": Path("~/.lingma/skills").expanduser(),
        "mcpjam": Path("~/.mcpjam/skills").expanduser(),
        "minimax-code": Path("~/.minimax/skills").expanduser(),
        "mistral-vibe": vibe_home / "skills",
        "moxby": Path("~/.moxby/skills").expanduser(),
        "mux": Path("~/.mux/skills").expanduser(),
        "neovate": Path("~/.neovate/skills").expanduser(),
        "ona": Path("~/.ona/skills").expanduser(),
        "openclaw": openclaw_dir,
        "openhands": Path("~/.openhands/skills").expanduser(),
        "pi": Path("~/.pi/agent/skills").expanduser(),
        "pochi": Path("~/.pochi/skills").expanduser(),
        "posit-assistant": Path("~/.posit/assistant/skills").expanduser(),
        "qoder": Path("~/.qoder/skills").expanduser(),
        "qoder-cn": Path("~/.qoder-cn/skills").expanduser(),
        "qwen-code": Path("~/.qwen/skills").expanduser(),
        "reasonix": Path("~/.reasonix/skills").expanduser(),
        "roo": Path("~/.roo/skills").expanduser(),
        "rovodev": Path("~/.rovodev/skills").expanduser(),
        "tabnine-cli": Path("~/.tabnine/agent/skills").expanduser(),
        "terramind": Path("~/.terramind/skills").expanduser(),
        "tinycloud": Path("~/.tinycloud/skills").expanduser(),
        "trae": Path("~/.trae/skills").expanduser(),
        "trae-cn": Path("~/.trae-cn/skills").expanduser(),
        "windsurf": Path("~/.codeium/windsurf/skills").expanduser(),
        "zcode": Path("~/.zcode/skills").expanduser(),
        "zencoder": Path("~/.zencoder/skills").expanduser(),
    }


KNOWN_AGENTS: Dict[str, Path] = get_known_agents()

# Agents that natively support ~/.agents/skills or share the universal directory
UNIVERSAL_AGENTS: List[str] = [
    "amp",
    "antigravity",
    "antigravity-cli",
    "cline",
    "codex",
    "cursor",
    "deepagents",
    "dexto",
    "firebender",
    "gemini-cli",
    "github-copilot",
    "kimi-code-cli",
    "loaf",
    "opencode",
    "promptscript",
    "replit",
    "warp",
    "zed",
    "universal",
]

AGENT_ALIASES: Dict[str, str] = {
    # Non-universal aliases
    "claude": "claude-code",
    "roo-code": "roo",
    "vibe": "mistral-vibe",
    "hermes": "hermes-agent",
    "autohand": "autohand-code",
    "aider": "aider-desk",
    "codearts": "codearts-agent",
    "iflow": "iflow-cli",
    "kiro": "kiro-cli",
    "kilocode": "kilo",
    "minimax": "minimax-code",
    "posit": "posit-assistant",
    "positai": "posit-assistant",
    "tabnine": "tabnine-cli",
    "factory": "droid",
    "forge": "forgecode",
    "clawdbot": "openclaw",
    "moltbot": "openclaw",
    "opendevin": "openhands",
    "qwen": "qwen-code",
    "zenflow": "zencoder",
    # Universal aliases
    "gemini": "gemini-cli",
    "antigravity": "antigravity-cli",
    "kimi": "kimi-code-cli",
    "kimi-code": "kimi-code-cli",
    "copilot": "github-copilot",
}


def normalize_agent_name(name: str) -> str:
    """Normalize agent name or alias to canonical form."""
    low = name.strip().lower()
    return AGENT_ALIASES.get(low, low)


def is_universal_agent(name: str) -> bool:
    """Check if an agent natively uses ~/.agents/skills."""
    norm = normalize_agent_name(name)
    return norm in UNIVERSAL_AGENTS


@dataclass
class ParsedRepoSource:
    """Parsed repository source with key, clone url, and optional branch/subpath."""
    source_key: str
    url: str
    repo_type: str  # "github", "gitlab", "git"
    branch: Optional[str] = None
    subpath: Optional[str] = None


def parse_repo_source(raw: str) -> ParsedRepoSource:
    """
    Parse repository source string into canonical source key, clone URL, repo type, branch, and subpath.
    Supports:
      - 'gitlab:group/project'
      - 'github:owner/repo'
      - 'https://gitlab.com/group/project'
      - 'https://github.com/owner/repo/tree/main/skills/foo'
      - 'https://gitlab.com/group/project/-/tree/main/skills/foo'
      - 'git@gitlab.com:group/project.git'
      - 'git@github.com:owner/repo.git'
      - 'owner/repo' (defaults to GitHub)
    """
    raw = raw.strip()

    # 1. Prefix: gitlab:group/project
    if raw.lower().startswith("gitlab:"):
        repo_path = raw[7:].strip().strip("/")
        return ParsedRepoSource(
            source_key=f"gitlab.com/{repo_path}",
            url=f"https://gitlab.com/{repo_path}.git",
            repo_type="gitlab"
        )

    # Prefix: github:owner/repo
    if raw.lower().startswith("github:"):
        repo_path = raw[7:].strip().strip("/")
        return ParsedRepoSource(
            source_key=repo_path,
            url=f"https://github.com/{repo_path}.git",
            repo_type="github"
        )

    # 2. SSH URLs: git@host:group/project.git or ssh://git@host/...
    if raw.startswith("git@") or raw.startswith("ssh://"):
        url = raw
        if raw.startswith("git@"):
            match = re.match(r"^git@([^:]+):(.+?)(?:\.git)?$", raw)
            if match:
                host, path = match.group(1), match.group(2).strip("/")
                is_github = host.lower() in ("github.com", "github")
                repo_type = "github" if is_github else ("gitlab" if "gitlab" in host.lower() else "git")
                source_key = path if repo_type == "github" else f"{host}/{path}"
                return ParsedRepoSource(source_key=source_key, url=url, repo_type=repo_type)
        elif raw.startswith("ssh://"):
            parsed = urllib.parse.urlparse(raw)
            host = parsed.hostname or "git"
            path = parsed.path.lstrip("/").removesuffix(".git")
            is_github = "github" in host.lower()
            repo_type = "github" if is_github else ("gitlab" if "gitlab" in host.lower() else "git")
            source_key = path if repo_type == "github" else f"{host}/{path}"
            return ParsedRepoSource(source_key=source_key, url=url, repo_type=repo_type)

    # 3. HTTP / HTTPS URLs
    if raw.startswith("http://") or raw.startswith("https://"):
        parsed = urllib.parse.urlparse(raw)
        host = parsed.netloc.lower()
        path = parsed.path.strip("/")

        repo_type = "github" if "github" in host else ("gitlab" if "gitlab" in host else "git")

        if "github.com" in host:
            parts = path.split("/")
            if len(parts) >= 4 and parts[2] in ("tree", "blob"):
                owner, repo = parts[0], parts[1]
                branch = parts[3]
                subpath = "/".join(parts[4:]) if len(parts) > 4 else None
                source_key = f"{owner}/{repo}"
                clean_url = f"https://{host}/{owner}/{repo}.git"
                return ParsedRepoSource(source_key=source_key, url=clean_url, repo_type=repo_type, branch=branch, subpath=subpath)
            else:
                clean_path = path.removesuffix(".git")
                source_key = clean_path
                clean_url = f"https://{host}/{clean_path}.git"
                return ParsedRepoSource(source_key=source_key, url=clean_url, repo_type=repo_type)

        elif "gitlab" in host:
            if "/-/tree/" in path or "/-/blob/" in path:
                base_part, rest = re.split(r"/-(?:/tree|/blob)/", path, maxsplit=1)
                rest_parts = rest.split("/")
                branch = rest_parts[0] if rest_parts else None
                subpath = "/".join(rest_parts[1:]) if len(rest_parts) > 1 else None
                repo_path = base_part.removesuffix(".git")
                source_key = f"{host}/{repo_path}"
                clean_url = f"https://{host}/{repo_path}.git"
                return ParsedRepoSource(source_key=source_key, url=clean_url, repo_type=repo_type, branch=branch, subpath=subpath)
            elif "/tree/" in path or "/blob/" in path:
                base_part, rest = re.split(r"/(?:tree|blob)/", path, maxsplit=1)
                rest_parts = rest.split("/")
                branch = rest_parts[0] if rest_parts else None
                subpath = "/".join(rest_parts[1:]) if len(rest_parts) > 1 else None
                repo_path = base_part.removesuffix(".git")
                source_key = f"{host}/{repo_path}"
                clean_url = f"https://{host}/{repo_path}.git"
                return ParsedRepoSource(source_key=source_key, url=clean_url, repo_type=repo_type, branch=branch, subpath=subpath)
            else:
                clean_path = path.removesuffix(".git")
                source_key = f"{host}/{clean_path}"
                clean_url = f"https://{host}/{clean_path}.git"
                return ParsedRepoSource(source_key=source_key, url=clean_url, repo_type=repo_type)
        else:
            clean_path = path.removesuffix(".git")
            source_key = f"{host}/{clean_path}"
            protocol = "https" if raw.startswith("https://") else "http"
            clean_url = f"{protocol}://{host}/{clean_path}.git"
            return ParsedRepoSource(source_key=source_key, url=clean_url, repo_type=repo_type)

    # 4. Plain shorthand: e.g. "owner/repo" or "gitlab.com/group/project"
    clean_raw = raw.removesuffix(".git").strip("/")
    if clean_raw.startswith("gitlab.com/"):
        return ParsedRepoSource(
            source_key=clean_raw,
            url=f"https://{clean_raw}.git",
            repo_type="gitlab"
        )
    elif "/" in clean_raw and not clean_raw.startswith("github.com/"):
        parts = clean_raw.split("/")
        if "." in parts[0]:
            host = parts[0]
            repo_type = "gitlab" if "gitlab" in host.lower() else "git"
            return ParsedRepoSource(
                source_key=clean_raw,
                url=f"https://{clean_raw}.git",
                repo_type=repo_type
            )
        else:
            return ParsedRepoSource(
                source_key=clean_raw,
                url=f"https://github.com/{clean_raw}.git",
                repo_type="github"
            )
    else:
        clean_key = clean_raw.removeprefix("github.com/")
        return ParsedRepoSource(
            source_key=clean_key,
            url=f"https://github.com/{clean_key}.git",
            repo_type="github"
        )


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
