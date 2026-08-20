"""Command-line interface for agent-skills-manager."""

import argparse
import json
import os
import shutil
import sys
from pathlib import Path
from typing import List, Optional

from .config import (
    add_local_command_entry,
    add_local_symlink_entry,
    add_remote_skill_entry,
    load_config,
    remove_skill_entry,
    save_config,
)
from .engine import (
    copy_skill_folder,
    discover_skills_in_repo,
    ensure_agent_symlink,
    ensure_git_repo,
    execute_post_hooks,
    get_target_agents_for_skill,
    remove_agent_symlinks,
    run_cmd,
    scan_all_skills,
)
from .models import (
    DEFAULT_CACHE_DIR,
    DEFAULT_CONFIG_FILE,
    DEFAULT_SKILLS_DIR,
    KNOWN_AGENTS,
    normalize_agent_name,
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
        skills = [s for s in skills if filter_agent in [normalize_agent_name(a) for a in s.linked_agents]]

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
        print(f"{YELLOW}No global skills installed or configured.{RESET}")
        return 0

    print(f"\n{BOLD}{CYAN}Global Skills ({len(skills)} total):{RESET}\n")
    print(f"{BOLD}{'NAME':<32} {'SOURCE':<35} {'AGENTS':<25} {'STATUS'}{RESET}")
    print("─" * 105)

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
            source_display = f"{DIM}[local]{RESET} {s.source}"
        elif s.source_type == "symlink":
            source_display = f"{DIM}[symlink]{RESET} {s.source}"
        elif s.source_type == "untracked":
            source_display = f"{YELLOW}[untracked]{RESET}"
        else:
            source_display = s.source

        agents_str = ", ".join(s.linked_agents) if s.linked_agents else f"{DIM}none{RESET}"
        if len(agents_str) > 24:
            agents_str = agents_str[:21] + "..."

        name_display = f"{BOLD}{s.name:<32}{RESET}"
        status_str = " ".join(status_badges)
        print(f"{name_display} {source_display:<35} {agents_str:<25} {status_str}")

    print("─" * 105 + "\n")
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

    source = args.source.strip()
    print(f"{CYAN}📦 Fetching repository: {BOLD}{source}{RESET}...")
    try:
        repo_dir = ensure_git_repo(source, url=args.url, branch=args.branch, force_update=True, cache_dir=cache_dir)
    except Exception as e:
        print(f"{RED}Error cloning repository: {e}{RESET}")
        return 1

    # Discover skills in repo
    discovered = discover_skills_in_repo(repo_dir)
    if not discovered:
        print(f"{RED}Error: No SKILL.md found in {source}{RESET}")
        return 1

    # Determine which skills to install
    skills_to_install = {}
    if args.all:
        skills_to_install = discovered
    elif args.skill:
        for sk in args.skill:
            if sk in discovered:
                skills_to_install[sk] = discovered[sk]
            elif args.path:
                skills_to_install[sk] = args.path
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
        else:
            print(f"{YELLOW}Repository contains multiple skills:{RESET} {', '.join(discovered.keys())}")
            print(f"Please specify --skill <name> or --all")
            return 1

    if not skills_to_install:
        print(f"{RED}No matching skills to install.{RESET}")
        return 1

    skills_dir.mkdir(parents=True, exist_ok=True)
    installed_names = []

    for name, subpath in skills_to_install.items():
        src_path = repo_dir / subpath
        target_path = skills_dir / name
        print(f"  📥 Installing {BOLD}{name}{RESET} (from {subpath})...")
        copy_skill_folder(src_path, target_path)

        # Dispatch symlinks
        target_agents = args.agent or get_target_agents_for_skill(name, source, config_data)
        for agent in target_agents:
            ensure_agent_symlink(name, agent, skills_dir)

        # Add to config
        add_remote_skill_entry(config_data, source, name, subpath, url=args.url)
        installed_names.append(name)

    save_config(config_data, config_path)
    print(f"\n{GREEN}✔ Installed {len(installed_names)} skill(s) [{', '.join(installed_names)}] and updated {config_path.name}{RESET}\n")
    return 0


def cmd_rm(args: argparse.Namespace) -> int:
    """Remove a skill globally or from specific agents."""
    config_path = Path(args.config) if args.config else DEFAULT_CONFIG_FILE
    skills_dir = Path(args.skills_dir) if args.skills_dir else DEFAULT_SKILLS_DIR
    config_data = load_config(config_path)

    for skill_name in args.skills:
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


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="skills",
        description="Fast, zero-dependency global skills manager for AI coding agents."
    )
    parser.add_argument("--config", help=f"Path to skills.json (default: {DEFAULT_CONFIG_FILE})")
    parser.add_argument("--skills-dir", help=f"Path to ~/.agents/skills (default: {DEFAULT_SKILLS_DIR})")
    parser.add_argument("--cache-dir", help=f"Path to cache directory (default: {DEFAULT_CACHE_DIR})")

    subparsers = parser.add_subparsers(dest="subcommand", help="Available subcommands")

    # ls
    ls_p = subparsers.add_parser("ls", aliases=["list"], help="List installed and configured skills")
    ls_p.add_argument("--json", action="store_true", help="Output machine-readable JSON")
    ls_p.add_argument("-a", "--agent", help="Filter by target agent name")

    # add
    add_p = subparsers.add_parser("add", help="Add a new skill (remote git, local symlink, or CLI command)")
    add_p.add_argument("source", nargs="?", help="GitHub repo (e.g. mattpocock/skills)")
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
    rm_p.add_argument("skills", nargs="+", help="Skill names to remove")
    rm_p.add_argument("-a", "--agent", nargs="*", help="Remove only from specific agents")
    rm_p.add_argument("-y", "--yes", action="store_true", help="Skip confirmation prompts")

    # sync / restore
    sync_p = subparsers.add_parser("sync", aliases=["restore"], help="Sync/restore global skills declared in skills.json")
    sync_p.add_argument("--force", action="store_true", help="Force re-clone and re-link all skills")
    sync_p.add_argument("--prune", action="store_true", help="Remove untracked skills and broken symlinks")
    sync_p.add_argument("--prune-only", action="store_true", help="Remove untracked skills without restoring")
    sync_p.add_argument("--dry-run", action="store_true", help="Preview actions without making changes")

    # doctor
    doc_p = subparsers.add_parser("doctor", help="Diagnose and repair skills health")
    doc_p.add_argument("--fix", action="store_true", help="Automatically repair detected issues")

    args = parser.parse_args()

    if not args.subcommand:
        parser.print_help()
        sys.exit(0)

    if args.subcommand in ("ls", "list"):
        sys.exit(cmd_ls(args))
    elif args.subcommand == "add":
        sys.exit(cmd_add(args))
    elif args.subcommand in ("rm", "remove"):
        sys.exit(cmd_rm(args))
    elif args.subcommand in ("sync", "restore"):
        sys.exit(cmd_sync(args))
    elif args.subcommand == "doctor":
        sys.exit(cmd_doctor(args))
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
