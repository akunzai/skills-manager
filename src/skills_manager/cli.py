"""Command-line interface for agent-skills-manager."""

import argparse
import json
import os
import shutil
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional

from . import __version__
from .config import (
    add_local_command_entry,
    add_local_symlink_entry,
    add_remote_skill_entry,
    load_config,
    remove_skill_entry,
    save_config,
)
from .engine import (
    check_all_remote_skills_outdated,
    copy_skill_folder,
    discover_skills_in_repo,
    ensure_agent_symlink,
    ensure_git_repo,
    execute_post_hooks,
    get_target_agents_for_skill,
    remove_agent_symlinks,
    run_cmd,
    scan_all_skills,
    update_remote_skills,
)
from .models import (
    DEFAULT_CACHE_DIR,
    DEFAULT_CONFIG_FILE,
    DEFAULT_SKILLS_DIR,
    KNOWN_AGENTS,
    UNIVERSAL_AGENTS,
    SkillItem,
    is_universal_agent,
    normalize_agent_name,
    parse_repo_source,
)
from .ui import prompt_grouped_multi_select, prompt_multi_select
from .updater import (
    check_self_update,
    download_and_install_binary,
    get_current_executable_path,
)

# ANSI Colors
CYAN = "\033[96m"
GREEN = "\033[92m"
YELLOW = "\033[93m"
RED = "\033[91m"
BOLD = "\033[1m"
DIM = "\033[2m"
RESET = "\033[0m"


def print_banner() -> None:
    pass


def cmd_ls(args: argparse.Namespace) -> int:
    """List installed and configured skills."""
    config_path = Path(args.config) if args.config else DEFAULT_CONFIG_FILE
    skills_dir = Path(args.skills_dir) if args.skills_dir else DEFAULT_SKILLS_DIR
    config_data = load_config(config_path)
    skills = scan_all_skills(config_data, skills_dir)

    if args.agent:
        filter_agent = normalize_agent_name(args.agent)
        if is_universal_agent(filter_agent) or filter_agent in ("agents", "all", "universal"):
            skills = [s for s in skills if s.is_installed]
        else:
            skills = [s for s in skills if filter_agent in [normalize_agent_name(a) for a in s.linked_agents]]

    if getattr(args, "source", None):
        pattern = args.source.strip().lower()
        skills = [
            s for s in skills
            if pattern in s.source.lower() or pattern in s.source_type.lower()
        ]

    if args.json:
        # Output JSON compatible with skills CLI
        out_list = []
        for s in skills:
            out_list.append({
                "name": s.name,
                "path": str(s.installed_path) if s.installed_path else str(skills_dir / s.name),
                "scope": "global",
                "agents": s.linked_agents,
                "source": s.source,
                "sourceType": s.source_type,
                "subpath": s.subpath,
                "installed": s.is_installed,
                "valid": s.is_valid_skill,
            })
        print(json.dumps(out_list, indent=2, ensure_ascii=False))
        return 0

    if not skills:
        if getattr(args, "source", None) or getattr(args, "agent", None):
            print(f"{YELLOW}No skills found matching the specified filters.{RESET}")
        else:
            print(f"{YELLOW}No global skills installed or configured.{RESET}")
        return 0

    print(f"\n{BOLD}{CYAN}Global Skills ({len(skills)} total):{RESET}\n")
    print(f"{BOLD}{'NAME':<32} {'SOURCE':<38} {'AGENTS':<12} {'STATUS'}{RESET}")
    print("─" * 94)

    for s in skills:
        status_badges = []
        if s.is_installed:
            if s.is_valid_skill:
                status_badges.append(f"{GREEN}Installed{RESET}")
            else:
                status_badges.append(f"{RED}Invalid (No SKILL.md){RESET}")
        else:
            status_badges.append(f"{YELLOW}Missing{RESET}")

        if s.source_type.startswith("local_"):
            raw_src = f"[local] {s.source}"
            if len(raw_src) > 38:
                raw_src = raw_src[:35] + "..."
            source_display = f"{DIM}[local]{RESET} {raw_src[8:]:<30}"
        elif s.source_type == "symlink":
            raw_src = f"[symlink] {s.source}"
            if len(raw_src) > 38:
                raw_src = raw_src[:35] + "..."
            source_display = f"{DIM}[symlink]{RESET} {raw_src[10:]:<28}"
        elif s.source_type == "untracked":
            source_display = f"{YELLOW}{'[untracked]':<38}{RESET}"
        else:
            raw_src = s.source
            if len(raw_src) > 38:
                raw_src = raw_src[:35] + "..."
            source_display = f"{raw_src:<38}"

        # All skills are in ~/.agents/skills by default; list claude (and custom non-default agents)
        target_list = []
        if "claude-code" in s.linked_agents or "claude" in s.linked_agents:
            target_list.append("claude")

        other_agents = [
            a.replace("-code", "")
            for a in s.linked_agents
            if a not in ("claude-code", "claude", "agents")
        ]
        for oa in other_agents:
            if oa not in target_list:
                target_list.append(oa)

        raw_targets = ", ".join(target_list) if target_list else "-"
        if target_list:
            agents_display = f"{raw_targets:<12}"
        else:
            agents_display = f"{DIM}{raw_targets:<12}{RESET}"

        name_display = f"{BOLD}{s.name:<32}{RESET}"
        status_str = " ".join(status_badges)
        print(f"{name_display} {source_display} {agents_display} {status_str}")

    print("─" * 94 + "\n")
    return 0


def cmd_add(args: argparse.Namespace) -> int:
    """Add a skill from GitHub repository, local symlink, or CLI command."""
    config_path = Path(args.config) if args.config else DEFAULT_CONFIG_FILE
    skills_dir = Path(args.skills_dir) if args.skills_dir else DEFAULT_SKILLS_DIR
    cache_dir = Path(args.cache_dir) if args.cache_dir else DEFAULT_CACHE_DIR
    config_data = load_config(config_path)

    # 1. Local Symlink Mode
    if args.symlink:
        source_path = Path(args.symlink).expanduser().resolve()
        if not source_path.exists():
            print(f"{RED}Error: Local source path does not exist: {source_path}{RESET}")
            return 1

        skill_name = args.skill[0] if args.skill else source_path.name
        print(f"{CYAN}🔗 Linking local skill: {BOLD}{skill_name}{RESET} -> {source_path}")

        skills_dir.mkdir(parents=True, exist_ok=True)
        dest_link = skills_dir / skill_name
        if dest_link.is_symlink() or dest_link.is_file():
            dest_link.unlink()
        elif dest_link.is_dir():
            shutil.rmtree(dest_link)
        os.symlink(source_path, dest_link)

        # Dispatch symlinks to agents
        target_agents = args.agent or get_target_agents_for_skill(skill_name, "local", config_data)
        for agent in target_agents:
            ensure_agent_symlink(skill_name, agent, skills_dir)

        # Update config
        add_local_symlink_entry(config_data, skill_name, str(source_path), args.description)
        save_config(config_data, config_path)
        print(f"{GREEN}✔ Successfully added local skill {skill_name} and updated {config_path.name}{RESET}")
        return 0

    # 2. Command Skill Mode
    if args.command:
        if not args.skill:
            print(f"{RED}Error: --skill <name> is required when adding a command skill.{RESET}")
            return 1
        skill_name = args.skill[0]
        print(f"{CYAN}⚙️  Configuring command skill: {BOLD}{skill_name}{RESET}")
        print(f"   Command: {args.command}")

        # Run command
        print(f"   Executing installer command...")
        res = run_cmd(args.command, check=False)
        if res.returncode != 0:
            print(f"{YELLOW}Warning: Install command returned non-zero exit code {res.returncode}{RESET}")

        # Dispatch symlinks to agents
        target_agents = args.agent or get_target_agents_for_skill(skill_name, "local", config_data)
        for agent in target_agents:
            ensure_agent_symlink(skill_name, agent, skills_dir)

        add_local_command_entry(config_data, skill_name, args.command, args.check, args.description)
        save_config(config_data, config_path)
        print(f"{GREEN}✔ Successfully registered command skill {skill_name} and updated {config_path.name}{RESET}")
        return 0

    # 3. Remote Git Repository Mode
    if not args.source:
        print(f"{RED}Error: Source repository or --symlink/--command required.{RESET}")
        return 1

    parsed = parse_repo_source(args.source)
    source_key = parsed.source_key
    clone_url = args.url or parsed.url
    branch = args.branch or parsed.branch
    subpath_override = args.path or parsed.subpath
    repo_type = parsed.repo_type

    print(f"{CYAN}📦 Fetching repository: {BOLD}{source_key}{RESET}...")
    try:
        repo_dir = ensure_git_repo(source_key, url=clone_url, branch=branch, force_update=True, cache_dir=cache_dir)
    except Exception as e:
        print(f"{RED}Error cloning repository: {e}{RESET}")
        return 1

    # Discover skills in repo
    discovered = discover_skills_in_repo(repo_dir)
    if not discovered:
        print(f"{RED}Error: No SKILL.md found in {source_key}{RESET}")
        return 1

    # Determine which skills to install
    skills_to_install = {}
    if args.all:
        skills_to_install = discovered
    elif subpath_override and not args.skill:
        # Match by direct subpath from web tree URL or --path
        matched_by_subpath = {k: v for k, v in discovered.items() if v == subpath_override or v.rstrip("/") == subpath_override.rstrip("/")}
        if matched_by_subpath:
            skills_to_install = matched_by_subpath
        else:
            subpath_dir = repo_dir / subpath_override
            if subpath_dir.exists() and (subpath_dir / "SKILL.md").exists():
                skill_name = subpath_dir.name
                skills_to_install = {skill_name: subpath_override}
            else:
                print(f"{RED}Error: Specified path '{subpath_override}' does not contain SKILL.md{RESET}")
                return 1
    elif args.skill:
        for sk in args.skill:
            if sk in discovered:
                skills_to_install[sk] = discovered[sk]
            elif subpath_override:
                skills_to_install[sk] = subpath_override
            else:
                # Fuzzy match
                matched = [k for k in discovered.keys() if k.lower() == sk.lower()]
                if matched:
                    skills_to_install[matched[0]] = discovered[matched[0]]
                else:
                    print(f"{YELLOW}Warning: Skill '{sk}' not found in discovered list ({', '.join(discovered.keys())}){RESET}")
    else:
        # If single skill in repo, install it
        if len(discovered) == 1:
            skills_to_install = discovered
        elif sys.stdin.isatty():
            # Interactive multi-selection prompt
            items_list = []
            for sk_name in sorted(discovered.keys()):
                is_inst = (skills_dir / sk_name).exists()
                items_list.append((sk_name, is_inst, None))

            chosen = prompt_multi_select(
                f"📦 Select skills to install from {source_key}:",
                items_list
            )

            if chosen is None:
                print(f"{YELLOW}Operation cancelled.{RESET}")
                return 0

            if not chosen:
                print(f"{YELLOW}No skills selected. Aborted.{RESET}")
                return 0

            skills_to_install = {k: discovered[k] for k in chosen}
        else:
            print(f"{YELLOW}Repository contains multiple skills:{RESET} {', '.join(discovered.keys())}")
            print(f"Please specify --skill <name> or --all")
            return 1

    if not skills_to_install:
        print(f"{RED}No matching skills to install.{RESET}")
        return 1

    skills_dir.mkdir(parents=True, exist_ok=True)
    installed_names = []

    # Decide whether url needs to be explicitly stored in skills.json
    stored_url = clone_url if (args.url or repo_type != "github" or not clone_url.startswith("https://github.com/")) else None

    for name, subpath in skills_to_install.items():
        src_path = repo_dir / subpath
        target_path = skills_dir / name
        print(f"  📥 Installing {BOLD}{name}{RESET} (from {subpath})...")
        copy_skill_folder(src_path, target_path)

        # Dispatch symlinks
        target_agents = args.agent or get_target_agents_for_skill(name, source_key, config_data)
        for agent in target_agents:
            ensure_agent_symlink(name, agent, skills_dir)

        # Add to config
        add_remote_skill_entry(config_data, source_key, name, subpath, repo_type=repo_type, url=stored_url)
        installed_names.append(name)

    save_config(config_data, config_path)
    print(f"\n{GREEN}✔ Installed {len(installed_names)} skill(s) [{', '.join(installed_names)}] and updated {config_path.name}{RESET}\n")
    return 0


def cmd_rm(args: argparse.Namespace) -> int:
    """Remove a skill globally or from specific agents."""
    config_path = Path(args.config) if args.config else DEFAULT_CONFIG_FILE
    skills_dir = Path(args.skills_dir) if args.skills_dir else DEFAULT_SKILLS_DIR
    config_data = load_config(config_path)

    skills_to_remove = args.skills or []

    if not skills_to_remove:
        if sys.stdin.isatty():
            # Interactive multi-select for removal grouped by source
            all_skills = scan_all_skills(config_data, skills_dir)
            if not all_skills:
                print(f"{YELLOW}No global skills installed or configured to remove.{RESET}")
                return 0

            # Group skills by source
            from collections import OrderedDict
            grouped_dict: Dict[str, List[Tuple[str, bool, Optional[str]]]] = OrderedDict()
            for s in all_skills:
                source_key = s.source
                if source_key not in grouped_dict:
                    grouped_dict[source_key] = []
                grouped_dict[source_key].append((s.name, s.is_installed, None))

            # Safety: all unchecked by default
            chosen = prompt_grouped_multi_select(
                "🗑 Select skills to remove:",
                grouped_dict,
                initial_checked=set()
            )

            if chosen is None:
                print(f"{YELLOW}Operation cancelled.{RESET}")
                return 0

            if not chosen:
                print(f"{YELLOW}No skills selected. Aborted.{RESET}")
                return 0

            skills_to_remove = chosen
        else:
            print(f"{RED}Error: Skill name(s) required.{RESET}")
            return 1

    for skill_name in skills_to_remove:
        print(f"\n{CYAN}🗑  Removing skill: {BOLD}{skill_name}{RESET}...")

        # 1. If removing from specific agent only
        if args.agent:
            for agent in args.agent:
                norm = normalize_agent_name(agent)
                agent_dir = KNOWN_AGENTS.get(norm)
                if agent_dir:
                    link_path = agent_dir / skill_name
                    if link_path.is_symlink() or link_path.exists():
                        if link_path.is_symlink() or link_path.is_file():
                            link_path.unlink()
                        elif link_path.is_dir():
                            shutil.rmtree(link_path)
                        print(f"  {GREEN}✔{RESET} Removed link from {norm}")
            continue

        # 2. Full removal
        # Remove agent symlinks
        unlinked = remove_agent_symlinks(skill_name)
        if unlinked:
            print(f"  {GREEN}✔{RESET} Unlinked from: {', '.join(unlinked)}")

        # Remove master directory in ~/.agents/skills/
        master_path = skills_dir / skill_name
        if master_path.is_symlink() or master_path.is_file():
            master_path.unlink()
            print(f"  {GREEN}✔{RESET} Removed master symlink: {master_path}")
        elif master_path.is_dir():
            shutil.rmtree(master_path)
            print(f"  {GREEN}✔{RESET} Removed master directory: {master_path}")

        # Update config
        if remove_skill_entry(config_data, skill_name):
            print(f"  {GREEN}✔{RESET} Removed from configuration")

    save_config(config_data, config_path)
    print(f"\n{GREEN}✨ Skill removal complete!{RESET}\n")
    return 0


def cmd_sync(args: argparse.Namespace) -> int:
    """Sync and restore all global skills from skills.json."""
    config_path = Path(args.config) if args.config else DEFAULT_CONFIG_FILE
    skills_dir = Path(args.skills_dir) if args.skills_dir else DEFAULT_SKILLS_DIR
    cache_dir = Path(args.cache_dir) if args.cache_dir else DEFAULT_CACHE_DIR
    config_data = load_config(config_path)

    print(f"\n{BOLD}{CYAN}🚀 Syncing global skills from {config_path}...{RESET}\n")

    skills_dir.mkdir(parents=True, exist_ok=True)
    all_configured_skills = set()

    # 1. Prune step (if requested)
    if args.prune or args.prune_only:
        configured_remote = set()
        for repo_info in config_data.get("remote", {}).values():
            configured_remote.update(repo_info.get("skills", {}).keys())
        configured_local = set(config_data.get("local", {}).keys())
        valid_set = configured_remote | configured_local

        orphans = []
        if skills_dir.exists():
            for p in skills_dir.iterdir():
                if p.name.startswith("."):
                    continue
                if p.name not in valid_set:
                    orphans.append(p.name)

        if orphans:
            print(f"{YELLOW}Found {len(orphans)} untracked skill(s): {', '.join(orphans)}{RESET}")
            if args.dry_run:
                print(f"  [Dry-run] Would remove untracked skills: {', '.join(orphans)}")
            else:
                for orp in orphans:
                    remove_agent_symlinks(orp)
                    p = skills_dir / orp
                    if p.is_symlink() or p.is_file():
                        p.unlink()
                    elif p.is_dir():
                        shutil.rmtree(p)
                    print(f"  {GREEN}✔{RESET} Pruned {orp}")
        else:
            print(f"{GREEN}No untracked skills to prune.{RESET}")

        if args.prune_only:
            print(f"\n{GREEN}✨ Prune complete!{RESET}\n")
            return 0

    # 2. Sync Remote Skills
    remote_repos = config_data.get("remote", {})
    for source, repo_info in remote_repos.items():
        skills = repo_info.get("skills", {})
        all_configured_skills.update(skills.keys())

        # Check if skills need updating or installing
        missing_skills = {}
        for name, subpath in skills.items():
            target_path = skills_dir / name
            if args.force or not target_path.exists():
                missing_skills[name] = subpath

        if not missing_skills and not args.force:
            # Ensure agent links still exist
            for name in skills.keys():
                target_agents = get_target_agents_for_skill(name, source, config_data)
                for agent in target_agents:
                    ensure_agent_symlink(name, agent, skills_dir)
            continue

        print(f"📦 Syncing repo: {BOLD}{source}{RESET} ({len(skills)} skills)...")
        if args.dry_run:
            print(f"  [Dry-run] Would sync {', '.join(missing_skills.keys())} from {source}")
            continue

        try:
            repo_dir = ensure_git_repo(
                source,
                url=repo_info.get("url"),
                branch=repo_info.get("branch"),
                force_update=args.force,
                cache_dir=cache_dir
            )
        except Exception as e:
            print(f"  {RED}✖ Failed to fetch {source}: {e}{RESET}")
            continue

        for name, subpath in skills.items():
            src_path = repo_dir / subpath
            target_path = skills_dir / name
            if not src_path.exists():
                print(f"  {RED}✖ Skill path missing in repo: {subpath} for {name}{RESET}")
                continue

            if args.force or not target_path.exists():
                copy_skill_folder(src_path, target_path)
                print(f"  {GREEN}✔{RESET} Restored {BOLD}{name}{RESET}")

            # Dispatch links to agents
            target_agents = get_target_agents_for_skill(name, source, config_data)
            for agent in target_agents:
                ensure_agent_symlink(name, agent, skills_dir)

    # 3. Sync Local Skills
    local_skills = config_data.get("local", {})
    for name, local_info in local_skills.items():
        all_configured_skills.add(name)
        stype = local_info.get("type")

        if stype == "symlink":
            src = Path(local_info.get("source", "")).expanduser()
            target_link = skills_dir / name
            if not src.exists():
                print(f"  {YELLOW}⚠️  Local symlink source missing: {src} (skill: {name}){RESET}")
                continue

            if args.dry_run:
                print(f"  [Dry-run] Would symlink {target_link} -> {src}")
            else:
                if target_link.is_symlink() or target_link.is_file():
                    target_link.unlink()
                elif target_link.is_dir():
                    shutil.rmtree(target_link)
                os.symlink(src, target_link)
                print(f"  {GREEN}✔{RESET} Linked local skill {BOLD}{name}{RESET} -> {src}")

                # Dispatch links
                target_agents = get_target_agents_for_skill(name, "local", config_data)
                for agent in target_agents:
                    ensure_agent_symlink(name, agent, skills_dir)

        elif stype == "command":
            cmd = local_info.get("command")
            check = local_info.get("check")

            if check:
                res_check = run_cmd(check, check=False)
                if res_check.returncode != 0:
                    print(f"  {DIM}Command check '{check}' failed, skipping {name}{RESET}")
                    continue

            if args.dry_run:
                print(f"  [Dry-run] Would execute: {cmd}")
            else:
                print(f"  ⚙️  Running installer for {BOLD}{name}{RESET}...")
                run_cmd(cmd, check=False)

                # Dispatch links
                target_agents = get_target_agents_for_skill(name, "local", config_data)
                for agent in target_agents:
                    ensure_agent_symlink(name, agent, skills_dir)

    # 4. Post-hooks Execution
    post_hooks = config_data.get("postHooks", [])
    if post_hooks:
        print(f"\n{CYAN}⚡ Running post-sync hooks...{RESET}")
        hook_results = execute_post_hooks(post_hooks, dry_run=args.dry_run)
        for name, ok, msg in hook_results:
            badge = f"{GREEN}✔{RESET}" if ok else f"{RED}✖{RESET}"
            print(f"  {badge} [{name}] {msg}")

    print(f"\n{BOLD}{GREEN}✨ Global skills sync complete! ({len(all_configured_skills)} skills configured){RESET}\n")
    return 0


def cmd_doctor(args: argparse.Namespace) -> int:
    """Diagnose health of global skills setup."""
    config_path = Path(args.config) if args.config else DEFAULT_CONFIG_FILE
    skills_dir = Path(args.skills_dir) if args.skills_dir else DEFAULT_SKILLS_DIR
    config_data = load_config(config_path)

    print(f"\n{BOLD}{CYAN}🩺 Diagnosing Global Skills Health...{RESET}\n")
    issues_found = 0

    # 1. Check master skills dir
    if not skills_dir.exists():
        print(f"{RED}✖ Master skills directory does not exist: {skills_dir}{RESET}")
        issues_found += 1
    else:
        print(f"{GREEN}✔{RESET} Master skills directory: {skills_dir}")

    # 2. Check broken symlinks in master and agent directories
    print(f"\n{BOLD}Checking Agent Directories & Symlinks:{RESET}")
    for agent_name, agent_dir in KNOWN_AGENTS.items():
        if not agent_dir.exists():
            continue

        broken_in_agent = []
        physical_in_agent = []
        for p in agent_dir.iterdir():
            if p.is_symlink():
                if not p.exists():
                    broken_in_agent.append(p.name)
            elif p.is_dir() and not p.name.startswith("."):
                physical_in_agent.append(p.name)

        if broken_in_agent:
            print(f"  {RED}✖ [{agent_name}] Broken symlinks:{RESET} {', '.join(broken_in_agent)}")
            issues_found += len(broken_in_agent)
            if args.fix:
                for b in broken_in_agent:
                    (agent_dir / b).unlink()
                    print(f"    {GREEN}✔ Fixed: Removed broken symlink {b}{RESET}")
        else:
            print(f"  {GREEN}✔{RESET} [{agent_name}] Symlinks healthy ({agent_dir})")

        if physical_in_agent and agent_name == "claude-code":
            print(f"  {YELLOW}⚠️  [{agent_name}] Physical directories found instead of symlinks:{RESET} {', '.join(physical_in_agent)}")
            issues_found += len(physical_in_agent)
            if args.fix and "agentsview-finding-history" in physical_in_agent:
                # Auto fix agentsview symlink
                shutil.rmtree(agent_dir / "agentsview-finding-history")
                ensure_agent_symlink("agentsview-finding-history", "claude-code", skills_dir)
                print(f"    {GREEN}✔ Fixed: Converted agentsview-finding-history to symlink{RESET}")

    # 3. Check configured vs installed
    skills = scan_all_skills(config_data, skills_dir)
    missing = [s.name for s in skills if not s.is_installed]
    untracked = [s.name for s in skills if s.source_type == "untracked"]
    invalid = [s.name for s in skills if s.is_installed and not s.is_valid_skill]

    if missing:
        print(f"\n{YELLOW}⚠️  Configured but missing skills:{RESET} {', '.join(missing)}")
        issues_found += len(missing)
    if untracked:
        print(f"\n{YELLOW}⚠️  Untracked skills in ~/.agents/skills/:{RESET} {', '.join(untracked)}")
    if invalid:
        print(f"\n{RED}✖ Installed folders missing SKILL.md:{RESET} {', '.join(invalid)}")
        issues_found += len(invalid)

    print("\n" + "─" * 60)
    if issues_found == 0:
        print(f"{BOLD}{GREEN}🎉 Everything is in top condition! No issues detected.{RESET}\n")
        return 0
    else:
        print(f"{BOLD}{YELLOW}Found {issues_found} issue(s). Run with --fix or 'skills sync' to repair.{RESET}\n")
        return 1


def cmd_outdated(args: argparse.Namespace) -> int:
    """Check for new versions in remote skill repositories."""
    config_path = Path(args.config) if args.config else DEFAULT_CONFIG_FILE
    cache_dir = Path(args.cache_dir) if args.cache_dir else DEFAULT_CACHE_DIR
    config_data = load_config(config_path)

    remote_repos = config_data.get("remote", {})
    if not remote_repos:
        if args.json:
            print(json.dumps([], indent=2))
        else:
            print(f"{YELLOW}No remote repositories configured in {config_path.name}.{RESET}")
        return 0

    if not args.json:
        print(f"\n{BOLD}{CYAN}🔍 Checking remote repositories for updates...{RESET}\n")

    results = check_all_remote_skills_outdated(config_data, cache_dir=cache_dir)

    if args.json:
        out_json = []
        for r in results:
            out_json.append({
                "source": r["source"],
                "url": r["url"],
                "branch": r["branch"],
                "status": r["status"],
                "localSha": r["local_sha"],
                "remoteSha": r["remote_sha"],
                "skills": r["skills"],
            })
        print(json.dumps(out_json, indent=2, ensure_ascii=False))
        return 0

    print(f"{BOLD}{'REPOSITORY / SKILL':<40} {'CURRENT':<12} {'LATEST':<12} {'STATUS'}{RESET}")
    print("─" * 80)

    outdated_count = 0
    up_to_date_count = 0
    error_count = 0

    for r in results:
        status = r["status"]
        local_raw = r["local_sha"][:7] if r["local_sha"] else "none"
        remote_raw = r["remote_sha"][:7] if r["remote_sha"] else "none"

        local_padded = f"{local_raw:<12}"
        remote_padded = f"{remote_raw:<12}"

        local_display = f"{DIM}{local_padded}{RESET}" if not r["local_sha"] else local_padded
        remote_display = f"{DIM}{remote_padded}{RESET}" if not r["remote_sha"] else remote_padded

        if status == "update_available":
            outdated_count += 1
            status_display = f"{YELLOW}{BOLD}Update available{RESET}"
        elif status == "up_to_date":
            up_to_date_count += 1
            status_display = f"{GREEN}Up to date{RESET}"
        elif status == "not_installed":
            outdated_count += 1
            status_display = f"{CYAN}Not installed (New){RESET}"
        else:
            error_count += 1
            status_display = f"{RED}Check failed{RESET}"

        repo_name = r["source"]
        print(f"{BOLD}{repo_name:<40}{RESET} {local_display} {remote_display} {status_display}")

        # List individual skills under this repository
        skills = r["skills"]
        for i, sk in enumerate(skills):
            is_last = (i == len(skills) - 1)
            prefix = "  └─ " if is_last else "  ├─ "
            print(f"{DIM}{prefix}{sk}{RESET}")

    print("─" * 80)
    summary_parts = []
    if outdated_count:
        summary_parts.append(f"{YELLOW}{BOLD}{outdated_count} update(s) available{RESET}")
    if up_to_date_count:
        summary_parts.append(f"{GREEN}{up_to_date_count} up to date{RESET}")
    if error_count:
        summary_parts.append(f"{RED}{error_count} error(s){RESET}")

    print(f"Summary: {', '.join(summary_parts)}")
    if outdated_count > 0:
        print(f"\n💡 Run '{BOLD}skills update{RESET}' to upgrade outdated skills.\n")
    else:
        print(f"\n{GREEN}✨ All skills are up to date!{RESET}\n")

    return 0


def cmd_update(args: argparse.Namespace) -> int:
    """Update remote repositories and sync skills."""
    config_path = Path(args.config) if args.config else DEFAULT_CONFIG_FILE
    skills_dir = Path(args.skills_dir) if args.skills_dir else DEFAULT_SKILLS_DIR
    cache_dir = Path(args.cache_dir) if args.cache_dir else DEFAULT_CACHE_DIR
    config_data = load_config(config_path)

    remote_repos = config_data.get("remote", {})
    if not remote_repos:
        if args.json:
            print(json.dumps({"updated": [], "skipped": [], "errors": []}, indent=2))
        else:
            print(f"{YELLOW}No remote repositories configured in {config_path.name}.{RESET}")
        return 0

    targets = args.targets if hasattr(args, "targets") and args.targets else None

    if not args.json:
        if args.dry_run:
            print(f"\n{BOLD}{CYAN}🔍 [Dry-Run] Checking and previewing skills update...{RESET}\n", flush=True)
        else:
            print(f"\n{BOLD}{CYAN}🚀 Updating skills from remote repositories...{RESET}\n", flush=True)

    def on_progress(event: str, data: Dict[str, Any]) -> None:
        if args.json:
            return
        if event == "check_start":
            print(f"  🔍 Checking {data['total']} remote repositories in parallel...", flush=True)
        elif event == "check_done":
            outdated = data.get("outdated", 0)
            up_to_date = data.get("up_to_date", 0)
            if outdated == 0:
                print(f"  {GREEN}✔ All {up_to_date} repositories are already up to date.{RESET}\n", flush=True)
            else:
                print(f"  {CYAN}ℹ {outdated} repository update(s) needed, {up_to_date} already up to date.{RESET}\n", flush=True)
        elif event == "update_start":
            idx = data.get("index", 1)
            total = data.get("total", 1)
            source = data["source"]
            skills_list = ", ".join(data.get("skills", []))
            if data.get("dry_run"):
                print(f"  [{idx}/{total}] {CYAN}ℹ [Dry-run]{RESET} Would update {BOLD}{source}{RESET} ({skills_list})", flush=True)
            else:
                print(f"  [{idx}/{total}] 📦 Updating {BOLD}{source}{RESET} ({skills_list})...", flush=True)
        elif event == "skill_restored":
            print(f"      📥 Restored {BOLD}{data['skill']}{RESET}", flush=True)
        elif event == "repo_done":
            sha_str = f" ({data['new_sha'][:7]})" if data.get("new_sha") else ""
            print(f"      {GREEN}✔{RESET} Successfully updated {BOLD}{data['source']}{RESET}{sha_str}", flush=True)
        elif event == "repo_error":
            print(f"      {RED}✖ Error updating {data['source']}: {data['error']}{RESET}", flush=True)
        elif event == "hooks_start":
            print(f"\n{CYAN}⚡ Running post-sync hooks...{RESET}", flush=True)
        elif event == "hook_done":
            badge = f"{GREEN}✔{RESET}" if data.get("ok") else f"{RED}✖{RESET}"
            print(f"  {badge} [{data.get('name')}] {data.get('msg')}", flush=True)

    result = update_remote_skills(
        config_data,
        targets=targets,
        force=args.force,
        dry_run=args.dry_run,
        skills_dir=skills_dir,
        cache_dir=cache_dir,
        on_progress=on_progress,
    )

    if args.json:
        print(json.dumps(result, indent=2, ensure_ascii=False))
        return 0 if not result["errors"] else 1

    total_updated = len(result["updated_skills"])
    total_skipped = len(result["skipped_repos"])
    if total_updated > 0:
        skip_msg = f" ({total_skipped} repository/repositories were already up to date)" if total_skipped > 0 else ""
        if args.dry_run:
            print(f"\n{BOLD}{GREEN}✨ Dry-run complete: {total_updated} skill(s) would be updated.{RESET}{skip_msg}\n", flush=True)
        else:
            print(f"\n{BOLD}{GREEN}✨ Successfully updated {total_updated} skill(s)!{RESET}{skip_msg}\n", flush=True)
    else:
        if not result["errors"]:
            print(f"\n{BOLD}{GREEN}✨ Everything is already up to date.{RESET}\n", flush=True)
        else:
            print(f"\n{BOLD}{YELLOW}Update completed with errors.{RESET}\n", flush=True)

    return 0 if not result["errors"] else 1


def cmd_self_update(args: argparse.Namespace) -> int:
    """Update the skills CLI binary to the latest release."""
    target_version = args.version if hasattr(args, "version") and args.version else None

    if not args.json:
        print(f"\n{BOLD}{CYAN}🔍 Checking for skills CLI updates from GitHub Releases...{RESET}\n")

    try:
        info = check_self_update(target_version=target_version)
    except Exception as e:
        if args.json:
            print(json.dumps({"status": "error", "error": str(e)}, indent=2))
        else:
            print(f"{RED}✖ Failed to check for updates: {e}{RESET}\n")
        return 1

    curr_v = info["current_version"]
    latest_v = info["latest_version"]
    latest_tag = info["latest_tag"]
    update_avail = info["update_available"]
    asset_url = info["asset_url"]

    if args.json:
        print(json.dumps(info, indent=2, ensure_ascii=False))
        if args.check:
            return 0
        if not update_avail and not args.force:
            return 0

    if args.check:
        print(f"Current version: {BOLD}v{curr_v}{RESET}")
        print(f"Latest version:  {BOLD}{latest_tag}{RESET}")
        if update_avail:
            print(f"\n{YELLOW}{BOLD}✨ Update available: v{curr_v} -> {latest_tag}{RESET}")
            print(f"Run '{BOLD}skills self-update{RESET}' to upgrade.\n")
        else:
            print(f"\n{GREEN}✔ skills is already on the latest version ({latest_tag}).{RESET}\n")
        return 0

    if not update_avail and not args.force:
        print(f"Current version: {BOLD}v{curr_v}{RESET}")
        print(f"Latest version:  {BOLD}{latest_tag}{RESET}")
        print(f"\n{GREEN}✔ skills is already on the latest version ({latest_tag}).{RESET}\n")
        return 0

    if not asset_url:
        print(f"{RED}✖ No standalone binary asset found in release {latest_tag}.{RESET}\n")
        return 1

    target_path = get_current_executable_path()
    print(f"Upgrading skills CLI:")
    print(f"  Version:   {YELLOW}v{curr_v}{RESET} -> {GREEN}{latest_tag}{RESET}")
    print(f"  Target:    {target_path}")
    print(f"  Download:  {asset_url}")

    if args.dry_run:
        print(f"\n{CYAN}ℹ [Dry-run]{RESET} Would download and replace {target_path} with {latest_tag}\n")
        return 0

    print(f"\n📥 Downloading and installing {latest_tag}...")
    try:
        installed_dest = download_and_install_binary(asset_url, target_path=target_path)
        print(f"{GREEN}✔ Successfully updated skills to {BOLD}{latest_tag}{RESET}! ({installed_dest})\n")
        return 0
    except PermissionError:
        print(f"{RED}✖ Permission denied writing to {target_path}. Please run with appropriate permissions (e.g. sudo).{RESET}\n")
        return 1
    except Exception as e:
        print(f"{RED}✖ Update failed: {e}{RESET}\n")
        return 1


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="skills",
        description="Global skills manager for AI coding agents."
    )
    parser.add_argument("-v", "--version", action="version", version=f"%(prog)s {__version__}")
    parser.add_argument("--config", help=f"Path to skills.json (default: {DEFAULT_CONFIG_FILE})")
    parser.add_argument("--skills-dir", help=f"Path to ~/.agents/skills (default: {DEFAULT_SKILLS_DIR})")
    parser.add_argument("--cache-dir", help=f"Path to cache directory (default: {DEFAULT_CACHE_DIR})")

    subparsers = parser.add_subparsers(dest="subcommand", help="Available subcommands")

    # version
    subparsers.add_parser("version", help="Print skills manager version")

    # ls
    ls_p = subparsers.add_parser("ls", aliases=["list"], help="List installed and configured skills")
    ls_p.add_argument("--json", action="store_true", help="Output machine-readable JSON")
    ls_p.add_argument("-a", "--agent", help="Filter by target agent name")
    ls_p.add_argument("-s", "--source", help="Filter skills by source repository or type (e.g. akunzai, local)")

    # add
    add_p = subparsers.add_parser("add", help="Add a new skill (remote git, local symlink, or CLI command)")
    add_p.add_argument("source", nargs="?", help="GitHub repo (e.g. akunzai/agent-skills)")
    add_p.add_argument("-s", "--skill", nargs="*", help="Specific skill name(s)")
    add_p.add_argument("--all", action="store_true", help="Install all skills found in the repository")
    add_p.add_argument("--path", help="Relative path within repo")
    add_p.add_argument("--url", help="Custom Git clone URL")
    add_p.add_argument("--branch", help="Git branch or tag")
    add_p.add_argument("-a", "--agent", nargs="*", help="Target agents to link skill to")
    add_p.add_argument("--symlink", help="Path to local skill directory for symlink install")
    add_p.add_argument("--command", help="Command to install the skill")
    add_p.add_argument("--check", help="Command to check before installing command skill")
    add_p.add_argument("--description", help="Description of the skill")
    add_p.add_argument("-y", "--yes", action="store_true", help="Skip confirmation prompts")

    # rm
    rm_p = subparsers.add_parser("rm", aliases=["remove"], help="Remove one or more skills")
    rm_p.add_argument("skills", nargs="*", help="Skill names to remove (interactive selection if omitted)")
    rm_p.add_argument("-a", "--agent", nargs="*", help="Remove only from specific agents")
    rm_p.add_argument("-y", "--yes", action="store_true", help="Skip confirmation prompts")

    # sync / restore
    sync_p = subparsers.add_parser("sync", aliases=["restore"], help="Sync/restore global skills declared in skills.json")
    sync_p.add_argument("--force", action="store_true", help="Force re-clone and re-link all skills")
    sync_p.add_argument("--prune", action="store_true", help="Remove untracked skills and broken symlinks")
    sync_p.add_argument("--prune-only", action="store_true", help="Remove untracked skills without restoring")
    sync_p.add_argument("--dry-run", action="store_true", help="Preview actions without making changes")

    # outdated / check
    outdated_p = subparsers.add_parser("outdated", aliases=["check", "check-update"], help="Check for newer versions of remote skills")
    outdated_p.add_argument("--json", action="store_true", help="Output machine-readable JSON")

    # update / upgrade
    update_p = subparsers.add_parser("update", aliases=["upgrade"], help="Update remote skills to latest versions")
    update_p.add_argument("targets", nargs="*", help="Optional repository name or skill name(s) to update")
    update_p.add_argument("--force", action="store_true", help="Force re-fetch and overwrite even if commit SHA is unchanged")
    update_p.add_argument("--dry-run", action="store_true", help="Preview updates without making changes")
    update_p.add_argument("--json", action="store_true", help="Output machine-readable JSON")

    # self-update / self-upgrade
    self_up_p = subparsers.add_parser("self-update", aliases=["self-upgrade"], help="Update skills CLI itself to latest release")
    self_up_p.add_argument("--check", action="store_true", help="Only check for updates without installing")
    self_up_p.add_argument("--version", help="Specify target version/tag to install (e.g. v0.1.1)")
    self_up_p.add_argument("--force", action="store_true", help="Force re-download even if already up to date")
    self_up_p.add_argument("--dry-run", action="store_true", help="Preview update without downloading")
    self_up_p.add_argument("--json", action="store_true", help="Output machine-readable JSON")

    # doctor
    doc_p = subparsers.add_parser("doctor", help="Diagnose and repair skills health")
    doc_p.add_argument("--fix", action="store_true", help="Automatically repair detected issues")

    args = parser.parse_args()

    if args.subcommand == "version":
        print(f"skills {__version__}")
        sys.exit(0)
    elif args.subcommand in ("ls", "list"):
        sys.exit(cmd_ls(args))
    elif args.subcommand == "add":
        sys.exit(cmd_add(args))
    elif args.subcommand in ("rm", "remove"):
        sys.exit(cmd_rm(args))
    elif args.subcommand in ("sync", "restore"):
        sys.exit(cmd_sync(args))
    elif args.subcommand in ("outdated", "check", "check-update"):
        sys.exit(cmd_outdated(args))
    elif args.subcommand in ("update", "upgrade"):
        sys.exit(cmd_update(args))
    elif args.subcommand in ("self-update", "self-upgrade"):
        sys.exit(cmd_self_update(args))
    elif args.subcommand == "doctor":
        sys.exit(cmd_doctor(args))
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
