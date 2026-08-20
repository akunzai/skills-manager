"""Unit tests for skills-manager."""

import json
import os
import shutil
import tempfile
import unittest
from pathlib import Path

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
    copy_skill_folder,
    discover_skills_in_repo,
    ensure_agent_symlink,
    execute_post_hooks,
    get_target_agents_for_skill,
    remove_agent_symlinks,
    scan_all_skills,
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


if __name__ == "__main__":
    unittest.main()
