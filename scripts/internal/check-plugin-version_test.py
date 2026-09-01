#!/usr/bin/env python3
import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GATE = ROOT / "scripts/internal/check-plugin-version.py"
MANIFESTS = (
    ".claude-plugin/plugin.json",
    ".claude-plugin/marketplace.json",
    "plugins/ralphex/.codex-plugin/plugin.json",
    "plugins/ralphex/plugin.json",
)


class PluginVersionGateTest(unittest.TestCase):
    def setUp(self):
        self.tempdir = tempfile.TemporaryDirectory(prefix="ralphex-plugin-version-gate-")
        self.root = Path(self.tempdir.name)
        for path in MANIFESTS:
            target = self.root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / path, target)
        for path in ("assets/claude/skills", "plugins/ralphex/skills"):
            shutil.copytree(ROOT / path, self.root / path)
        self.git("init", "-q")
        self.set_versions("1.0.0")
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "base")
        self.base = self.git("rev-parse", "HEAD").stdout.strip()

    def tearDown(self):
        self.tempdir.cleanup()

    def git(self, *args):
        return subprocess.run(
            ["git", *args],
            cwd=self.root,
            capture_output=True,
            text=True,
            check=True,
        )

    def set_versions(self, version):
        for path in MANIFESTS:
            manifest = self.root / path
            data = json.loads(manifest.read_text())
            if path.endswith("marketplace.json"):
                data["plugins"][0]["version"] = version
            else:
                data["version"] = version
            manifest.write_text(json.dumps(data))

    def run_gate(self, base=None):
        command = ["python3", str(GATE), "--root", str(self.root)]
        if base is not None:
            command.extend(("--base", base))
        return subprocess.run(command, capture_output=True, text=True, check=False)

    def change_payload(self):
        path = self.root / "plugins/ralphex/skills/ralphex/SKILL.md"
        path.write_text(path.read_text() + "\nchanged\n")

    def test_local_check_without_base_requires_consistent_versions(self):
        result = self.run_gate()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no git base provided", result.stdout)

    def test_zero_event_base_is_treated_as_unavailable(self):
        result = self.run_gate("0" * 40)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("no git base provided", result.stdout)

    def test_unchanged_payload_allows_same_version(self):
        result = self.run_gate(self.base)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("payload is unchanged", result.stdout)

    def test_changed_payload_requires_version_bump(self):
        self.change_payload()
        result = self.run_gate(self.base)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("plugin version remains 1.0.0", result.stderr)

    def test_changed_payload_accepts_consistent_version_bump(self):
        self.change_payload()
        self.set_versions("1.0.1")
        result = self.run_gate(self.base)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("changed with a version bump", result.stdout)

    def test_base_may_precede_new_distribution_manifests(self):
        saved = {}
        for path in MANIFESTS[2:]:
            manifest = self.root / path
            saved[path] = manifest.read_text()
            manifest.unlink()
        self.git("add", "-u")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", "partial base")
        partial_base = self.git("rev-parse", "HEAD").stdout.strip()
        for path, text in saved.items():
            (self.root / path).write_text(text)
        self.change_payload()
        self.set_versions("1.0.1")

        result = self.run_gate(partial_base)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("changed with a version bump", result.stdout)

    def test_untracked_payload_requires_version_bump(self):
        path = self.root / "plugins/ralphex/skills/new-skill/SKILL.md"
        path.parent.mkdir()
        path.write_text("new")
        result = self.run_gate(self.base)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("plugin version remains 1.0.0", result.stderr)

    def test_rejects_inconsistent_current_versions(self):
        path = self.root / "plugins/ralphex/plugin.json"
        data = json.loads(path.read_text())
        data["version"] = "2.0.0"
        path.write_text(json.dumps(data))
        result = self.run_gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("current plugin versions do not agree", result.stderr)


if __name__ == "__main__":
    unittest.main()
