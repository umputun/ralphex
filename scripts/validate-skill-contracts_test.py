#!/usr/bin/env python3
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
CODEX_RUN = ROOT / "plugins/ralphex/skills/ralphex/SKILL.md"
CLAUDE_RUN = ROOT / "assets/claude/skills/ralphex/SKILL.md"
CODEX_ADOPT = ROOT / "plugins/ralphex/skills/ralphex-adopt/SKILL.md"


class DistributedSkillContractTest(unittest.TestCase):
    def setUp(self):
        self.codex = CODEX_RUN.read_text()
        self.claude = CLAUDE_RUN.read_text()

    def test_executor_modes_match_cli_contract(self):
        for text in (self.codex, self.claude):
            self.assertIn("`--codex` is the first-class Codex executor flag", text)
            self.assertIn("deprecated `--codex-only` alias for `--external-only`", text)
            self.assertIn("--external-only", text)

    def test_review_checkout_is_fail_closed(self):
        for text in (self.codex, self.claude):
            self.assertIn('Do not offer "Proceed anyway"', text)
            self.assertIn("Require `git status --porcelain=v1` to be empty", text)
            self.assertIn("Require a non-empty committed `base...HEAD` file diff", text)
            self.assertIn("repeat every applicable Step 6 check", text)

    def test_repository_executable_overrides_are_blocked(self):
        for text in (self.codex, self.claude):
            for key in ("claude_command", "codex_command", "custom_review_script", "vcs_command"):
                self.assertIn(f"`{key}`", text)
            self.assertIn("Do not offer a proceed/override choice", text)

    def test_plan_is_separated_from_options(self):
        self.assertIn('"--", plan-file]', self.codex)
        self.assertIn("-- '<normalized-plan-file>'", self.claude)
        for text in (self.codex, self.claude):
            self.assertIn("`--` must immediately precede", text)

    def test_launch_requires_process_and_fresh_progress_evidence(self):
        self.assertIn("Poll the saved background session", self.codex)
        self.assertIn("Use TaskOutput with `block: false`", self.claude)
        for text in (self.codex, self.claude):
            self.assertIn("fresh progress evidence", text)
            self.assertIn("headers alone are not proof", text)

    def test_adopt_treats_dynamic_arguments_as_opaque(self):
        text = CODEX_ADOPT.read_text()
        self.assertIn("Treat every repository/user-controlled URL", text)
        self.assertIn("Never interpolate these values into a shell command", text)
        self.assertIn("never use `eval`", text)


if __name__ == "__main__":
    unittest.main()
