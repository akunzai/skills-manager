"""Unit tests for skills-manager."""

import json
import os
import shutil
import tempfile
import unittest
from pathlib import Path

from unittest.mock import MagicMock, patch

from skills_manager.cli import cmd_add, cmd_ls, cmd_outdated, cmd_self_update, cmd_update
from skills_manager.ui import prompt_multi_select
from skills_manager.config import (
    add_local_command_entry,
    add_local_symlink_entry,
    add_remote_skill_entry,
    get_default_config,
    load_config,
    remove_skill_entry,
    save_config,
)
from skills_manager.updater import (
    check_self_update,
    download_and_install_binary,
    fetch_release_info,
    get_current_executable_path,
    parse_semver,
)
from skills_manager.engine import (
    check_all_remote_skills_outdated,
    check_repo_update_status,
    copy_skill_folder,
    discover_skills_in_repo,
    ensure_agent_symlink,
    execute_post_hooks,
    get_local_repo_commit,
    get_remote_repo_commit,
    get_target_agents_for_skill,
    remove_agent_symlinks,
    scan_all_skills,
    update_remote_skills,
)


class TestConfigManagement(unittest.TestCase):
    def setUp(self):
        self.temp_dir = Path(tempfile.mkdtemp())
        self.config_path = self.temp_dir / "skills.json"

    def tearDown(self):
        shutil.rmtree(self.temp_dir)

    def test_default_config(self):
        cfg = get_default_config()
        self.assertEqual(cfg["version"], 1)
        self.assertIn("claude-code", cfg["settings"]["defaultAgents"])

    def test_save_and_load_config(self):
        cfg = get_default_config()
        add_remote_skill_entry(cfg, "mattpocock/skills", "ask-matt", "skills/engineering/ask-matt")
        add_local_symlink_entry(cfg, "terminal-browser", "~/.local/share/terminal-browser")
        add_local_command_entry(cfg, "agentsview-finding-history", "agentsview skills install", "which agentsview")
        save_config(cfg, self.config_path)

        loaded = load_config(self.config_path)
        self.assertIn("mattpocock/skills", loaded["remote"])
        self.assertEqual(loaded["remote"]["mattpocock/skills"]["skills"]["ask-matt"], "skills/engineering/ask-matt")
        self.assertIn("terminal-browser", loaded["local"])
        self.assertIn("agentsview-finding-history", loaded["local"])

    def test_remove_skill_entry(self):
        cfg = get_default_config()
        add_remote_skill_entry(cfg, "mattpocock/skills", "ask-matt", "skills/engineering/ask-matt")
        add_local_symlink_entry(cfg, "terminal-browser", "/path/to/tb")

        removed = remove_skill_entry(cfg, "ask-matt")
        self.assertTrue(removed)
        self.assertNotIn("mattpocock/skills", cfg["remote"])

        removed_local = remove_skill_entry(cfg, "terminal-browser")
        self.assertTrue(removed_local)
        self.assertNotIn("terminal-browser", cfg["local"])


class TestEngineOperations(unittest.TestCase):
    def setUp(self):
        self.temp_dir = Path(tempfile.mkdtemp())
        self.skills_dir = self.temp_dir / ".agents" / "skills"
        self.skills_dir.mkdir(parents=True)

    def tearDown(self):
        shutil.rmtree(self.temp_dir)

    def test_discover_skills_in_mock_repo(self):
        repo_dir = self.temp_dir / "mock-repo"
        skill1_dir = repo_dir / "skills" / "my-skill"
        skill1_dir.mkdir(parents=True)
        (skill1_dir / "SKILL.md").write_text("---\nname: my-skill\n---\n# My Skill", encoding="utf-8")

        skill2_dir = repo_dir / "skills" / "other-skill"
        skill2_dir.mkdir(parents=True)
        (skill2_dir / "SKILL.md").write_text("# Other Skill", encoding="utf-8")

        discovered = discover_skills_in_repo(repo_dir)
        self.assertEqual(len(discovered), 2)
        self.assertIn("my-skill", discovered)
        self.assertIn("other-skill", discovered)

    def test_copy_skill_folder(self):
        src_dir = self.temp_dir / "src-skill"
        src_dir.mkdir()
        (src_dir / "SKILL.md").write_text("Test content", encoding="utf-8")

        target_dir = self.skills_dir / "target-skill"
        copy_skill_folder(src_dir, target_dir)

        self.assertTrue((target_dir / "SKILL.md").exists())
        self.assertEqual((target_dir / "SKILL.md").read_text(encoding="utf-8"), "Test content")

    def test_post_hooks_execution(self):
        test_file = self.temp_dir / "hook_created.txt"
        hooks = [
            {
                "name": "touch-test",
                "run": f"touch '{test_file}'"
            }
        ]
        results = execute_post_hooks(hooks)
        self.assertEqual(len(results), 1)
        self.assertTrue(results[0][1])
        self.assertTrue(test_file.exists())

    def test_scan_all_skills_sorted_by_source_and_name(self):
        config_data = {
            "remote": {
                "b-repo/skills": {
                    "skills": {"alpha": "skills/alpha", "zeta": "skills/zeta"}
                },
                "a-repo/skills": {
                    "skills": {"beta": "skills/beta", "gamma": "skills/gamma"}
                }
            },
            "local": {
                "local-tool": {
                    "type": "command",
                    "command": "tool install"
                }
            }
        }
        skills = scan_all_skills(config_data, skills_dir=self.skills_dir)
        # a-repo/skills: beta, gamma
        # b-repo/skills: alpha, zeta
        # tool install: local-tool
        keys = [(s.source.lower(), s.name.lower()) for s in skills]
        self.assertEqual(keys, sorted(keys))

    def test_cmd_ls_source_filter(self):
        import argparse
        import io
        from unittest.mock import patch

        config_path = self.temp_dir / "skills.json"
        config_data = {
            "remote": {
                "mattpocock/skills": {
                    "skills": {"ask-matt": "skills/ask-matt"}
                },
                "akunzai/agent-skills": {
                    "skills": {"tidy-commits": "skills/tidy-commits"}
                }
            },
            "local": {
                "local-tool": {
                    "type": "command",
                    "command": "agentsview skills install"
                }
            }
        }
        save_config(config_data, config_path)

        # 1. Filter by specific remote repo substring
        args = argparse.Namespace(
            config=str(config_path),
            skills_dir=str(self.skills_dir),
            agent=None,
            source="mattpocock",
            json=True
        )
        with patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            ret = cmd_ls(args)
            self.assertEqual(ret, 0)
            data = json.loads(mock_stdout.getvalue())
            self.assertEqual(len(data), 1)
            self.assertEqual(data[0]["name"], "ask-matt")

        # 2. Filter by local type
        args_local = argparse.Namespace(
            config=str(config_path),
            skills_dir=str(self.skills_dir),
            agent=None,
            source="local",
            json=True
        )
        with patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            ret = cmd_ls(args_local)
            self.assertEqual(ret, 0)
            data = json.loads(mock_stdout.getvalue())
            self.assertEqual(len(data), 1)
            self.assertEqual(data[0]["name"], "local-tool")

        # 3. Filter with no matches
        args_empty = argparse.Namespace(
            config=str(config_path),
            skills_dir=str(self.skills_dir),
            agent=None,
            source="nonexistent",
            json=False
        )
        with patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
            ret = cmd_ls(args_empty)
            self.assertEqual(ret, 0)
            self.assertIn("No skills found matching the specified filters.", mock_stdout.getvalue())


class TestOutdatedAndUpdateOperations(unittest.TestCase):
    def setUp(self):
        self.temp_dir = Path(tempfile.mkdtemp())
        self.cache_dir = self.temp_dir / "cache"
        self.skills_dir = self.temp_dir / ".agents" / "skills"
        self.config_path = self.temp_dir / "skills.json"

        self.cache_dir.mkdir(parents=True)
        self.skills_dir.mkdir(parents=True)

        self.config = get_default_config()
        add_remote_skill_entry(self.config, "mattpocock/skills", "triage", "skills/triage")
        add_remote_skill_entry(self.config, "mattpocock/skills", "to-tickets", "skills/to-tickets")
        save_config(self.config, self.config_path)

    def tearDown(self):
        shutil.rmtree(self.temp_dir)

    @patch("skills_manager.engine.run_cmd")
    def test_get_local_repo_commit(self, mock_run_cmd):
        repo_dest = self.cache_dir / "mattpocock/skills"
        repo_dest.mkdir(parents=True)
        (repo_dest / ".git").mkdir()

        mock_proc = MagicMock()
        mock_proc.stdout = "1111222233334444555566667777888899990000\n"
        mock_run_cmd.return_value = mock_proc

        sha = get_local_repo_commit(repo_dest)
        self.assertEqual(sha, "1111222233334444555566667777888899990000")

    @patch("skills_manager.engine.run_cmd")
    def test_get_remote_repo_commit(self, mock_run_cmd):
        mock_proc = MagicMock()
        mock_proc.stdout = "aaaabbbbccccddddeeeeffff0000111122223333\tHEAD\n"
        mock_run_cmd.return_value = mock_proc

        sha = get_remote_repo_commit("mattpocock/skills")
        self.assertEqual(sha, "aaaabbbbccccddddeeeeffff0000111122223333")

    @patch("skills_manager.engine.get_remote_repo_commit")
    @patch("skills_manager.engine.get_local_repo_commit")
    def test_check_repo_update_status(self, mock_local_sha, mock_remote_sha):
        repo_info = self.config["remote"]["mattpocock/skills"]

        # 1. Up to date
        mock_local_sha.return_value = "commit123"
        mock_remote_sha.return_value = "commit123"
        res = check_repo_update_status("mattpocock/skills", repo_info, cache_dir=self.cache_dir)
        self.assertEqual(res["status"], "up_to_date")

        # 2. Update available
        mock_local_sha.return_value = "commit123"
        mock_remote_sha.return_value = "commit456"
        res = check_repo_update_status("mattpocock/skills", repo_info, cache_dir=self.cache_dir)
        self.assertEqual(res["status"], "update_available")

        # 3. Not installed
        mock_local_sha.return_value = None
        mock_remote_sha.return_value = "commit456"
        res = check_repo_update_status("mattpocock/skills", repo_info, cache_dir=self.cache_dir)
        self.assertEqual(res["status"], "not_installed")

    @patch("skills_manager.engine.check_repo_update_status")
    def test_check_all_remote_skills_outdated(self, mock_check_status):
        mock_check_status.return_value = {
            "source": "mattpocock/skills",
            "url": "https://github.com/mattpocock/skills.git",
            "branch": "HEAD",
            "skills": ["triage", "to-tickets"],
            "local_sha": "abc",
            "remote_sha": "def",
            "status": "update_available",
            "cache_path": self.cache_dir / "mattpocock/skills"
        }
        results = check_all_remote_skills_outdated(self.config, cache_dir=self.cache_dir)
        self.assertEqual(len(results), 1)
        self.assertEqual(results[0]["status"], "update_available")

    @patch("skills_manager.engine.ensure_agent_symlink")
    @patch("skills_manager.engine.ensure_git_repo")
    @patch("skills_manager.engine.check_repo_update_status")
    def test_update_remote_skills(self, mock_check_status, mock_ensure_git, mock_ensure_symlink):
        # Create mock repo in cache
        mock_repo = self.cache_dir / "mattpocock" / "skills"
        (mock_repo / "skills" / "triage").mkdir(parents=True)
        (mock_repo / "skills" / "triage" / "SKILL.md").write_text("Triage Skill", encoding="utf-8")
        (mock_repo / "skills" / "to-tickets").mkdir(parents=True)
        (mock_repo / "skills" / "to-tickets" / "SKILL.md").write_text("To Tickets Skill", encoding="utf-8")
        (mock_repo / ".git").mkdir()

        mock_ensure_git.return_value = mock_repo
        mock_check_status.return_value = {
            "source": "mattpocock/skills",
            "status": "update_available",
            "local_sha": "old",
            "remote_sha": "new"
        }

        # Dry run test
        res_dry = update_remote_skills(
            self.config,
            dry_run=True,
            skills_dir=self.skills_dir,
            cache_dir=self.cache_dir
        )
        self.assertEqual(len(res_dry["updated_repos"]), 1)
        self.assertEqual(res_dry["updated_repos"][0]["dry_run"], True)

        # Real update
        progress_events = []
        def on_prog(event, data):
            progress_events.append(event)

        res_real = update_remote_skills(
            self.config,
            dry_run=False,
            skills_dir=self.skills_dir,
            cache_dir=self.cache_dir,
            on_progress=on_prog,
        )
        self.assertEqual(len(res_real["updated_skills"]), 2)
        self.assertTrue((self.skills_dir / "triage" / "SKILL.md").exists())
        self.assertTrue((self.skills_dir / "to-tickets" / "SKILL.md").exists())
        self.assertIn("update_start", progress_events)
        self.assertIn("skill_restored", progress_events)
        self.assertIn("repo_done", progress_events)

    def test_repo_suffix_stripping(self):
        """Ensure repositories ending in g, i, t are not truncated by rstrip."""
        from skills_manager.engine import ensure_git_repo
        with patch("skills_manager.engine.run_cmd") as mock_run:
            mock_run.return_value.returncode = 0
            mock_run.return_value.stdout = "mock-sha"

            test_cases = [
                ("openclaw/imsg", "openclaw/imsg"),
                ("openclaw/imsg.git", "openclaw/imsg"),
                ("shadcn/ui", "shadcn/ui"),
                ("shadcn/ui.git", "shadcn/ui"),
                ("microsoft/playwright-cli", "microsoft/playwright-cli"),
                ("microsoft/webwright", "microsoft/webwright"),
                ("dgrr/tgcli", "dgrr/tgcli"),
            ]
            for raw_source, expected_path in test_cases:
                dest = ensure_git_repo(raw_source, cache_dir=self.cache_dir)
                self.assertEqual(dest.name, expected_path.split("/")[-1])

    @patch("skills_manager.engine.check_all_remote_skills_outdated")
    def test_cmd_outdated_json(self, mock_check_all):
        mock_check_all.return_value = [
            {
                "source": "mattpocock/skills",
                "url": "https://github.com/mattpocock/skills.git",
                "branch": "HEAD",
                "status": "update_available",
                "local_sha": "1111222233334444555566667777888899990000",
                "remote_sha": "aaaabbbbccccddddeeeeffff0000111122223333",
                "skills": ["triage"],
            }
        ]

        class Args:
            config = str(self.config_path)
            cache_dir = str(self.cache_dir)
            json = True

        ret = cmd_outdated(Args())
        self.assertEqual(ret, 0)

    @patch("skills_manager.cli.update_remote_skills")
    def test_cmd_update(self, mock_update):
        mock_update.return_value = {
            "updated_repos": [{"source": "mattpocock/skills", "skills": ["triage"], "new_sha": "new123"}],
            "updated_skills": ["triage"],
            "skipped_repos": [],
            "errors": [],
            "post_hooks": [],
        }

        class Args:
            config = str(self.config_path)
            skills_dir = str(self.skills_dir)
            cache_dir = str(self.cache_dir)
            targets = ["triage"]
            force = False
            dry_run = False
            json = False

        ret = cmd_update(Args())
        self.assertEqual(ret, 0)
        mock_update.assert_called_once()


class TestSelfUpdateOperations(unittest.TestCase):
    def setUp(self):
        self.temp_dir = Path(tempfile.mkdtemp())

    def tearDown(self):
        shutil.rmtree(self.temp_dir)

    def test_parse_semver(self):
        self.assertEqual(parse_semver("v0.1.0"), (0, 1, 0))
        self.assertEqual(parse_semver("1.2.3"), (1, 2, 3))
        self.assertEqual(parse_semver("v2.0"), (2, 0, 0))
        self.assertEqual(parse_semver("v0.2.0-beta.1"), (0, 2, 0))
        self.assertTrue(parse_semver("v0.2.0") > parse_semver("v0.1.0"))

    def test_get_current_executable_path(self):
        p = get_current_executable_path()
        self.assertIsInstance(p, Path)

    @patch("skills_manager.updater.urllib.request.urlopen")
    def test_fetch_release_info(self, mock_urlopen):
        mock_resp = MagicMock()
        mock_resp.status = 200
        mock_resp.read.return_value = json.dumps({
            "tag_name": "v0.2.0",
            "body": "Release notes for v0.2.0",
            "assets": [
                {
                    "name": "skills",
                    "browser_download_url": "https://github.com/akunzai/skills-manager/releases/download/v0.2.0/skills",
                    "size": 12345
                }
            ]
        }).encode("utf-8")
        mock_resp.__enter__.return_value = mock_resp
        mock_urlopen.return_value = mock_resp

        data = fetch_release_info()
        self.assertEqual(data["tag_name"], "v0.2.0")
        self.assertEqual(len(data["assets"]), 1)

    @patch("skills_manager.updater.fetch_release_info")
    def test_check_self_update(self, mock_fetch):
        mock_fetch.return_value = {
            "tag_name": "v99.0.0",
            "body": "Future version",
            "assets": [
                {
                    "name": "skills",
                    "browser_download_url": "https://example.com/skills",
                    "size": 10000
                }
            ]
        }

        info = check_self_update()
        self.assertTrue(info["update_available"])
        self.assertEqual(info["latest_tag"], "v99.0.0")
        self.assertEqual(info["asset_url"], "https://example.com/skills")

    @patch("skills_manager.updater.urllib.request.urlopen")
    def test_download_and_install_binary(self, mock_urlopen):
        target_binary = self.temp_dir / "skills"
        target_binary.write_text("#!/usr/bin/env python3\nold", encoding="utf-8")

        mock_resp = MagicMock()
        mock_resp.read = MagicMock(side_effect=[b"#!/usr/bin/env python3\nnew_binary_content_bytes" * 10, b""])
        mock_resp.__enter__.return_value = mock_resp
        mock_urlopen.return_value = mock_resp

        installed = download_and_install_binary("https://example.com/skills", target_path=target_binary)
        self.assertEqual(installed, target_binary)
        self.assertTrue(target_binary.exists())
        self.assertIn("new_binary_content_bytes", target_binary.read_text(encoding="utf-8"))

    @patch("skills_manager.cli.check_self_update")
    def test_cmd_self_update_check(self, mock_check):
        mock_check.return_value = {
            "current_version": "0.1.0",
            "latest_version": "0.2.0",
            "latest_tag": "v0.2.0",
            "update_available": True,
            "asset_url": "https://example.com/skills",
            "asset_size": 10000,
            "release_notes": "Notes",
        }

        class Args:
            version = None
            check = True
            force = False
            dry_run = False
            json = True

        ret = cmd_self_update(Args())
        self.assertEqual(ret, 0)

    @patch("sys.argv", ["skills", "--version"])
    def test_cli_version_flag(self):
        from skills_manager.cli import main
        with self.assertRaises(SystemExit) as cm:
            main()
        self.assertEqual(cm.exception.code, 0)


class TestUIOperations(unittest.TestCase):
    @patch("sys.stdin.isatty", return_value=False)
    def test_prompt_multi_select_non_tty(self, mock_isatty):
        res = prompt_multi_select("Title", [("skill1", False, None)])
        self.assertIsNone(res)

    @patch("skills_manager.ui.read_key", side_effect=["enter"])
    @patch("sys.stdin.isatty", return_value=True)
    def test_prompt_multi_select_confirm_default_all(self, mock_isatty, mock_key):
        items = [("skill1", False, None), ("skill2", True, None)]
        res = prompt_multi_select("Title", items, default_all=True)
        self.assertEqual(res, ["skill1", "skill2"])

    @patch("skills_manager.ui.read_key", side_effect=["space", "down", "space", "enter"])
    @patch("sys.stdin.isatty", return_value=True)
    def test_prompt_multi_select_toggle_items(self, mock_isatty, mock_key):
        items = [("skill1", False, None), ("skill2", False, None)]
        # Initial: [True, True]. Press space on skill1 -> [False, True]. Down -> cursor on skill2. Press space -> [False, False]. Enter -> []
        res = prompt_multi_select("Title", items, default_all=True)
        self.assertEqual(res, [])

    @patch("skills_manager.ui.read_key", side_effect=["escape"])
    @patch("sys.stdin.isatty", return_value=True)
    def test_prompt_multi_select_cancel_escape(self, mock_isatty, mock_key):
        items = [("skill1", False, None)]
        res = prompt_multi_select("Title", items)
        self.assertIsNone(res)

    @patch("skills_manager.ui.read_key", side_effect=["a", "enter"])
    @patch("sys.stdin.isatty", return_value=True)
    def test_prompt_multi_select_toggle_all(self, mock_isatty, mock_key):
        items = [("skill1", False, None), ("skill2", False, None)]
        # Initial: [True, True]. Press 'a' -> all uncheck [False, False]. Enter -> []
        res = prompt_multi_select("Title", items, default_all=True)
        self.assertEqual(res, [])


class TestInteractiveAdd(unittest.TestCase):
    def setUp(self):
        self.temp_dir = Path(tempfile.mkdtemp())
        self.skills_dir = self.temp_dir / ".agents" / "skills"
        self.cache_dir = self.temp_dir / "cache"
        self.config_path = self.temp_dir / "skills.json"
        self.skills_dir.mkdir(parents=True)
        self.cache_dir.mkdir(parents=True)

        self.config = get_default_config()
        save_config(self.config, self.config_path)

    def tearDown(self):
        shutil.rmtree(self.temp_dir)

    @patch("skills_manager.cli.ensure_agent_symlink")
    @patch("skills_manager.cli.prompt_multi_select")
    @patch("skills_manager.cli.ensure_git_repo")
    @patch("sys.stdin.isatty", return_value=True)
    def test_cmd_add_interactive_selection(self, mock_isatty, mock_git, mock_prompt, mock_ensure_link):
        mock_repo = self.cache_dir / "repo"
        (mock_repo / "skills" / "skill-a").mkdir(parents=True)
        (mock_repo / "skills" / "skill-a" / "SKILL.md").write_text("# A", encoding="utf-8")
        (mock_repo / "skills" / "skill-b").mkdir(parents=True)
        (mock_repo / "skills" / "skill-b" / "SKILL.md").write_text("# B", encoding="utf-8")
        mock_git.return_value = mock_repo

        # User chooses only skill-b
        mock_prompt.return_value = ["skill-b"]

        import argparse
        args = argparse.Namespace(
            source="akunzai/agent-skills",
            config=str(self.config_path),
            skills_dir=str(self.skills_dir),
            cache_dir=str(self.cache_dir),
            skill=None,
            all=False,
            path=None,
            url=None,
            branch=None,
            agent=None,
            symlink=None,
            command=None,
            check=None,
            description=None
        )

        ret = cmd_add(args)
        self.assertEqual(ret, 0)
        self.assertTrue((self.skills_dir / "skill-b" / "SKILL.md").exists())
        self.assertFalse((self.skills_dir / "skill-a").exists())

    @patch("skills_manager.cli.ensure_git_repo")
    @patch("sys.stdin.isatty", return_value=False)
    def test_cmd_add_non_tty_fallback(self, mock_isatty, mock_git):
        mock_repo = self.cache_dir / "repo"
        (mock_repo / "skills" / "skill-a").mkdir(parents=True)
        (mock_repo / "skills" / "skill-a" / "SKILL.md").write_text("# A", encoding="utf-8")
        (mock_repo / "skills" / "skill-b").mkdir(parents=True)
        (mock_repo / "skills" / "skill-b" / "SKILL.md").write_text("# B", encoding="utf-8")
        mock_git.return_value = mock_repo

        import argparse
        args = argparse.Namespace(
            source="akunzai/agent-skills",
            config=str(self.config_path),
            skills_dir=str(self.skills_dir),
            cache_dir=str(self.cache_dir),
            skill=None,
            all=False,
            path=None,
            url=None,
            branch=None,
            agent=None,
            symlink=None,
            command=None,
            check=None,
            description=None
        )

        ret = cmd_add(args)
        self.assertEqual(ret, 1)


if __name__ == "__main__":
    unittest.main()
