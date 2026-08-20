"""Configuration management for skills.json."""

import json
from pathlib import Path
from typing import Any, Dict, Optional, Tuple

from .models import DEFAULT_CONFIG_FILE, normalize_agent_name


def get_default_config() -> Dict[str, Any]:
    """Return default baseline configuration structure."""
    return {
        "$schema": "https://json-schema.org/draft/2020-12/schema",
        "version": 1,
        "settings": {
            "defaultAgents": [
                "claude-code"
            ],
            "excludeAgents": [],
            "agentExclusions": {}
        },
        "remote": {},
        "local": {},
        "postHooks": []
    }


def load_config(config_path: Optional[Path] = None) -> Dict[str, Any]:
    """Load configuration from JSON file or return default if missing."""
    target = config_path or DEFAULT_CONFIG_FILE
    if not target.exists():
        return get_default_config()

    try:
        with open(target, "r", encoding="utf-8") as f:
            data = json.load(f)
            if not isinstance(data, dict):
                raise ValueError("Config root must be a JSON object")
            if "version" not in data:
                data["version"] = 1
            data.setdefault("settings", {})
            data.setdefault("remote", {})
            data.setdefault("local", {})
            data.setdefault("postHooks", [])
            return data
    except Exception as e:
        raise RuntimeError(f"Failed to read skills config from {target}: {e}") from e


def save_config(data: Dict[str, Any], config_path: Optional[Path] = None) -> None:
    """Save configuration to JSON file formatted nicely."""
    target = config_path or DEFAULT_CONFIG_FILE
    target.parent.mkdir(parents=True, exist_ok=True)

    # Reorder top-level keys for clean readability
    ordered: Dict[str, Any] = {}
    if "$schema" in data:
        ordered["$schema"] = data["$schema"]
    ordered["version"] = data.get("version", 1)
    if "settings" in data:
        ordered["settings"] = data["settings"]
    if "remote" in data:
        # Sort repos alphabetically
        ordered["remote"] = dict(sorted(data["remote"].items()))
    if "local" in data:
        ordered["local"] = dict(sorted(data["local"].items()))
    if "postHooks" in data:
        ordered["postHooks"] = data["postHooks"]

    # Preserve any other custom fields
    for k, v in data.items():
        if k not in ordered:
            ordered[k] = v

    with open(target, "w", encoding="utf-8") as f:
        json.dump(ordered, f, indent=2, ensure_ascii=False)
        f.write("\n")


def find_skill_source(config_data: Dict[str, Any], skill_name: str) -> Optional[Tuple[str, str, Dict[str, Any]]]:
    """Find skill location in config. Returns (category, source_key, entry_data)."""
    # Check remote
    for source_key, repo_info in config_data.get("remote", {}).items():
        skills = repo_info.get("skills", {})
        if skill_name in skills:
            return ("remote", source_key, repo_info)

    # Check local
    local_skills = config_data.get("local", {})
    if skill_name in local_skills:
        return ("local", skill_name, local_skills[skill_name])

    return None


def add_remote_skill_entry(
    config_data: Dict[str, Any],
    source: str,
    skill_name: str,
    subpath: str,
    repo_type: str = "github",
    url: Optional[str] = None
) -> None:
    """Add or update a remote skill in configuration."""
    remote = config_data.setdefault("remote", {})
    repo_entry = remote.setdefault(source, {
        "type": repo_type,
        "skills": {}
    })
    repo_entry["type"] = repo_type
    if url:
        repo_entry["url"] = url
    repo_entry.setdefault("skills", {})[skill_name] = subpath


def add_local_symlink_entry(
    config_data: Dict[str, Any],
    skill_name: str,
    source_path: str,
    description: Optional[str] = None
) -> None:
    """Add or update a local symlink skill in configuration."""
    local = config_data.setdefault("local", {})
    entry = {
        "type": "symlink",
        "source": str(source_path)
    }
    if description:
        entry["description"] = description
    local[skill_name] = entry


def add_local_command_entry(
    config_data: Dict[str, Any],
    skill_name: str,
    command: str,
    check_cmd: Optional[str] = None,
    description: Optional[str] = None
) -> None:
    """Add or update a local command-installed skill in configuration."""
    local = config_data.setdefault("local", {})
    entry: Dict[str, Any] = {
        "type": "command",
        "command": command
    }
    if check_cmd:
        entry["check"] = check_cmd
    if description:
        entry["description"] = description
    local[skill_name] = entry


def remove_skill_entry(config_data: Dict[str, Any], skill_name: str) -> bool:
    """Remove a skill from remote or local sections of config."""
    found = False
    # Check remote
    for source_key, repo_info in list(config_data.get("remote", {}).items()):
        skills = repo_info.get("skills", {})
        if skill_name in skills:
            del skills[skill_name]
            found = True
            if not skills:
                del config_data["remote"][source_key]

    # Check local
    local = config_data.get("local", {})
    if skill_name in local:
        del local[skill_name]
        found = True

    return found
