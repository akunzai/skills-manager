"""Core execution engine for skills caching, installation, symlinking, and hooks."""

import os
import re
import shutil
import subprocess
from concurrent.futures import ThreadPoolExecutor
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
    clean_source = source.strip().removesuffix(".git")
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
    default_agents = settings.get("defaultAgents", ["claude-code"])
    exclude_agents = set(normalize_agent_name(a) for a in settings.get("excludeAgents", []))
    agent_exclusions = settings.get("agentExclusions", {})

    active_agents = []
    for a in default_agents:
        norm = normalize_agent_name(a)
        if norm in exclude_agents:
            continue
        if is_skill_excluded_for_agent(skill_name, source, norm, agent_exclusions):
            continue
        if norm in KNOWN_AGENTS:
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

    return sorted(items.values(), key=lambda x: (x.source.lower(), x.name.lower()))


def get_local_repo_commit(repo_dest: Path) -> Optional[str]:
    """Get the current HEAD commit hash of the local cached repo."""
    if not repo_dest.exists() or not (repo_dest / ".git").exists():
        return None
    try:
        res = run_cmd("git rev-parse HEAD", cwd=repo_dest, check=True, capture=True)
        return res.stdout.strip()
    except Exception:
        return None


def get_remote_repo_commit(
    source: str,
    url: Optional[str] = None,
    branch: Optional[str] = None
) -> Optional[str]:
    """Query remote repo commit hash using git ls-remote without fetching objects."""
    clean_source = source.strip().removesuffix(".git")
    repo_url = url or f"https://github.com/{clean_source}.git"
    ref_target = branch or "HEAD"
    try:
        res = run_cmd(f"git ls-remote {repo_url} {ref_target}", check=True, capture=True)
        output = res.stdout.strip()
        if not output:
            return None
        first_line = output.splitlines()[0]
        parts = first_line.split()
        if parts:
            return parts[0].strip()
    except Exception:
        return None
    return None


def check_repo_update_status(
    source: str,
    repo_info: Dict[str, Any],
    cache_dir: Optional[Path] = None
) -> Dict[str, Any]:
    """Check whether a remote repository in cache is up-to-date with remote HEAD."""
    base_cache = cache_dir or DEFAULT_CACHE_DIR
    clean_source = source.strip().removesuffix(".git")
    repo_dest = base_cache / clean_source

    url = repo_info.get("url")
    branch = repo_info.get("branch")
    skills = repo_info.get("skills", {})

    local_sha = get_local_repo_commit(repo_dest)
    remote_sha = get_remote_repo_commit(source, url=url, branch=branch)

    if local_sha is None:
        status = "not_installed"
    elif remote_sha is None:
        status = "error"
    elif local_sha == remote_sha:
        status = "up_to_date"
    else:
        status = "update_available"

    return {
        "source": source,
        "url": url or f"https://github.com/{clean_source}.git",
        "branch": branch or "HEAD",
        "skills": list(skills.keys()),
        "local_sha": local_sha,
        "remote_sha": remote_sha,
        "status": status,
        "cache_path": repo_dest,
    }


def check_all_remote_skills_outdated(
    config_data: Dict[str, Any],
    cache_dir: Optional[Path] = None,
    max_workers: int = 8
) -> List[Dict[str, Any]]:
    """Check update status for all configured remote repositories concurrently."""
    remote_repos = config_data.get("remote", {})
    if not remote_repos:
        return []

    sources = list(remote_repos.keys())

    def _check(src: str) -> Dict[str, Any]:
        return check_repo_update_status(src, remote_repos[src], cache_dir=cache_dir)

    with ThreadPoolExecutor(max_workers=min(max_workers, len(sources) or 1)) as executor:
        results = list(executor.map(_check, sources))

    return results


def update_remote_skills(
    config_data: Dict[str, Any],
    targets: Optional[List[str]] = None,
    force: bool = False,
    dry_run: bool = False,
    skills_dir: Optional[Path] = None,
    cache_dir: Optional[Path] = None,
    on_progress: Optional[Any] = None,
) -> Dict[str, Any]:
    """
    Update remote repositories and sync skills to skills_dir and target agents.
    If targets is specified, only updates matching repos or skills.
    If targets is omitted and force is False, rapidly checks all repos in parallel
    and only updates repos where an update is available or not installed.
    """
    base_skills = skills_dir or DEFAULT_SKILLS_DIR
    base_cache = cache_dir or DEFAULT_CACHE_DIR
    remote_repos = config_data.get("remote", {})

    base_skills.mkdir(parents=True, exist_ok=True)

    target_set = set(t.strip().lower() for t in targets) if targets else None

    repos_to_process: Dict[str, Dict[str, Any]] = {}
    for source, repo_info in remote_repos.items():
        if target_set is None:
            repos_to_process[source] = repo_info
        else:
            source_lower = source.lower()
            source_parts = source_lower.split("/")
            repo_name = source_parts[-1] if source_parts else source_lower
            skills = repo_info.get("skills", {})
            skill_names_lower = [s.lower() for s in skills.keys()]

            matched = False
            if source_lower in target_set or repo_name in target_set:
                matched = True
            elif any(sk in target_set for sk in skill_names_lower):
                matched = True

            if matched:
                repos_to_process[source] = repo_info

    updated_repos = []
    updated_skills = []
    skipped_repos = []
    errors = []

    # 1. Fast parallel check when no targets specified and not force
    repos_needing_update: Dict[str, Dict[str, Any]] = {}

    if not force and target_set is None:
        if on_progress:
            on_progress("check_start", {"total": len(repos_to_process)})

        status_results = check_all_remote_skills_outdated(config_data, cache_dir=base_cache)
        status_map = {r["source"]: r for r in status_results}

        for source, repo_info in repos_to_process.items():
            skills = repo_info.get("skills", {})
            status_info = status_map.get(source, {})
            status = status_info.get("status")

            if status == "up_to_date":
                all_exist = all((base_skills / sk).exists() for sk in skills.keys())
                if all_exist:
                    skipped_repos.append({
                        "source": source,
                        "reason": "up_to_date",
                        "local_sha": status_info.get("localSha"),
                        "skills": list(skills.keys()),
                    })
                    if not dry_run:
                        for name in skills.keys():
                            for agent in get_target_agents_for_skill(name, source, config_data):
                                ensure_agent_symlink(name, agent, base_skills)
                    continue

            repos_needing_update[source] = repo_info

        if on_progress:
            on_progress("check_done", {
                "total": len(repos_to_process),
                "up_to_date": len(skipped_repos),
                "outdated": len(repos_needing_update),
            })
    else:
        repos_needing_update = repos_to_process

    # 2. Process updates for repos that need them
    update_list = list(repos_needing_update.items())
    total_to_update = len(update_list)

    for i, (source, repo_info) in enumerate(update_list, 1):
        skills = repo_info.get("skills", {})
        branch = repo_info.get("branch")
        url = repo_info.get("url")

        if on_progress:
            on_progress("update_start", {
                "source": source,
                "index": i,
                "total": total_to_update,
                "skills": list(skills.keys()),
                "dry_run": dry_run,
            })

        if dry_run:
            updated_repos.append({
                "source": source,
                "skills": list(skills.keys()),
                "dry_run": True,
            })
            for sk in skills.keys():
                updated_skills.append(sk)
            continue

        try:
            repo_dir = ensure_git_repo(
                source,
                url=url,
                branch=branch,
                force_update=True,
                cache_dir=base_cache,
            )
        except Exception as e:
            err_msg = str(e)
            errors.append({"source": source, "error": err_msg})
            if on_progress:
                on_progress("repo_error", {"source": source, "error": err_msg})
            continue

        repo_updated_skills = []
        for name, subpath in skills.items():
            src_path = repo_dir / subpath
            target_path = base_skills / name

            if not src_path.exists():
                err_msg = f"Path missing in repository: {subpath}"
                errors.append({"source": source, "skill": name, "error": err_msg})
                if on_progress:
                    on_progress("repo_error", {"source": source, "skill": name, "error": err_msg})
                continue

            copy_skill_folder(src_path, target_path)
            repo_updated_skills.append(name)
            updated_skills.append(name)

            for agent in get_target_agents_for_skill(name, source, config_data):
                ensure_agent_symlink(name, agent, base_skills)

            if on_progress:
                on_progress("skill_restored", {"source": source, "skill": name, "subpath": subpath})

        new_sha = get_local_repo_commit(repo_dir)
        updated_repos.append({
            "source": source,
            "skills": repo_updated_skills,
            "new_sha": new_sha,
        })
        if on_progress:
            on_progress("repo_done", {
                "source": source,
                "new_sha": new_sha,
                "skills": repo_updated_skills,
            })

    # 3. Post hooks
    post_hook_results = []
    if updated_skills and not dry_run:
        post_hooks = config_data.get("postHooks", [])
        if post_hooks:
            if on_progress:
                on_progress("hooks_start", {})
            post_hook_results = execute_post_hooks(post_hooks, dry_run=False)
            if on_progress:
                for name, ok, msg in post_hook_results:
                    on_progress("hook_done", {"name": name, "ok": ok, "msg": msg})

    return {
        "updated_repos": updated_repos,
        "updated_skills": updated_skills,
        "skipped_repos": skipped_repos,
        "errors": errors,
        "post_hooks": post_hook_results,
    }
