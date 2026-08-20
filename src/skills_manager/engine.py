"""Core execution engine for skills caching, installation, symlinking, and hooks."""

import os
import re
import shutil
import subprocess
from pathlib import Path
from typing import Any, Dict, List, Optional, Set, Tuple

from .models import (
    DEFAULT_CACHE_DIR,
    DEFAULT_SKILLS_DIR,
    KNOWN_AGENTS,
    SkillItem,
    normalize_agent_name,
)

# Regex to parse frontmatter skill name
FRONTMATTER_NAME_REGEX = re.compile(r"^name:\s*[\"']?([a-zA-Z0-9_\-\.]+)[\"']?", re.MULTILINE)


def run_cmd(cmd: str, cwd: Optional[Path] = None, check: bool = True, capture: bool = False) -> subprocess.CompletedProcess:
    """Run shell command safely."""
    return subprocess.run(
        cmd,
        shell=True,
        cwd=str(cwd) if cwd else None,
        check=check,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
    )


def ensure_git_repo(
    source: str,
    url: Optional[str] = None,
    branch: Optional[str] = None,
    force_update: bool = False,
    cache_dir: Optional[Path] = None
) -> Path:
    """Clone or update git repo in cache directory with shallow depth."""
    base_cache = cache_dir or DEFAULT_CACHE_DIR
    clean_source = source.strip().rstrip(".git")
    repo_dest = base_cache / clean_source
    repo_url = url or f"https://github.com/{clean_source}.git"

    if repo_dest.exists() and (repo_dest / ".git").exists():
        if force_update:
            try:
                # Fetch latest shallow commit
                run_cmd(f"git fetch --depth 1 origin {branch or 'HEAD'}", cwd=repo_dest, check=False)
                run_cmd("git reset --hard FETCH_HEAD", cwd=repo_dest, check=False)
            except Exception:
                pass
        return repo_dest

    # Clone new
    repo_dest.parent.mkdir(parents=True, exist_ok=True)
    if repo_dest.exists():
        shutil.rmtree(repo_dest)

    clone_cmd = f"git clone --depth 1 {f'--branch {branch}' if branch else ''} {repo_url} \"{repo_dest}\""
    res = run_cmd(clone_cmd, check=False, capture=True)
    if res.returncode != 0:
        raise RuntimeError(f"Failed to clone {repo_url}: {res.stderr.strip() or res.stdout.strip()}")

    return repo_dest


def parse_skill_name_from_md(skill_md_path: Path) -> Optional[str]:
    """Parse skill name from SKILL.md frontmatter if available."""
    try:
        content = skill_md_path.read_text(encoding="utf-8")
        if content.startswith("---"):
            parts = content.split("---", 2)
            if len(parts) >= 3:
                frontmatter = parts[1]
                match = FRONTMATTER_NAME_REGEX.search(frontmatter)
                if match:
                    return match.group(1).strip()
    except Exception:
        pass
    return None


def discover_skills_in_repo(repo_dir: Path) -> Dict[str, str]:
    """
    Search repo for all directories containing SKILL.md.
    Returns map of { skill_name: relative_dir_path }.
    """
    found: Dict[str, str] = {}
    for root, _, files in os.walk(repo_dir):
        if ".git" in root.split(os.sep):
            continue
        for f in files:
            if f.lower() == "skill.md":
                skill_file = Path(root) / f
                skill_dir = skill_file.parent
                rel_path = os.path.relpath(skill_dir, repo_dir)
                rel_path_str = "." if rel_path == "." else rel_path

                # Name priority: frontmatter > directory name > repo name
                name = parse_skill_name_from_md(skill_file)
                if not name:
                    if rel_path_str == ".":
                        name = repo_dir.name
                    else:
                        name = skill_dir.name

                found[name] = rel_path_str
    return found


def copy_skill_folder(src_dir: Path, target_dir: Path) -> None:
    """Copy skill directory cleanly, removing existing target first."""
    if target_dir.is_symlink() or target_dir.is_file():
        target_dir.unlink()
    elif target_dir.is_dir():
        shutil.rmtree(target_dir)

    target_dir.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(src_dir, target_dir, symlinks=True, ignore=shutil.ignore_patterns(".git", "*.pyc", "__pycache__"))


def is_skill_excluded_for_agent(
    skill_name: str,
    source: str,
    agent: str,
    agent_exclusions: Dict[str, List[str]]
) -> bool:
    """Check if skill is excluded for a specific agent."""
    norm_agent = normalize_agent_name(agent)
    for k, patterns in agent_exclusions.items():
        if normalize_agent_name(k) == norm_agent:
            for pat in patterns:
                p_low = str(pat).strip().lower()
                if p_low == skill_name.lower() or p_low == source.lower():
                    return True
    return False


def get_target_agents_for_skill(
    skill_name: str,
    source: str,
    config_data: Dict[str, Any]
) -> List[str]:
    """Calculate the list of active agents that should receive symlinks for this skill."""
    settings = config_data.get("settings", {})
    default_agents = settings.get("defaultAgents", list(KNOWN_AGENTS.keys()))
    exclude_agents = set(normalize_agent_name(a) for a in settings.get("excludeAgents", []))
    agent_exclusions = settings.get("agentExclusions", {})

    active_agents = []
    for a in default_agents:
        norm = normalize_agent_name(a)
        if norm in exclude_agents:
            continue
        if is_skill_excluded_for_agent(skill_name, source, norm, agent_exclusions):
            continue
        active_agents.append(norm)

    return active_agents


def ensure_agent_symlink(
    skill_name: str,
    agent_name: str,
    skills_dir: Path
) -> bool:
    """Create symlink in agent's skills directory pointing to master ~/.agents/skills/{skill_name}."""
    norm_agent = normalize_agent_name(agent_name)
    agent_dir = KNOWN_AGENTS.get(norm_agent)
    if not agent_dir:
        return False

    master_skill_path = skills_dir / skill_name
    if not master_skill_path.exists() and not master_skill_path.is_symlink():
        return False

    # Special handling for gemini-cli which links the whole directory if desired
    if norm_agent == "gemini-cli" and agent_dir.is_symlink():
        try:
            if os.path.realpath(agent_dir) == os.path.realpath(skills_dir):
                return True
        except Exception:
            pass

    agent_dir.mkdir(parents=True, exist_ok=True)
    agent_link = agent_dir / skill_name

    # Determine symlink target (relative link for cleaner portability)
    try:
        rel_target = os.path.relpath(master_skill_path, agent_dir)
    except ValueError:
        rel_target = str(master_skill_path)

    if agent_link.is_symlink():
        try:
            current_target = os.readlink(agent_link)
            if current_target == rel_target or os.path.realpath(agent_link) == os.path.realpath(master_skill_path):
                return True
        except Exception:
            pass
        agent_link.unlink()
    elif agent_link.is_dir():
        # Physical directory exists in agent directory (e.g. agentsview default install)
        shutil.rmtree(agent_link)
    elif agent_link.exists():
        agent_link.unlink()

    os.symlink(rel_target, agent_link)
    return True


def remove_agent_symlinks(skill_name: str) -> List[str]:
    """Remove symlinks for a skill across all known agents."""
    removed = []
    for agent_name, agent_dir in KNOWN_AGENTS.items():
        link_path = agent_dir / skill_name
        if link_path.is_symlink() or link_path.exists():
            try:
                if link_path.is_symlink() or link_path.is_file():
                    link_path.unlink()
                elif link_path.is_dir():
                    shutil.rmtree(link_path)
                removed.append(agent_name)
            except Exception:
                pass
    return removed


def execute_post_hooks(post_hooks: List[Dict[str, Any]], dry_run: bool = False) -> List[Tuple[str, bool, str]]:
    """Execute post-sync hooks (e.g. agentsview claude symlink fix)."""
    results = []
    for hook in post_hooks:
        name = hook.get("name", "unnamed-hook")
        desc = hook.get("description", "")
        cond = hook.get("condition")
        cmd = hook.get("run", "")

        if not cmd:
            continue

        if cond:
            res_cond = run_cmd(cond, check=False)
            if res_cond.returncode != 0:
                results.append((name, True, f"Condition not met ({cond}), skipped"))
                continue

        if dry_run:
            results.append((name, True, f"[Dry-run] Would execute: {cmd}"))
            continue

        try:
            res = run_cmd(cmd, check=True, capture=True)
            results.append((name, True, f"Success: {desc or cmd}"))
        except subprocess.CalledProcessError as e:
            results.append((name, False, f"Failed ({e.returncode}): {e.stderr.strip() or e.stdout.strip()}"))

    return results


def scan_all_skills(config_data: Dict[str, Any], skills_dir: Optional[Path] = None) -> List[SkillItem]:
    """
    Scan all configured and installed skills, returning unified status list.
    """
    base_skills = skills_dir or DEFAULT_SKILLS_DIR
    configured_names: Set[str] = set()
    items: Dict[str, SkillItem] = {}

    # 1. Configured Remote Skills
    for source_key, repo_info in config_data.get("remote", {}).items():
        skills = repo_info.get("skills", {})
        for name, subpath in skills.items():
            configured_names.add(name)
            items[name] = SkillItem(
                name=name,
                source_type="github",
                source=source_key,
                subpath=subpath
            )

    # 2. Configured Local Skills
    for name, local_info in config_data.get("local", {}).items():
        configured_names.add(name)
        stype = local_info.get("type", "local")
        src = local_info.get("source") or local_info.get("command") or "local"
        items[name] = SkillItem(
            name=name,
            source_type=f"local_{stype}",
            source=src,
            description=local_info.get("description")
        )

    # 3. Check Physical Directory State in ~/.agents/skills/
    if base_skills.exists():
        for entry in base_skills.iterdir():
            name = entry.name
            if name.startswith("."):
                continue

            item = items.get(name)
            if not item:
                # Untracked local skill
                is_link = entry.is_symlink()
                target_src = os.readlink(entry) if is_link else "local"
                item = SkillItem(
                    name=name,
                    source_type="symlink" if is_link else "untracked",
                    source=target_src,
                    installed_path=entry
                )
                items[name] = item

            item.installed_path = entry
            item.is_installed = True
            item.is_valid_skill = (entry / "SKILL.md").exists()

    # 4. Check Agent Link States
    for name, item in items.items():
        linked = []
        for agent_name, agent_dir in KNOWN_AGENTS.items():
            link_path = agent_dir / name
            if link_path.exists() or link_path.is_symlink():
                linked.append(agent_name)
        item.linked_agents = linked

    return sorted(items.values(), key=lambda x: x.name.lower())
