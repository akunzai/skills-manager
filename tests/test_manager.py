"""Unit tests for skills-manager."""

import json
import os
import shutil
import tempfile
import unittest
from pathlib import Path

from unittest.mock import MagicMock, patch

from skills_manager.cli import cmd_outdated, cmd_update
from skills_manager.config import (
    add_local_command_entry,
    add_local_symlink_entry,
    add_remote_skill_entry,
    get_default_config,
    load_config,
    remove_skill_entry,
    save_config,
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

    @patch("skills_manager.engine.ensure_git_repo")
    @patch("skills_manager.engine.check_repo_update_status")
    def test_update_remote_skills(self, mock_check_status, mock_ensure_git):
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
        res_real = update_remote_skills(
            self.config,
            dry_run=False,
            skills_dir=self.skills_dir,
            cache_dir=self.cache_dir
        )
        self.assertEqual(len(res_real["updated_skills"]), 2)
        self.assertTrue((self.skills_dir / "triage" / "SKILL.md").exists())
        self.assertTrue((self.skills_dir / "to-tickets" / "SKILL.md").exists())

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


if __name__ == "__main__":
    unittest.main()
