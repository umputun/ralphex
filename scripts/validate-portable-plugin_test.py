#!/usr/bin/env python3
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
VALIDATOR = ROOT / "scripts/validate-portable-plugin.py"


class PortablePluginValidatorTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory(prefix="ralphex-plugin-validator-")
        self.root = Path(self.tempdir.name)
        shutil.copytree(ROOT / ".agents", self.root / ".agents")
        shutil.copytree(ROOT / "plugins", self.root / "plugins")

    def tearDown(self):
        self.tempdir.cleanup()

    def run_validator(self, timeout=5):
        return subprocess.run(
            ["python3", str(VALIDATOR), "--root", str(self.root)],
            capture_output=True,
            text=True,
            check=False,
            timeout=timeout,
        )

    def test_valid_fixture(self):
        result = self.run_validator()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_missing_marketplace_path(self):
        path = self.root / ".agents/plugins/marketplace.json"
        data = json.loads(path.read_text())
        data["plugins"][0]["source"]["path"] = "./plugins/missing"
        path.write_text(json.dumps(data))
        result = self.run_validator()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("referenced path cannot be inspected", result.stderr)

    def test_rejects_implicit_invocation(self):
        path = self.root / "plugins/ralphex/skills/ralphex/agents/openai.yaml"
        data = yaml.safe_load(path.read_text())
        data["policy"]["allow_implicit_invocation"] = True
        path.write_text(yaml.safe_dump(data))
        result = self.run_validator()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("allow_implicit_invocation must be false", result.stderr)

    def test_rejects_invalid_agent_shape(self):
        path = self.root / "plugins/ralphex/skills/ralphex/agents/openai.yaml"
        data = yaml.safe_load(path.read_text())
        data["interface"]["default_prompt"] = ["not", "a", "string"]
        path.write_text(yaml.safe_dump(data))
        result = self.run_validator()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("interface.default_prompt must be a non-empty string", result.stderr)

    def test_rejects_missing_agent_manifest(self):
        path = self.root / "plugins/ralphex/skills/ralphex/agents/openai.yaml"
        path.unlink()
        result = self.run_validator()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot inspect file", result.stderr)

    def test_rejects_non_local_marketplace_source(self):
        path = self.root / ".agents/plugins/marketplace.json"
        data = json.loads(path.read_text())
        data["plugins"][0]["source"]["source"] = "git"
        path.write_text(json.dumps(data))
        result = self.run_validator()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("source must be a local source object", result.stderr)

    def test_rejects_marketplace_name_mismatch(self):
        path = self.root / ".agents/plugins/marketplace.json"
        data = json.loads(path.read_text())
        data["plugins"][0]["name"] = "different-name"
        path.write_text(json.dumps(data))
        result = self.run_validator()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("marketplace/plugin name mismatch", result.stderr)

    def test_rejects_unsupported_agent_field(self):
        path = self.root / "plugins/ralphex/skills/ralphex/agents/openai.yaml"
        data = yaml.safe_load(path.read_text())
        data["unsupported"] = True
        path.write_text(yaml.safe_dump(data))
        result = self.run_validator()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unsupported fields", result.stderr)

    def test_rejects_skills_escape_without_reading_external_fifo(self):
        plugin_root = self.root / "plugins/ralphex"
        outside = self.root.parent / f"{self.root.name}-outside"
        outside.mkdir()
        fifo = outside / "SKILL.md"
        os.mkfifo(fifo)
        shutil.rmtree(plugin_root / "skills")
        (plugin_root / "skills").symlink_to(outside, target_is_directory=True)
        self.addCleanup(shutil.rmtree, outside)

        result = self.run_validator(timeout=2)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("symlinks are not allowed in plugin tree", result.stderr)

    def test_rejects_legacy_skills_pointer_outside_fixed_directory(self):
        path = self.root / "plugins/ralphex/.codex-plugin/plugin.json"
        data = json.loads(path.read_text())
        data["skills"] = "../ralphex-copy/skills"
        path.write_text(json.dumps(data))
        result = self.run_validator()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("path must resolve to", result.stderr)


if __name__ == "__main__":
    unittest.main()
