"""Self-update manager for the skills CLI binary."""

import json
import os
import shutil
import sys
import tempfile
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Dict, Optional, Tuple

from . import __version__

GITHUB_REPO = "akunzai/skills-manager"
RELEASES_API_URL = f"https://api.github.com/repos/{GITHUB_REPO}/releases"


def get_current_executable_path() -> Path:
    """
    Locate the current executable path of the skills CLI binary.
    Prioritizes sys.argv[0] if it is a standalone script/binary,
    then PATH lookup via shutil.which("skills"),
    and falls back to ~/.local/bin/skills.
    """
    if sys.argv and sys.argv[0]:
        candidate = Path(sys.argv[0]).expanduser().resolve()
        if candidate.is_file() and not candidate.name.endswith(".py"):
            if candidate.suffix.lower() in (".cmd", ".bat", ".ps1"):
                zipapp_cand = candidate.parent / "skills"
                if zipapp_cand.exists() or (candidate.parent / "skills.pyz").exists():
                    return zipapp_cand
            return candidate

    which_skills = shutil.which("skills")
    if which_skills:
        cand = Path(which_skills).expanduser().resolve()
        if cand.is_file():
            if cand.suffix.lower() in (".cmd", ".bat", ".ps1"):
                zipapp_cand = cand.parent / "skills"
                if zipapp_cand.exists() or (cand.parent / "skills.pyz").exists():
                    return zipapp_cand
            return cand

    return Path.home() / ".local" / "bin" / "skills"


def parse_semver(v: str) -> Tuple[int, ...]:
    """Parse version string (e.g. 'v0.1.0' or '1.2.3') into numeric tuple."""
    clean = v.strip().lstrip("v").split("-")[0]
    parts = []
    for part in clean.split("."):
        try:
            parts.append(int(part))
        except ValueError:
            parts.append(0)
    while len(parts) < 3:
        parts.append(0)
    return tuple(parts)


def fetch_release_info(
    version_tag: Optional[str] = None,
    timeout: int = 10
) -> Dict[str, Any]:
    """
    Fetch release metadata from GitHub Releases API using standard library urllib.
    If version_tag is specified, fetches /tags/{version_tag}.
    Otherwise fetches /latest.
    """
    if version_tag:
        clean_tag = version_tag if version_tag.startswith("v") else f"v{version_tag}"
        url = f"{RELEASES_API_URL}/tags/{clean_tag}"
    else:
        url = f"{RELEASES_API_URL}/latest"

    req = urllib.request.Request(
        url,
        headers={
            "User-Agent": f"skills-manager/{__version__}",
            "Accept": "application/vnd.github.v3+json",
        }
    )

    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            if response.status != 200:
                raise RuntimeError(f"GitHub API returned HTTP {response.status}")
            data = json.loads(response.read().decode("utf-8"))
            return data
    except urllib.error.HTTPError as e:
        if e.code == 404:
            raise RuntimeError(f"Release not found: {version_tag or 'latest'}")
        elif e.code == 403:
            raise RuntimeError("GitHub API rate limit exceeded. Please try again later.")
        raise RuntimeError(f"HTTP error fetching release info: {e.code} {e.reason}")
    except Exception as e:
        raise RuntimeError(f"Failed to fetch release info from GitHub: {e}")


def check_self_update(
    target_version: Optional[str] = None
) -> Dict[str, Any]:
    """
    Check if a newer version of skills CLI is available on GitHub Releases.
    Returns dictionary with status, current_version, latest_version, asset_url, release_notes.
    """
    current_v = __version__
    rel_info = fetch_release_info(target_version)

    latest_tag = rel_info.get("tag_name", "").strip()
    latest_v = latest_tag.lstrip("v")
    body = rel_info.get("body", "")

    # Locate the standalone binary asset named 'skills'
    assets = rel_info.get("assets", [])
    asset_url = None
    asset_size = 0
    for asset in assets:
        if asset.get("name") == "skills":
            asset_url = asset.get("browser_download_url")
            asset_size = asset.get("size", 0)
            break

    if target_version:
        is_newer = (latest_v != current_v)
    else:
        is_newer = parse_semver(latest_v) > parse_semver(current_v)

    return {
        "current_version": current_v,
        "latest_version": latest_v,
        "latest_tag": latest_tag,
        "update_available": is_newer,
        "asset_url": asset_url,
        "asset_size": asset_size,
        "release_notes": body,
        "html_url": rel_info.get("html_url", ""),
    }


def download_and_install_binary(
    asset_url: str,
    target_path: Optional[Path] = None,
    timeout: int = 30
) -> Path:
    """
    Download standalone binary from asset_url and atomically replace target_path.
    """
    dest = target_path or get_current_executable_path()
    dest.parent.mkdir(parents=True, exist_ok=True)

    fd, tmp_path_str = tempfile.mkstemp(prefix="skills_update_", dir=dest.parent)
    os.close(fd)
    tmp_path = Path(tmp_path_str)

    req = urllib.request.Request(
        asset_url,
        headers={
            "User-Agent": f"skills-manager/{__version__}",
            "Accept": "application/octet-stream",
        }
    )

    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            with open(tmp_path, "wb") as out_f:
                shutil.copyfileobj(response, out_f)

        if tmp_path.stat().st_size < 100:
            raise RuntimeError("Downloaded binary is too small or corrupted.")

        os.chmod(tmp_path, 0o755)

        if sys.platform == "win32" and dest.exists():
            old_backup = dest.with_name(f"{dest.name}.old")
            if old_backup.exists():
                try:
                    old_backup.unlink()
                except Exception:
                    pass
            try:
                dest.rename(old_backup)
            except Exception:
                pass

        os.replace(tmp_path, dest)

        # On Windows, ensure skills.cmd and skills.ps1 wrappers exist next to the binary
        if sys.platform == "win32":
            cmd_wrapper = dest.parent / "skills.cmd"
            if not cmd_wrapper.exists():
                cmd_wrapper.write_text(f'@echo off\npython "%~dp0{dest.name}" %*\n', encoding="ascii")
            ps1_wrapper = dest.parent / "skills.ps1"
            if not ps1_wrapper.exists():
                ps1_wrapper.write_text(f'& python "$PSScriptRoot\\{dest.name}" @args\n', encoding="utf-8")

        return dest
    except Exception as e:
        if tmp_path.exists():
            try:
                tmp_path.unlink()
            except Exception:
                pass
        raise RuntimeError(f"Failed to download and install binary: {e}")
