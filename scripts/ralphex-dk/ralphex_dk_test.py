#!/usr/bin/env python3
"""unit tests for ralphex_dk docker wrapper."""

import argparse
import io
import os
import platform
import shutil
import sys
import tempfile
import threading
import time
import unittest
import unittest.mock
from pathlib import Path
from typing import Optional

# ensure same-directory imports work
_script_dir = str(Path(__file__).resolve().parent)
if _script_dir not in sys.path:
    sys.path.insert(0, _script_dir)

from ralphex_dk import (  # noqa: E402
    BEDROCK_ENV_VARS,
    DEFAULT_IMAGE,
    DEFAULT_PORT,
    DEFAULT_DOCKER_SOCKET,
    VALID_CLAUDE_PROVIDERS,
    ParsedEnvFlags,
    build_base_env_vars,
    build_bedrock_env_args,
    build_docker_command,
    build_env_vars,
    build_parser,
    build_volumes,
    detect_explicit_secrets,
    detect_git_worktree,
    detect_inherited_env_vars,
    detect_timezone,
    export_aws_profile_credentials,
    extract_env_from_flags,
    extract_macos_credentials,
    get_claude_provider,
    get_docker_socket_gid,
    get_global_gitignore,
    is_docker_enabled,
    is_sensitive_name,
    keychain_service_name,
    main,
    merge_env_flags,
    merge_volume_flags,
    parse_env_flags,
    resolve_docker_socket,
    resolve_path,
    schedule_cleanup,
    selinux_enabled,
    should_bind_port,
    symlink_target_dirs,
    validate_bedrock_config,
)


class EnvTestCase(unittest.TestCase):
    """base class for tests that modify environment variables.

    subclasses should set:
    - env_vars: list of env var names to save/clear before each test
    - save_argv: True to also save/restore sys.argv
    """

    env_vars: list[str] = []
    save_argv: bool = False

    def setUp(self) -> None:
        self._saved_env: dict[str, str | None] = {}
        for key in self.env_vars:
            self._saved_env[key] = os.environ.get(key)
            os.environ.pop(key, None)
        if self.save_argv:
            self._saved_argv = sys.argv[:]

    def tearDown(self) -> None:
        for key, val in self._saved_env.items():
            if val is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = val
        if self.save_argv:
            sys.argv[:] = self._saved_argv

class TestResolvePath(unittest.TestCase):
    def test_regular_path(self) -> None:
        tmp = Path(tempfile.mkdtemp())
        try:
            regular = tmp / "regular"
            regular.mkdir()
            self.assertEqual(resolve_path(regular), regular)
        finally:
            shutil.rmtree(tmp)

    def test_symlink(self) -> None:
        tmp = Path(tempfile.mkdtemp())
        try:
            target = tmp / "target"
            target.mkdir()
            link = tmp / "link"
            link.symlink_to(target)
            self.assertEqual(resolve_path(link), target.resolve())
        finally:
            shutil.rmtree(tmp)

class TestSymlinkTargetDirs(unittest.TestCase):
    def test_collects_symlink_targets(self) -> None:
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            target_dir = tmp / "targets" / "sub"
            target_dir.mkdir(parents=True)
            target_file = target_dir / "file.txt"
            target_file.write_text("content")

            src = tmp / "src"
            src.mkdir()
            (src / "link").symlink_to(target_file)

            dirs = symlink_target_dirs(src)
            self.assertIn(target_dir, dirs)
        finally:
            shutil.rmtree(tmp)

    def test_respects_depth_limit(self) -> None:
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            target = tmp / "far_target"
            target.mkdir()
            target_file = target / "file.txt"
            target_file.write_text("content")

            src = tmp / "src"
            # create deep nesting: src/a/b/c/link (depth=3, exceeds maxdepth=2)
            deep = src / "a" / "b" / "c"
            deep.mkdir(parents=True)
            (deep / "link").symlink_to(target_file)

            dirs = symlink_target_dirs(src, maxdepth=2)
            self.assertNotIn(target, dirs)

            # link inside depth-2 dir (src/a/b/link) exceeds find -maxdepth 2
            (src / "a" / "b" / "depth2_link").symlink_to(target_file)
            dirs = symlink_target_dirs(src, maxdepth=2)
            self.assertNotIn(target, dirs)

            # depth=1 link should work: src/a/link (within find -maxdepth 2)
            (src / "a" / "shallow_link").symlink_to(target_file)
            dirs = symlink_target_dirs(src, maxdepth=2)
            self.assertIn(target, dirs)
        finally:
            shutil.rmtree(tmp)

    def test_dir_symlink_at_depth_boundary(self) -> None:
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            target_dir = tmp / "target_dir"
            target_dir.mkdir()
            src = tmp / "src"
            subdir = src / "a"
            subdir.mkdir(parents=True)
            # directory symlink at depth 2 (find -maxdepth 2): src/a/link_dir
            (subdir / "link_dir").symlink_to(target_dir)
            dirs = symlink_target_dirs(src, maxdepth=2)
            self.assertIn(target_dir.parent, dirs)
        finally:
            shutil.rmtree(tmp)

    def test_nonexistent_dir(self) -> None:
        self.assertEqual(symlink_target_dirs(Path("/nonexistent")), [])

class TestShouldBindPort(unittest.TestCase):
    def test_with_serve(self) -> None:
        self.assertTrue(should_bind_port(["--serve", "plan.md"]))

    def test_with_s(self) -> None:
        self.assertTrue(should_bind_port(["-s", "plan.md"]))

    def test_without_serve(self) -> None:
        self.assertFalse(should_bind_port(["--review", "plan.md"]))

    def test_empty(self) -> None:
        self.assertFalse(should_bind_port([]))

class TestBuildVolumes(unittest.TestCase):
    def test_volume_pairs(self) -> None:
        with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False):
            vols = build_volumes(None)
        # volumes should come in -v pairs
        for i in range(0, len(vols), 2):
            self.assertEqual(vols[i], "-v")
            self.assertIn(":", vols[i + 1])

    def test_includes_workspace_without_selinux(self) -> None:
        with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False):
            vols = build_volumes(None)
            pwd_env = os.environ.get("PWD")
            cwd = Path(pwd_env) if pwd_env else Path(os.getcwd())
            self.assertIn(f"{cwd}:/workspace", vols)

    def test_includes_workspace_with_selinux(self) -> None:
        with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=True):
            vols = build_volumes(None)
            pwd_env = os.environ.get("PWD")
            cwd = Path(pwd_env) if pwd_env else Path(os.getcwd())
            self.assertIn(f"{cwd}:/workspace:z", vols)

    def test_includes_claude_dir_without_selinux(self) -> None:
        with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False):
            vols = build_volumes(None)
            found = any("/mnt/claude:ro" in v for v in vols)
            self.assertTrue(found, "should mount ~/.claude to /mnt/claude:ro")

    def test_includes_claude_dir_with_selinux(self) -> None:
        with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=True):
            vols = build_volumes(None)
            found = any("/mnt/claude:ro,z" in v for v in vols)
            self.assertTrue(found, "should mount ~/.claude to /mnt/claude:ro,z")

    def test_creates_ralphex_config_dir_when_missing(self) -> None:
        """build_volumes creates ~/.config/ralphex with mode 0o700 when it does not exist."""
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            fake_home = tmp / "home"
            fake_home.mkdir()
            (fake_home / ".claude").mkdir()
            ralphex_config = fake_home / ".config" / "ralphex"
            self.assertFalse(ralphex_config.exists())
            with (
                unittest.mock.patch("ralphex_dk.Path.home", return_value=fake_home),
                unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False),
            ):
                build_volumes(None)
            self.assertTrue(ralphex_config.is_dir(), "~/.config/ralphex should be created")
            mode = ralphex_config.stat().st_mode & 0o777
            self.assertEqual(mode, 0o700, f"expected mode 0700, got {oct(mode)}")
        finally:
            shutil.rmtree(tmp)

    def test_always_mounts_ralphex_config(self) -> None:
        """build_volumes always includes the ~/.config/ralphex mount even when dir was missing."""
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            fake_home = tmp / "home"
            fake_home.mkdir()
            (fake_home / ".claude").mkdir()
            with (
                unittest.mock.patch("ralphex_dk.Path.home", return_value=fake_home),
                unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False),
            ):
                vols = build_volumes(None)
            found = any("/home/app/.config/ralphex" in v for v in vols)
            self.assertTrue(found, "~/.config/ralphex should always be mounted to /home/app/.config/ralphex")
        finally:
            shutil.rmtree(tmp)

class TestBuildVolumesGitignore(unittest.TestCase):
    def test_global_gitignore_remapped_to_home_app(self) -> None:
        """global gitignore under $HOME should be mounted at /home/app/<relative>."""
        home = Path.home()
        fake_ignore = home / ".gitignore"
        with (
            unittest.mock.patch("ralphex_dk.get_global_gitignore", return_value=fake_ignore),
            unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False),
        ):
            vols = build_volumes(None)
        expected_dst = "/home/app/.gitignore"
        found = any(expected_dst + ":ro" in v for v in vols)
        self.assertTrue(found, f"expected mount destination {expected_dst}:ro in volumes, got {vols}")

    def test_global_gitignore_also_mounted_at_original_absolute_path(self) -> None:
        """gitignore under $HOME should also be mounted at original absolute path for .gitconfig refs."""
        home = Path.home()
        fake_ignore = home / ".gitignore_global"
        with (
            unittest.mock.patch("ralphex_dk.get_global_gitignore", return_value=fake_ignore),
            unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False),
        ):
            vols = build_volumes(None)
        # remapped mount for tilde-based .gitconfig references
        remapped = "/home/app/.gitignore_global"
        found_remapped = any(remapped + ":ro" in v for v in vols)
        self.assertTrue(found_remapped, f"expected remapped mount {remapped}:ro in volumes, got {vols}")
        # original absolute mount for absolute .gitconfig references
        original = str(fake_ignore)
        found_original = any(original + ":ro" in v for v in vols)
        self.assertTrue(found_original, f"expected original mount {original}:ro in volumes, got {vols}")

    def test_global_gitignore_outside_home_keeps_path(self) -> None:
        """global gitignore outside $HOME should keep its absolute path as mount destination."""
        fake_ignore = Path("/etc/gitignore_global")
        with (
            unittest.mock.patch("ralphex_dk.get_global_gitignore", return_value=fake_ignore),
            unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False),
            unittest.mock.patch("ralphex_dk.resolve_path", side_effect=lambda p: p),
        ):
            vols = build_volumes(None)
        found = any("/etc/gitignore_global:ro" in v for v in vols)
        self.assertTrue(found, f"expected /etc/gitignore_global:ro in volumes, got {vols}")

class TestDetectGitWorktree(unittest.TestCase):
    def test_regular_dir(self) -> None:
        tmp = Path(tempfile.mkdtemp())
        try:
            self.assertIsNone(detect_git_worktree(tmp))
        finally:
            shutil.rmtree(tmp)

class TestDetectTimezone(unittest.TestCase):
    def test_tz_env_takes_priority(self) -> None:
        old = os.environ.get("TZ")
        try:
            os.environ["TZ"] = "Europe/Berlin"
            self.assertEqual(detect_timezone(), "Europe/Berlin")
        finally:
            if old is None:
                os.environ.pop("TZ", None)
            else:
                os.environ["TZ"] = old

    def test_returns_string(self) -> None:
        # without TZ env, should return some timezone string (at least UTC)
        old = os.environ.pop("TZ", None)
        try:
            tz = detect_timezone()
            self.assertIsInstance(tz, str)
            self.assertTrue(len(tz) > 0)
        finally:
            if old is not None:
                os.environ["TZ"] = old

    def test_timezone_in_docker_cmd(self) -> None:
        """verify both TIME_ZONE (baseimage) and TZ (Go runtime) env vars are set."""
        old = os.environ.get("TZ")
        try:
            os.environ["TZ"] = "Asia/Tokyo"
            env_vars = build_base_env_vars()
            # find matching values in the flat list (format: ["-e", "KEY=val", ...])
            time_zone_values = [e for e in env_vars if e.startswith("TIME_ZONE=")]
            tz_values = [e for e in env_vars if e.startswith("TZ=")]
            self.assertEqual(time_zone_values, ["TIME_ZONE=Asia/Tokyo"])
            self.assertEqual(tz_values, ["TZ=Asia/Tokyo"])
        finally:
            if old is None:
                os.environ.pop("TZ", None)
            else:
                os.environ["TZ"] = old

class TestExtractCredentials(unittest.TestCase):
    def test_write_pattern_adds_trailing_newline(self) -> None:
        """credential write pattern appends newline (matching bash echo behavior)."""
        fd, tmp_path = tempfile.mkstemp()
        try:
            with os.fdopen(fd, "w") as f:
                creds = '{"token": "test"}'
                f.write(creds + "\n")
            content = Path(tmp_path).read_text()
            self.assertTrue(content.endswith("\n"), "credentials should end with newline")
            self.assertEqual(content, '{"token": "test"}\n')
        finally:
            try:
                os.unlink(tmp_path)
            except OSError:
                pass

    def test_skips_non_darwin(self) -> None:
        """extract_macos_credentials returns None on non-Darwin platforms."""
        if platform.system() == "Darwin":
            return  # skip on actual macOS
        self.assertIsNone(extract_macos_credentials(Path.home() / ".claude"))

class TestScheduleCleanup(unittest.TestCase):
    def test_cleans_up_file(self) -> None:
        """schedule_cleanup should delete the file after delay."""
        import time
        fd, tmp_path = tempfile.mkstemp()
        os.close(fd)
        p = Path(tmp_path)
        self.assertTrue(p.exists())

        # patch Timer to use a very short delay
        orig_timer = threading.Timer
        threading.Timer = lambda delay, fn: orig_timer(0.05, fn)  # type: ignore[misc,assignment]
        try:
            schedule_cleanup(p)
            time.sleep(0.2)
        finally:
            threading.Timer = orig_timer  # type: ignore[misc]
        self.assertFalse(p.exists())

    def test_none_is_noop(self) -> None:
        """schedule_cleanup with None should not raise."""
        schedule_cleanup(None)

    def test_uses_60s_delay(self) -> None:
        """schedule_cleanup should arm the timer with a 60s delay to cover slow container cold-start."""
        fd, tmp_path = tempfile.mkstemp()
        os.close(fd)
        p = Path(tmp_path)
        captured_delay: list[float] = []

        class _FakeTimer:
            def __init__(self, delay: float, fn: object) -> None:
                captured_delay.append(delay)
                self.daemon = False

            def start(self) -> None:
                pass

        orig_timer = threading.Timer
        threading.Timer = _FakeTimer  # type: ignore[misc,assignment]
        try:
            schedule_cleanup(p)
        finally:
            threading.Timer = orig_timer  # type: ignore[misc]
            p.unlink(missing_ok=True)

        self.assertEqual(captured_delay, [60.0])

class TestBuildDockerCmd(unittest.TestCase):
    def test_creds_volume_mount_without_selinux(self) -> None:
        """build_volumes should include creds temp mount when provided."""
        fd, tmp_path = tempfile.mkstemp()
        os.close(fd)
        try:
            creds = Path(tmp_path)
            with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False):
                vols = build_volumes(creds)
            mount = f"{creds}:/mnt/claude-credentials.json:ro"
            self.assertIn(mount, vols)
        finally:
            os.unlink(tmp_path)

    def test_creds_volume_mount_with_selinux(self) -> None:
        """build_volumes should include creds temp mount with :ro,z when SELinux is active."""
        fd, tmp_path = tempfile.mkstemp()
        os.close(fd)
        try:
            creds = Path(tmp_path)
            with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=True):
                vols = build_volumes(creds)
            mount = f"{creds}:/mnt/claude-credentials.json:ro,z"
            self.assertIn(mount, vols)
        finally:
            os.unlink(tmp_path)

class TestKeychainServiceName(unittest.TestCase):
    def test_default_claude_dir(self) -> None:
        """default ~/.claude returns base service name without suffix."""
        self.assertEqual(keychain_service_name(Path.home() / ".claude"), "Claude Code-credentials")

    def test_custom_dir_returns_suffixed_name(self) -> None:
        """non-default path returns service name with sha256 suffix."""
        name = keychain_service_name(Path.home() / ".claude2")
        self.assertTrue(name.startswith("Claude Code-credentials-"))
        suffix = name.removeprefix("Claude Code-credentials-")
        self.assertEqual(len(suffix), 8)
        # verify it's a valid hex string
        int(suffix, 16)

    def test_same_path_same_suffix(self) -> None:
        """same path always produces the same suffix."""
        p = Path("/tmp/test-claude-config")
        self.assertEqual(keychain_service_name(p), keychain_service_name(p))

    def test_different_paths_different_suffixes(self) -> None:
        """different paths produce different suffixes."""
        name1 = keychain_service_name(Path("/tmp/claude-a"))
        name2 = keychain_service_name(Path("/tmp/claude-b"))
        self.assertNotEqual(name1, name2)

    def test_tilde_path_expansion(self) -> None:
        """tilde path ~/.claude is expanded and recognized as default."""
        self.assertEqual(keychain_service_name(Path("~/.claude")), "Claude Code-credentials")

class TestBuildVolumesClaudeHome(unittest.TestCase):
    def test_custom_claude_home_mount_without_selinux(self) -> None:
        """build_volumes with custom claude_home mounts that dir to /mnt/claude:ro."""
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            custom = tmp / "my-claude"
            custom.mkdir()
            with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False):
                vols = build_volumes(None, claude_home=custom)
            mount = f"{custom}:/mnt/claude:ro"
            self.assertIn(mount, vols)
        finally:
            shutil.rmtree(tmp)

    def test_custom_claude_home_mount_with_selinux(self) -> None:
        """build_volumes with custom claude_home mounts that dir to /mnt/claude:ro,z."""
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            custom = tmp / "my-claude"
            custom.mkdir()
            with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=True):
                vols = build_volumes(None, claude_home=custom)
            mount = f"{custom}:/mnt/claude:ro,z"
            self.assertIn(mount, vols)
        finally:
            shutil.rmtree(tmp)

    def test_default_claude_home_when_none(self) -> None:
        """build_volumes with claude_home=None defaults to ~/.claude."""
        with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False):
            vols = build_volumes(None)
        found = any("/mnt/claude:ro" in v for v in vols)
        self.assertTrue(found, "should mount default claude dir to /mnt/claude:ro")

class TestExtractCredentialsClaudeHome(unittest.TestCase):
    def test_skips_when_credentials_exist_on_darwin(self) -> None:
        """extract_macos_credentials returns None when .credentials.json exists in claude_home."""
        if platform.system() != "Darwin":
            return  # only testable on macOS
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            (tmp / ".credentials.json").write_text('{"token": "test"}')
            self.assertIsNone(extract_macos_credentials(tmp))
        finally:
            shutil.rmtree(tmp)

    def test_returns_none_on_non_darwin(self) -> None:
        """extract_macos_credentials returns None on non-Darwin regardless of claude_home."""
        if platform.system() == "Darwin":
            return  # skip on macOS
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            self.assertIsNone(extract_macos_credentials(tmp))
        finally:
            shutil.rmtree(tmp)

class TestSelinuxEnabled(unittest.TestCase):
    def test_returns_false_on_non_linux(self) -> None:
        """selinux_enabled returns False on non-Linux."""
        with unittest.mock.patch("ralphex_dk.platform") as mock_platform:
            mock_platform.system.return_value = "Darwin"
            self.assertFalse(selinux_enabled())

    def test_returns_false_when_enforce_missing(self) -> None:
        """selinux_enabled returns False when /sys/fs/selinux/enforce does not exist."""
        with unittest.mock.patch("ralphex_dk.platform") as mock_platform, \
             unittest.mock.patch("ralphex_dk.Path") as mock_path:
            mock_platform.system.return_value = "Linux"
            mock_path.return_value.exists.return_value = False
            self.assertFalse(selinux_enabled())

    def test_returns_true_when_enforce_exists(self) -> None:
        """selinux_enabled returns True when /sys/fs/selinux/enforce exists."""
        with unittest.mock.patch("ralphex_dk.platform") as mock_platform, \
             unittest.mock.patch("ralphex_dk.Path") as mock_path:
            mock_platform.system.return_value = "Linux"
            mock_path.return_value.exists.return_value = True
            self.assertTrue(selinux_enabled())

class TestSelinuxVolumeSuffix(unittest.TestCase):
    def test_z_label_in_volumes_when_selinux(self) -> None:
        """volume mounts include :z label when SELinux is enabled."""
        with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=True):
            vols = build_volumes(None)
        for i in range(1, len(vols), 2):
            has_z = vols[i].endswith(":z") or ",z" in vols[i]
            self.assertTrue(has_z, f"volume {vols[i]} missing :z SELinux label")

    def test_no_z_label_without_selinux(self) -> None:
        """volume mounts omit :z label when SELinux is not enabled."""
        with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False):
            vols = build_volumes(None)
        for i in range(1, len(vols), 2):
            self.assertNotIn(",z", vols[i])
            self.assertFalse(vols[i].endswith(":z"),
                             f"volume {vols[i]} should not have :z without SELinux")

class TestClaudeConfigDirEnv(unittest.TestCase):
    def test_env_sets_claude_home(self) -> None:
        """CLAUDE_CONFIG_DIR env var selects alternate claude directory."""
        tmp = Path(tempfile.mkdtemp()).resolve()
        try:
            custom = tmp / "my-claude"
            custom.mkdir()
            old = os.environ.get("CLAUDE_CONFIG_DIR")
            os.environ["CLAUDE_CONFIG_DIR"] = str(custom)
            try:
                env_val = os.environ.get("CLAUDE_CONFIG_DIR", "")
                self.assertTrue(env_val)
                result = Path(env_val).expanduser().resolve()
                self.assertEqual(result, custom)
            finally:
                if old is None:
                    os.environ.pop("CLAUDE_CONFIG_DIR", None)
                else:
                    os.environ["CLAUDE_CONFIG_DIR"] = old
        finally:
            shutil.rmtree(tmp)

    def test_empty_env_defaults_to_dot_claude(self) -> None:
        """empty CLAUDE_CONFIG_DIR falls back to ~/.claude in build_volumes."""
        old = os.environ.get("CLAUDE_CONFIG_DIR")
        os.environ.pop("CLAUDE_CONFIG_DIR", None)
        try:
            # when CLAUDE_CONFIG_DIR is unset, build_volumes uses ~/.claude as default
            with unittest.mock.patch("ralphex_dk.selinux_enabled", return_value=False):
                vols = build_volumes(None, claude_home=None)
            # the first volume mount should map ~/.claude -> /mnt/claude
            default_claude = str((Path.home() / ".claude").resolve())
            vol_sources = [v.split(":")[0] for v in vols if v.startswith("/")]
            self.assertIn(default_claude, vol_sources,
                          f"default ~/.claude path not found in volume mounts: {vols}")
        finally:
            if old is not None:
                os.environ["CLAUDE_CONFIG_DIR"] = old

    def test_tilde_expansion(self) -> None:
        """CLAUDE_CONFIG_DIR with ~ is expanded correctly."""
        old = os.environ.get("CLAUDE_CONFIG_DIR")
        os.environ["CLAUDE_CONFIG_DIR"] = "~/.claude-test"
        try:
            env_val = os.environ.get("CLAUDE_CONFIG_DIR", "")
            result = Path(env_val).expanduser().resolve()
            expected = (Path.home() / ".claude-test").resolve()
            self.assertEqual(result, expected)
        finally:
            if old is None:
                os.environ.pop("CLAUDE_CONFIG_DIR", None)
            else:
                os.environ["CLAUDE_CONFIG_DIR"] = old

# note: TestExtraVolumes removed - RALPHEX_EXTRA_VOLUMES is now handled by
# merge_volume_flags() in main(), tested by TestMergeVolumeFlags class.

class TestIsSensitiveName(unittest.TestCase):
    def test_matches_sensitive_patterns(self) -> None:
        """names containing KEY, SECRET, TOKEN etc. are sensitive."""
        self.assertTrue(is_sensitive_name("API_KEY"))
        self.assertTrue(is_sensitive_name("SECRET_TOKEN"))
        self.assertTrue(is_sensitive_name("MY_PASSWORD"))
        self.assertTrue(is_sensitive_name("PASSWD"))
        self.assertTrue(is_sensitive_name("DB_CREDENTIAL"))
        self.assertTrue(is_sensitive_name("AUTH_TOKEN"))

    def test_case_insensitivity(self) -> None:
        """matching is case insensitive."""
        self.assertTrue(is_sensitive_name("api_key"))
        self.assertTrue(is_sensitive_name("API_KEY"))
        self.assertTrue(is_sensitive_name("Api_Key"))
        self.assertTrue(is_sensitive_name("secret"))
        self.assertTrue(is_sensitive_name("SECRET"))

    def test_non_sensitive_names(self) -> None:
        """names without sensitive patterns return False."""
        self.assertFalse(is_sensitive_name("DEBUG"))
        self.assertFalse(is_sensitive_name("LOG_LEVEL"))
        self.assertFalse(is_sensitive_name("PORT"))
        self.assertFalse(is_sensitive_name("HOME"))
        self.assertFalse(is_sensitive_name("PATH"))

    def test_partial_matches_at_word_boundary(self) -> None:
        """substring matches at word boundaries are sensitive."""
        self.assertTrue(is_sensitive_name("MY_API_KEY"))
        self.assertTrue(is_sensitive_name("SECRET_VALUE"))
        self.assertTrue(is_sensitive_name("USER_TOKEN_ID"))

    def test_later_occurrence_matches(self) -> None:
        """pattern at later position in string is still detected."""
        # MONKEY_API_KEY: first KEY in MONKEY is not at boundary, but _KEY at end is
        self.assertTrue(is_sensitive_name("MONKEY_API_KEY"))
        self.assertTrue(is_sensitive_name("KEY_MONKEY_KEY"))  # KEY at start and end
        self.assertTrue(is_sensitive_name("XSECRET_TOKEN"))  # SECRET not at boundary, but TOKEN is

    def test_no_match_without_word_boundary(self) -> None:
        """substring without word boundary is not sensitive."""
        self.assertFalse(is_sensitive_name("MONKEY"))  # KEY is substring but not at boundary
        self.assertFalse(is_sensitive_name("BUCKET"))  # no sensitive pattern
        self.assertFalse(is_sensitive_name("AUTHENTICATE"))  # AUTH not at word boundary (no _ before/after)
        self.assertFalse(is_sensitive_name("AUTHX"))  # AUTH at start but no right boundary
        self.assertFalse(is_sensitive_name("XAUTH"))  # AUTH at end but no left boundary

class TestBuildEnvVars(EnvTestCase):
    env_vars = ["RALPHEX_EXTRA_ENV"]

    def test_extra_env_with_explicit_values(self) -> None:
        """RALPHEX_EXTRA_ENV with explicit values builds -e flags."""
        os.environ["RALPHEX_EXTRA_ENV"] = "FOO=bar,BAZ=qux"
        env_vars = build_env_vars()
        self.assertEqual(env_vars, ["-e", "FOO=bar", "-e", "BAZ=qux"])

    def test_name_only_inherits_from_host(self) -> None:
        """RALPHEX_EXTRA_ENV with name-only entries inherit from host."""
        os.environ["RALPHEX_EXTRA_ENV"] = "FOO,BAR"
        env_vars = build_env_vars()
        self.assertEqual(env_vars, ["-e", "FOO", "-e", "BAR"])

    def test_comma_separation_and_whitespace_trimming(self) -> None:
        """entries are split by comma and whitespace is trimmed."""
        os.environ["RALPHEX_EXTRA_ENV"] = "FOO=bar , BAZ , QUUX=123"
        env_vars = build_env_vars()
        self.assertEqual(env_vars, ["-e", "FOO=bar", "-e", "BAZ", "-e", "QUUX=123"])

    def test_invalid_entries_skipped(self) -> None:
        """entries with invalid var names are silently skipped."""
        os.environ["RALPHEX_EXTRA_ENV"] = "123BAD,FOO=bar,-invalid,GOOD"
        env_vars = build_env_vars()
        self.assertEqual(env_vars, ["-e", "FOO=bar", "-e", "GOOD"])

    def test_empty_env_var_is_noop(self) -> None:
        """empty or unset RALPHEX_EXTRA_ENV returns empty list."""
        env_vars = build_env_vars()
        self.assertEqual(env_vars, [])
        os.environ["RALPHEX_EXTRA_ENV"] = ""
        env_vars = build_env_vars()
        self.assertEqual(env_vars, [])

    def test_sensitive_name_warning(self) -> None:
        """sensitive name with explicit value prints warning to stderr."""
        os.environ["RALPHEX_EXTRA_ENV"] = "API_KEY=secret"
        import io
        captured = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured):
            env_vars = build_env_vars()
        self.assertEqual(env_vars, ["-e", "API_KEY=secret"])
        warning = captured.getvalue()
        self.assertIn("warning:", warning)
        self.assertIn("API_KEY", warning)
        self.assertIn("-E API_KEY", warning)

    def test_sensitive_name_no_warning_for_name_only(self) -> None:
        """sensitive name without explicit value does not print warning."""
        os.environ["RALPHEX_EXTRA_ENV"] = "API_KEY"
        import io
        captured = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured):
            env_vars = build_env_vars()
        self.assertEqual(env_vars, ["-e", "API_KEY"])
        warning = captured.getvalue()
        self.assertEqual(warning, "")

class TestMergeEnvFlags(EnvTestCase):
    env_vars = ["RALPHEX_EXTRA_ENV"]

    def test_env_only(self) -> None:
        """with only env var set, returns env var entries."""
        os.environ["RALPHEX_EXTRA_ENV"] = "FOO=bar,BAZ"
        result = merge_env_flags([])
        self.assertEqual(result, ["-e", "FOO=bar", "-e", "BAZ"])

    def test_cli_only(self) -> None:
        """with only CLI args, returns CLI entries."""
        result = merge_env_flags(["FOO=bar", "BAZ"])
        self.assertEqual(result, ["-e", "FOO=bar", "-e", "BAZ"])

    def test_env_then_cli(self) -> None:
        """env var entries come first, CLI entries append."""
        os.environ["RALPHEX_EXTRA_ENV"] = "ENV1=a,ENV2"
        result = merge_env_flags(["CLI1=b", "CLI2"])
        self.assertEqual(result, ["-e", "ENV1=a", "-e", "ENV2", "-e", "CLI1=b", "-e", "CLI2"])

    def test_invalid_cli_entries_skipped(self) -> None:
        """invalid CLI entries are skipped with warning."""
        import io
        captured = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured):
            result = merge_env_flags(["=invalid", "VALID=val", "123BAD"])
        self.assertEqual(result, ["-e", "VALID=val"])
        warning = captured.getvalue()
        self.assertIn("=invalid", warning)
        self.assertIn("123BAD", warning)

    def test_sensitive_name_warning_for_cli(self) -> None:
        """sensitive name with explicit value in CLI prints warning."""
        import io
        captured = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured):
            result = merge_env_flags(["API_KEY=secret"])
        self.assertEqual(result, ["-e", "API_KEY=secret"])
        warning = captured.getvalue()
        self.assertIn("API_KEY", warning)

    def test_empty_both(self) -> None:
        """with no env var and no CLI args, returns empty list."""
        result = merge_env_flags([])
        self.assertEqual(result, [])

class TestMergeVolumeFlags(EnvTestCase):
    env_vars = ["RALPHEX_EXTRA_VOLUMES"]

    def test_env_only(self) -> None:
        """with only env var set, returns env var entries."""
        os.environ["RALPHEX_EXTRA_VOLUMES"] = "/a:/b,/c:/d:ro"
        result = merge_volume_flags([])
        self.assertEqual(result, ["-v", "/a:/b", "-v", "/c:/d:ro"])

    def test_cli_only(self) -> None:
        """with only CLI args, returns CLI entries."""
        result = merge_volume_flags(["/a:/b", "/c:/d:ro"])
        self.assertEqual(result, ["-v", "/a:/b", "-v", "/c:/d:ro"])

    def test_env_then_cli(self) -> None:
        """env var entries come first, CLI entries append."""
        os.environ["RALPHEX_EXTRA_VOLUMES"] = "/env1:/mnt/env1"
        result = merge_volume_flags(["/cli1:/mnt/cli1", "/cli2:/mnt/cli2:ro"])
        self.assertEqual(result, ["-v", "/env1:/mnt/env1", "-v", "/cli1:/mnt/cli1", "-v", "/cli2:/mnt/cli2:ro"])

    def test_invalid_env_entries_skipped(self) -> None:
        """env var entries without ':' are silently skipped."""
        os.environ["RALPHEX_EXTRA_VOLUMES"] = "badentry,/ok:/mnt/ok"
        result = merge_volume_flags([])
        self.assertEqual(result, ["-v", "/ok:/mnt/ok"])

    def test_invalid_cli_entries_skipped(self) -> None:
        """CLI entries without ':' are silently skipped."""
        result = merge_volume_flags(["badentry", "/ok:/mnt/ok"])
        self.assertEqual(result, ["-v", "/ok:/mnt/ok"])

    def test_empty_both(self) -> None:
        """with no env var and no CLI args, returns empty list."""
        result = merge_volume_flags([])
        self.assertEqual(result, [])

    def test_whitespace_trimmed(self) -> None:
        """whitespace in env var entries is trimmed."""
        os.environ["RALPHEX_EXTRA_VOLUMES"] = "  /a:/b  ,  /c:/d  "
        result = merge_volume_flags([])
        self.assertEqual(result, ["-v", "/a:/b", "-v", "/c:/d"])

class TestBuildParser(unittest.TestCase):
    def test_returns_argument_parser(self) -> None:
        """build_parser returns an ArgumentParser instance."""
        parser = build_parser()
        self.assertIsInstance(parser, argparse.ArgumentParser)

    def test_env_flag_short(self) -> None:
        """-E flag is parsed correctly."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["-E", "FOO=bar"])
        self.assertEqual(args.env, ["FOO=bar"])

    def test_env_flag_long(self) -> None:
        """--env flag is parsed correctly."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["--env", "FOO=bar"])
        self.assertEqual(args.env, ["FOO=bar"])

    def test_env_flag_multiple(self) -> None:
        """multiple -E flags accumulate."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["-E", "FOO=bar", "-E", "BAZ"])
        self.assertEqual(args.env, ["FOO=bar", "BAZ"])

    def test_volume_flag_short(self) -> None:
        """-v flag is parsed correctly."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["-v", "/a:/b"])
        self.assertEqual(args.volume, ["/a:/b"])

    def test_volume_flag_long(self) -> None:
        """--volume flag is parsed correctly."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["--volume", "/a:/b:ro"])
        self.assertEqual(args.volume, ["/a:/b:ro"])

    def test_volume_flag_multiple(self) -> None:
        """multiple -v flags accumulate."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["-v", "/a:/b", "-v", "/c:/d"])
        self.assertEqual(args.volume, ["/a:/b", "/c:/d"])

    def test_update_flag(self) -> None:
        """--update flag is store_true."""
        parser = build_parser()
        args, _ = parser.parse_known_args(["--update"])
        self.assertTrue(args.update)

    def test_update_script_flag(self) -> None:
        """--update-script flag is store_true."""
        parser = build_parser()
        args, _ = parser.parse_known_args(["--update-script"])
        self.assertTrue(args.update_script)

    def test_test_flag(self) -> None:
        """--test flag is store_true."""
        parser = build_parser()
        args, _ = parser.parse_known_args(["--test"])
        self.assertTrue(args.test)

    def test_help_flag(self) -> None:
        """-h/--help flag is store_true (custom handling)."""
        parser = build_parser()
        args, _ = parser.parse_known_args(["-h"])
        self.assertTrue(args.help)
        args, _ = parser.parse_known_args(["--help"])
        self.assertTrue(args.help)

    def test_unknown_args_pass_through(self) -> None:
        """unknown args (ralphex args) are returned in second tuple element."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["--serve", "plan.md", "--review"])
        self.assertEqual(unknown, ["--serve", "plan.md", "--review"])
        self.assertEqual(args.env, [])
        self.assertEqual(args.volume, [])

    def test_mixed_known_and_unknown(self) -> None:
        """known and unknown args are separated correctly."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["-E", "FOO=bar", "--serve", "-v", "/a:/b", "plan.md"])
        self.assertEqual(args.env, ["FOO=bar"])
        self.assertEqual(args.volume, ["/a:/b"])
        self.assertEqual(unknown, ["--serve", "plan.md"])

    def test_double_dash_delimiter(self) -> None:
        """args after -- are NOT consumed by wrapper (-- is preserved in pass-through)."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["-E", "FOO", "--", "-v", "/ignored", "plan.md"])
        self.assertEqual(args.env, ["FOO"])
        self.assertEqual(args.volume, [])
        # note: -- is preserved and passed through to ralphex along with remaining args
        self.assertEqual(unknown, ["--", "-v", "/ignored", "plan.md"])

    def test_lowercase_e_passes_through(self) -> None:
        """-e (lowercase) is not consumed by wrapper, passes to ralphex."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["-e", "plan.md"])
        self.assertEqual(args.env, [])
        self.assertEqual(unknown, ["-e", "plan.md"])

    def test_e_at_end_without_value_raises_error(self) -> None:
        """-E at end without value raises argparse error."""
        parser = build_parser()
        with self.assertRaises(SystemExit):
            import io
            with unittest.mock.patch("sys.stderr", io.StringIO()):
                parser.parse_known_args(["-E"])

    def test_v_at_end_without_value_raises_error(self) -> None:
        """-v at end without value raises argparse error."""
        parser = build_parser()
        with self.assertRaises(SystemExit):
            import io
            with unittest.mock.patch("sys.stderr", io.StringIO()):
                parser.parse_known_args(["-v"])

    def test_defaults_when_no_args(self) -> None:
        """all flags have sensible defaults when no args provided."""
        parser = build_parser()
        args, unknown = parser.parse_known_args([])
        self.assertEqual(args.env, [])
        self.assertEqual(args.volume, [])
        self.assertFalse(args.update)
        self.assertFalse(args.update_script)
        self.assertFalse(args.test)
        self.assertFalse(args.help)
        self.assertEqual(unknown, [])

    def test_abbreviations_disabled(self) -> None:
        """flag abbreviations are disabled to preserve pass-through semantics."""
        parser = build_parser()
        # --te should NOT match --test (abbreviation), should pass through to ralphex
        args, unknown = parser.parse_known_args(["--te"])
        self.assertFalse(args.test)
        self.assertEqual(unknown, ["--te"])
        # --up should NOT fail as ambiguous, should pass through to ralphex
        args, unknown = parser.parse_known_args(["--up"])
        self.assertFalse(args.update)
        self.assertFalse(args.update_script)
        self.assertEqual(unknown, ["--up"])

    def test_image_flag(self) -> None:
        """--image flag is parsed correctly."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["--image", "my/img:tag"])
        self.assertEqual(args.image, "my/img:tag")
        self.assertEqual(unknown, [])

    def test_image_flag_default_none(self) -> None:
        """--image default is None (so env/default fallback can apply)."""
        parser = build_parser()
        args, _ = parser.parse_known_args([])
        self.assertIsNone(args.image)

    def test_port_flag(self) -> None:
        """--port flag is parsed correctly."""
        parser = build_parser()
        args, unknown = parser.parse_known_args(["--port", "9090"])
        self.assertEqual(args.port, "9090")
        self.assertEqual(unknown, [])

    def test_port_flag_default_none(self) -> None:
        """--port default is None (so env/default fallback can apply)."""
        parser = build_parser()
        args, _ = parser.parse_known_args([])
        self.assertIsNone(args.port)

class TestMainArgparse(EnvTestCase):
    """tests for main() argparse integration."""
    env_vars = ["RALPHEX_IMAGE", "RALPHEX_PORT", "RALPHEX_EXTRA_ENV",
                "RALPHEX_EXTRA_VOLUMES", "CLAUDE_CONFIG_DIR"]
    save_argv = True

    def test_update_flag_triggers_handle_update(self) -> None:
        """--update calls handle_update with image."""
        calls: list[str] = []
        with unittest.mock.patch("ralphex_dk.handle_update", side_effect=lambda img: (calls.append(img), 0)[1]):
            sys.argv = ["ralphex-dk", "--update"]
            result = main()
        self.assertEqual(calls, [DEFAULT_IMAGE])
        self.assertEqual(result, 0)

    def test_update_with_custom_image(self) -> None:
        """--update uses RALPHEX_IMAGE env var."""
        os.environ["RALPHEX_IMAGE"] = "custom:latest"
        calls: list[str] = []
        with unittest.mock.patch("ralphex_dk.handle_update", side_effect=lambda img: (calls.append(img), 0)[1]):
            sys.argv = ["ralphex-dk", "--update"]
            result = main()
        self.assertEqual(calls, ["custom:latest"])
        self.assertEqual(result, 0)

    def test_update_script_flag_triggers_handle_update_script(self) -> None:
        """--update-script calls handle_update_script."""
        calls: list[Path] = []
        with unittest.mock.patch("ralphex_dk.handle_update_script", side_effect=lambda p: (calls.append(p), 0)[1]):
            sys.argv = ["ralphex-dk", "--update-script"]
            result = main()
        self.assertEqual(len(calls), 1)
        self.assertEqual(result, 0)

    def test_env_flags_build_cli_env(self) -> None:
        """CLI -E/--env flags are converted to docker -e flags."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            captured_env: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_env.extend(env_vars)
                return 0

            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    sys.argv = ["ralphex-dk", "-E", "FOO=bar", "--env", "BAZ", "plan.md"]
                    result = main()

            self.assertEqual(result, 0)
            self.assertIn("-e", captured_env)
            self.assertIn("FOO=bar", captured_env)
            self.assertIn("BAZ", captured_env)
        finally:
            shutil.rmtree(tmp)

    def test_volume_flags_build_cli_volumes(self) -> None:
        """CLI -v/--volume flags are added to volume list."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            captured_volumes: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_volumes.extend(volumes)
                return 0

            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    sys.argv = ["ralphex-dk", "-v", "/a:/b", "--volume", "/c:/d:ro", "plan.md"]
                    result = main()

            self.assertEqual(result, 0)
            self.assertIn("-v", captured_volumes)
            self.assertIn("/a:/b", captured_volumes)
            self.assertIn("/c:/d:ro", captured_volumes)
        finally:
            shutil.rmtree(tmp)

    def test_ralphex_args_pass_through(self) -> None:
        """unknown args pass through to run_docker."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            captured_args: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_args.extend(args)
                return 0

            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    sys.argv = ["ralphex-dk", "--serve", "plan.md", "--review"]
                    result = main()

            self.assertEqual(result, 0)
            self.assertEqual(captured_args, ["--serve", "plan.md", "--review"])
        finally:
            shutil.rmtree(tmp)

    def test_double_dash_delimiter_pass_through(self) -> None:
        """args after -- pass through unchanged to ralphex, including -- itself."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            captured_args: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_args.extend(args)
                return 0

            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    # -E FOO is consumed, but -v after -- is NOT consumed
                    sys.argv = ["ralphex-dk", "-E", "FOO", "--", "-v", "/ignored", "plan.md"]
                    result = main()

            self.assertEqual(result, 0)
            # -- and everything after it passes through
            self.assertEqual(captured_args, ["--", "-v", "/ignored", "plan.md"])
        finally:
            shutil.rmtree(tmp)

    def test_lowercase_e_passes_to_ralphex(self) -> None:
        """-e (ralphex's external-only flag) passes through to ralphex."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            captured_args: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_args.extend(args)
                return 0

            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    sys.argv = ["ralphex-dk", "-e", "plan.md"]
                    result = main()

            self.assertEqual(result, 0)
            self.assertEqual(captured_args, ["-e", "plan.md"])
        finally:
            shutil.rmtree(tmp)

    def test_mixed_wrapper_and_ralphex_args(self) -> None:
        """wrapper args are separated from ralphex args correctly."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            captured_args: list[str] = []
            captured_env: list[str] = []
            captured_volumes: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_args.extend(args)
                captured_env.extend(env_vars)
                captured_volumes.extend(volumes)
                return 0

            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    sys.argv = ["ralphex-dk", "-E", "DEBUG=1", "--serve", "-v", "/data:/mnt", "plan.md", "-e"]
                    result = main()

            self.assertEqual(result, 0)
            # wrapper args consumed
            self.assertIn("DEBUG=1", captured_env)
            self.assertIn("/data:/mnt", captured_volumes)
            # ralphex args passed through
            self.assertEqual(captured_args, ["--serve", "plan.md", "-e"])
        finally:
            shutil.rmtree(tmp)

    def test_invalid_env_entries_skipped_with_warning(self) -> None:
        """invalid -E entries are skipped with warning."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            captured_env: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_env.extend(env_vars)
                return 0

            import io
            captured_stderr = io.StringIO()
            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                        sys.argv = ["ralphex-dk", "-E", "=invalid", "-E", "VALID=val"]
                        result = main()

            self.assertEqual(result, 0)
            # only valid entry is included
            self.assertIn("VALID=val", captured_env)
            self.assertNotIn("=invalid", captured_env)
            # warning printed
            warning = captured_stderr.getvalue()
            self.assertIn("=invalid", warning)
        finally:
            shutil.rmtree(tmp)

    def _run_main_capturing_image_port(self, argv: list[str]) -> tuple[str, str]:
        """run main() with claude dir set up and capture resolved image/port."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            captured: dict[str, str] = {}

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured["image"] = image
                captured["port"] = port
                return 0

            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    sys.argv = argv
                    result = main()
            self.assertEqual(result, 0)
            return captured["image"], captured["port"]
        finally:
            shutil.rmtree(tmp)

    def test_image_port_defaults(self) -> None:
        """without flag or env, image and port fall back to defaults."""
        image, port = self._run_main_capturing_image_port(["ralphex-dk", "plan.md"])
        self.assertEqual(image, DEFAULT_IMAGE)
        self.assertEqual(port, DEFAULT_PORT)

    def test_image_port_from_env(self) -> None:
        """env vars provide image and port when CLI flags absent."""
        os.environ["RALPHEX_IMAGE"] = "env-image:tag"
        os.environ["RALPHEX_PORT"] = "7070"
        image, port = self._run_main_capturing_image_port(["ralphex-dk", "plan.md"])
        self.assertEqual(image, "env-image:tag")
        self.assertEqual(port, "7070")

    def test_image_port_cli_flags(self) -> None:
        """CLI flags set image and port."""
        image, port = self._run_main_capturing_image_port(
            ["ralphex-dk", "--image", "cli-image:tag", "--port", "9090", "plan.md"],
        )
        self.assertEqual(image, "cli-image:tag")
        self.assertEqual(port, "9090")

    def test_image_port_cli_overrides_env(self) -> None:
        """CLI flags take precedence over env vars."""
        os.environ["RALPHEX_IMAGE"] = "env-image:tag"
        os.environ["RALPHEX_PORT"] = "7070"
        image, port = self._run_main_capturing_image_port(
            ["ralphex-dk", "--image", "cli-image:tag", "--port", "9090", "plan.md"],
        )
        self.assertEqual(image, "cli-image:tag")
        self.assertEqual(port, "9090")

class TestHelpFlag(EnvTestCase):
    """tests for --help flag handling."""
    env_vars = ["RALPHEX_IMAGE", "CLAUDE_CONFIG_DIR", "RALPHEX_CLAUDE_PROVIDER"]
    save_argv = True

    def test_help_without_claude_config_shows_wrapper_help(self) -> None:
        """--help shows wrapper help even when claude config is missing."""
        os.environ["CLAUDE_CONFIG_DIR"] = "/tmp/nonexistent-dir-12345"

        captured_output: list[str] = []

        def fake_print(*args: object, **kwargs: object) -> None:
            out = " ".join(str(a) for a in args)
            captured_output.append(out)

        with unittest.mock.patch("builtins.print", side_effect=fake_print):
            sys.argv = ["ralphex-dk", "--help"]
            result = main()

        self.assertEqual(result, 0)
        output = "\n".join(captured_output)
        self.assertIn("ralphex options: (cannot show - claude config not found)", output)
        self.assertIn("run 'claude' first to authenticate", output)

    def test_help_with_claude_config_runs_container(self) -> None:
        """--help with valid claude config runs container for ralphex help."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            docker_calls: list[list[str]] = []

            def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
                mock_result = unittest.mock.Mock()
                mock_result.returncode = 0
                mock_result.stdout = ""  # default for git commands
                if cmd and cmd[0] == "docker":
                    docker_calls.append(cmd)
                return mock_result

            with unittest.mock.patch("subprocess.run", side_effect=fake_run):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    sys.argv = ["ralphex-dk", "--help"]
                    result = main()

            self.assertEqual(result, 0)
            # should have called docker run with --help
            self.assertEqual(len(docker_calls), 1)
            cmd = docker_calls[0]
            self.assertEqual(cmd[0], "docker")
            self.assertEqual(cmd[1], "run")
            self.assertIn("--help", cmd)
        finally:
            shutil.rmtree(tmp)

    def test_h_flag_same_as_help(self) -> None:
        """-h (short form) behaves same as --help."""
        os.environ["CLAUDE_CONFIG_DIR"] = "/tmp/nonexistent-dir-12345"

        captured_output: list[str] = []

        def fake_print(*args: object, **kwargs: object) -> None:
            out = " ".join(str(a) for a in args)
            captured_output.append(out)

        with unittest.mock.patch("builtins.print", side_effect=fake_print):
            sys.argv = ["ralphex-dk", "-h"]
            result = main()

        self.assertEqual(result, 0)
        output = "\n".join(captured_output)
        self.assertIn("cannot show - claude config not found", output)

    def test_help_returns_container_exit_code(self) -> None:
        """main() returns exit code from container's --help."""
        tmp = Path(tempfile.mkdtemp())
        try:
            claude_dir = tmp / ".claude"
            claude_dir.mkdir()
            (claude_dir / ".credentials.json").write_text("{}")
            os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

            def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
                mock_result = unittest.mock.Mock()
                mock_result.stdout = ""  # default for git commands
                if cmd and cmd[0] == "docker":
                    mock_result.returncode = 42  # docker run exit code
                else:
                    mock_result.returncode = 0  # git commands succeed
                return mock_result

            with unittest.mock.patch("subprocess.run", side_effect=fake_run):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    sys.argv = ["ralphex-dk", "--help"]
                    result = main()

            self.assertEqual(result, 42)
        finally:
            shutil.rmtree(tmp)

    def test_help_with_env_flags_still_shows_help(self) -> None:
        """wrapper flags (-E, -v) before --help are parsed but help takes precedence."""
        os.environ["CLAUDE_CONFIG_DIR"] = "/tmp/nonexistent-dir-12345"

        captured_output: list[str] = []

        def fake_print(*args: object, **kwargs: object) -> None:
            out = " ".join(str(a) for a in args)
            captured_output.append(out)

        with unittest.mock.patch("builtins.print", side_effect=fake_print):
            # -E and -v are parsed, but --help wins and run_docker is never called
            sys.argv = ["ralphex-dk", "-E", "FOO=bar", "-v", "/a:/b", "--help"]
            result = main()

        self.assertEqual(result, 0)
        output = "\n".join(captured_output)
        self.assertIn("cannot show - claude config not found", output)

class TestClaudeProvider(EnvTestCase):
    """tests for claude provider selection and bedrock env var handling."""
    env_vars = ["RALPHEX_CLAUDE_PROVIDER"] + BEDROCK_ENV_VARS

    def test_default_provider_no_bedrock_env(self) -> None:
        """no flag, no env → provider is 'default', bedrock args only has USE_BEDROCK=1."""
        provider = get_claude_provider(None)
        self.assertEqual(provider, "default")
        # build_bedrock_env_args always sets CLAUDE_CODE_USE_BEDROCK=1
        args = build_bedrock_env_args()
        self.assertEqual(args, ["-e", "CLAUDE_CODE_USE_BEDROCK=1"])

    def test_cli_flag_bedrock(self) -> None:
        """--claude-provider bedrock → provider is 'bedrock'."""
        provider = get_claude_provider("bedrock")
        self.assertEqual(provider, "bedrock")

    def test_env_var_fallback(self) -> None:
        """no flag, RALPHEX_CLAUDE_PROVIDER=bedrock → provider is 'bedrock'."""
        os.environ["RALPHEX_CLAUDE_PROVIDER"] = "bedrock"
        provider = get_claude_provider(None)
        self.assertEqual(provider, "bedrock")

    def test_cli_overrides_env(self) -> None:
        """flag and env var set → CLI wins."""
        os.environ["RALPHEX_CLAUDE_PROVIDER"] = "bedrock"
        provider = get_claude_provider("default")
        self.assertEqual(provider, "default")
        # also test the reverse
        os.environ["RALPHEX_CLAUDE_PROVIDER"] = "default"
        provider = get_claude_provider("bedrock")
        self.assertEqual(provider, "bedrock")

    def test_bedrock_passes_set_vars(self) -> None:
        """only passes BEDROCK_ENV_VARS that are actually set."""
        os.environ["AWS_REGION"] = "us-east-1"
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
        # AWS_SECRET_ACCESS_KEY is NOT set
        args = build_bedrock_env_args()
        self.assertIn("-e", args)
        self.assertIn("AWS_REGION", args)
        self.assertIn("AWS_ACCESS_KEY_ID", args)
        self.assertNotIn("AWS_SECRET_ACCESS_KEY", args)

    def test_bedrock_skips_user_overrides(self) -> None:
        """skips vars already set via -E flags to avoid overriding user values."""
        os.environ["AWS_REGION"] = "us-east-1"
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
        # user explicitly sets AWS_REGION via -E flag
        existing_env = ["-e", "AWS_REGION=eu-west-1"]
        args = build_bedrock_env_args(existing_env)
        # AWS_REGION should be skipped (user override), AWS_ACCESS_KEY_ID should be added
        self.assertNotIn("AWS_REGION", args)
        self.assertIn("AWS_ACCESS_KEY_ID", args)

    def test_bedrock_skips_user_inherit(self) -> None:
        """skips vars already set via -E VAR (inherit form) to avoid duplicates."""
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
        os.environ["AWS_SECRET_ACCESS_KEY"] = "secret"
        # user explicitly passes AWS_ACCESS_KEY_ID via -E flag (inherit form)
        existing_env = ["-e", "AWS_ACCESS_KEY_ID"]
        args = build_bedrock_env_args(existing_env)
        # AWS_ACCESS_KEY_ID should be skipped, AWS_SECRET_ACCESS_KEY should be added
        self.assertNotIn("AWS_ACCESS_KEY_ID", args)
        self.assertIn("AWS_SECRET_ACCESS_KEY", args)

    def test_inherit_form_passes_session_token(self) -> None:
        """inherit form -E VAR passes session token from host (for STS creds)."""
        os.environ["AWS_ACCESS_KEY_ID"] = "ASIATEST"
        os.environ["AWS_SECRET_ACCESS_KEY"] = "secret"
        os.environ["AWS_SESSION_TOKEN"] = "token123"
        # user uses inherit form for credentials (expects all three from host)
        existing_env = ["-e", "AWS_ACCESS_KEY_ID", "-e", "AWS_SECRET_ACCESS_KEY"]
        args = build_bedrock_env_args(existing_env)
        # session token should be passed since user is inheriting host creds
        self.assertIn("AWS_SESSION_TOKEN", args)

    def test_explicit_form_skips_session_token(self) -> None:
        """explicit form -E VAR=value skips host session token to avoid mixing."""
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIAOLD"
        os.environ["AWS_SECRET_ACCESS_KEY"] = "oldsecret"
        os.environ["AWS_SESSION_TOKEN"] = "stale-token"
        # user provides explicit new credentials
        existing_env = ["-e", "AWS_ACCESS_KEY_ID=AKIANEW", "-e", "AWS_SECRET_ACCESS_KEY=newsecret"]
        args = build_bedrock_env_args(existing_env)
        # session token should NOT be passed (would be stale from different creds)
        self.assertNotIn("AWS_SESSION_TOKEN", args)

    def test_mixed_form_uses_explicit_logic(self) -> None:
        """if any credential has explicit value, skip session token."""
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
        os.environ["AWS_SECRET_ACCESS_KEY"] = "secret"
        os.environ["AWS_SESSION_TOKEN"] = "token123"
        # user provides explicit access key but inherits secret (unusual but possible)
        existing_env = ["-e", "AWS_ACCESS_KEY_ID=NEWKEY", "-e", "AWS_SECRET_ACCESS_KEY"]
        args = build_bedrock_env_args(existing_env)
        # session token should NOT be passed (explicit key means new credential source)
        self.assertNotIn("AWS_SESSION_TOKEN", args)

    def test_explicit_session_token_always_honored(self) -> None:
        """explicit -E AWS_SESSION_TOKEN always passes through."""
        os.environ["AWS_SESSION_TOKEN"] = "host-token"
        # user explicitly provides session token (regardless of cred form)
        existing_env = ["-e", "AWS_ACCESS_KEY_ID=KEY", "-e", "AWS_SESSION_TOKEN=explicit-token"]
        args = build_bedrock_env_args(existing_env)
        # explicit session token is not in result (already in existing_env)
        # but importantly, skip_session_token logic doesn't remove it
        self.assertNotIn("AWS_SESSION_TOKEN", args)  # already in existing_env, so skipped

    def test_invalid_provider_rejected(self) -> None:
        """unknown provider value → error."""
        os.environ["RALPHEX_CLAUDE_PROVIDER"] = "invalid"
        with self.assertRaises(ValueError) as ctx:
            get_claude_provider(None)
        self.assertIn("invalid", str(ctx.exception))
        self.assertIn("RALPHEX_CLAUDE_PROVIDER", str(ctx.exception))

    def test_env_var_case_insensitive(self) -> None:
        """env var value is case-insensitive."""
        os.environ["RALPHEX_CLAUDE_PROVIDER"] = "BEDROCK"
        provider = get_claude_provider(None)
        self.assertEqual(provider, "bedrock")
        os.environ["RALPHEX_CLAUDE_PROVIDER"] = "Bedrock"
        provider = get_claude_provider(None)
        self.assertEqual(provider, "bedrock")

    def test_env_var_whitespace_trimmed(self) -> None:
        """env var value whitespace is trimmed."""
        os.environ["RALPHEX_CLAUDE_PROVIDER"] = "  bedrock  "
        provider = get_claude_provider(None)
        self.assertEqual(provider, "bedrock")

    def test_empty_env_var_defaults(self) -> None:
        """empty env var falls back to default."""
        os.environ["RALPHEX_CLAUDE_PROVIDER"] = ""
        provider = get_claude_provider(None)
        self.assertEqual(provider, "default")

class TestDockerEnabled(EnvTestCase):
    """tests for docker socket flag and env var detection."""
    env_vars = ["RALPHEX_DOCKER_SOCKET"]

    def test_flag_true(self) -> None:
        """CLI --docker flag returns True."""
        self.assertTrue(is_docker_enabled(True))

    def test_flag_false_no_env(self) -> None:
        """no flag, no env var → False."""
        self.assertFalse(is_docker_enabled(False))

    def test_env_var_1(self) -> None:
        """RALPHEX_DOCKER_SOCKET=1 → True."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "1"
        self.assertTrue(is_docker_enabled(False))

    def test_env_var_true(self) -> None:
        """RALPHEX_DOCKER_SOCKET=true → True."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "true"
        self.assertTrue(is_docker_enabled(False))

    def test_env_var_yes(self) -> None:
        """RALPHEX_DOCKER_SOCKET=yes → True."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "yes"
        self.assertTrue(is_docker_enabled(False))

    def test_env_var_true_uppercase(self) -> None:
        """RALPHEX_DOCKER_SOCKET=TRUE → True (case-insensitive)."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "TRUE"
        self.assertTrue(is_docker_enabled(False))

    def test_env_var_yes_mixed_case(self) -> None:
        """RALPHEX_DOCKER_SOCKET=Yes → True (case-insensitive)."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "Yes"
        self.assertTrue(is_docker_enabled(False))

    def test_env_var_falsy_zero(self) -> None:
        """RALPHEX_DOCKER_SOCKET=0 → False."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "0"
        self.assertFalse(is_docker_enabled(False))

    def test_env_var_falsy_no(self) -> None:
        """RALPHEX_DOCKER_SOCKET=no → False."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "no"
        self.assertFalse(is_docker_enabled(False))

    def test_env_var_falsy_empty(self) -> None:
        """RALPHEX_DOCKER_SOCKET="" → False."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = ""
        self.assertFalse(is_docker_enabled(False))

    def test_env_var_falsy_random(self) -> None:
        """RALPHEX_DOCKER_SOCKET=blah → False."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "blah"
        self.assertFalse(is_docker_enabled(False))

    def test_flag_overrides_env(self) -> None:
        """CLI flag True even when env var is falsy."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "0"
        self.assertTrue(is_docker_enabled(True))

    def test_env_var_whitespace_trimmed(self) -> None:
        """whitespace around env var value is trimmed."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "  1  "
        self.assertTrue(is_docker_enabled(False))


class TestResolveDockerSocket(EnvTestCase):
    """tests for resolve_docker_socket() with DOCKER_HOST env var."""
    env_vars = ["DOCKER_HOST"]

    def test_default_when_no_docker_host(self) -> None:
        """returns default path when DOCKER_HOST is not set."""
        self.assertEqual(resolve_docker_socket(), DEFAULT_DOCKER_SOCKET)

    def test_unix_socket_path(self) -> None:
        """extracts socket path from unix:// DOCKER_HOST."""
        os.environ["DOCKER_HOST"] = "unix:///run/user/1000/docker.sock"
        self.assertEqual(resolve_docker_socket(), "/run/user/1000/docker.sock")

    def test_standard_unix_socket(self) -> None:
        """extracts standard socket path from unix:// DOCKER_HOST."""
        os.environ["DOCKER_HOST"] = "unix:///var/run/docker.sock"
        self.assertEqual(resolve_docker_socket(), "/var/run/docker.sock")

    def test_tcp_ignored(self) -> None:
        """falls back to default when DOCKER_HOST is tcp://."""
        os.environ["DOCKER_HOST"] = "tcp://localhost:2375"
        self.assertEqual(resolve_docker_socket(), DEFAULT_DOCKER_SOCKET)

    def test_empty_docker_host(self) -> None:
        """falls back to default when DOCKER_HOST is empty."""
        os.environ["DOCKER_HOST"] = ""
        self.assertEqual(resolve_docker_socket(), DEFAULT_DOCKER_SOCKET)

    def test_whitespace_trimmed(self) -> None:
        """whitespace around DOCKER_HOST value is trimmed."""
        os.environ["DOCKER_HOST"] = "  unix:///custom/docker.sock  "
        self.assertEqual(resolve_docker_socket(), "/custom/docker.sock")


class TestAwsCredentialExport(EnvTestCase):
    """tests for AWS profile credential export."""
    env_vars = ["AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"]

    def test_exports_credentials_with_profile(self) -> None:
        """AWS_PROFILE set → runs aws cli, parses JSON output."""
        os.environ["AWS_PROFILE"] = "test-profile"
        json_output = '{"AccessKeyId": "AKIATEST", "SecretAccessKey": "secret123", "SessionToken": "tok123"}'

        captured_cmd: list[list[str]] = []

        def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
            captured_cmd.append(cmd)
            mock_result = unittest.mock.Mock()
            mock_result.returncode = 0
            mock_result.stdout = json_output
            mock_result.stderr = ""
            return mock_result

        with unittest.mock.patch("subprocess.run", side_effect=fake_run):
            with unittest.mock.patch("shutil.which", return_value="/usr/bin/aws"):
                creds = export_aws_profile_credentials()

        self.assertEqual(creds["AWS_ACCESS_KEY_ID"], "AKIATEST")
        self.assertEqual(creds["AWS_SECRET_ACCESS_KEY"], "secret123")
        self.assertEqual(creds["AWS_SESSION_TOKEN"], "tok123")

        # validate the AWS CLI command structure
        self.assertEqual(len(captured_cmd), 1)
        cmd = captured_cmd[0]
        self.assertEqual(cmd, [
            "aws", "configure", "export-credentials",
            "--profile", "test-profile",
            "--output", "json",
        ])

    def test_skips_export_when_explicit_creds(self) -> None:
        """AWS_ACCESS_KEY_ID set → no aws cli call."""
        os.environ["AWS_PROFILE"] = "test-profile"
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIAEXPLICIT"

        call_count = [0]

        def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
            call_count[0] += 1
            mock_result = unittest.mock.Mock()
            mock_result.returncode = 0
            mock_result.stdout = "{}"
            return mock_result

        with unittest.mock.patch("subprocess.run", side_effect=fake_run):
            with unittest.mock.patch("shutil.which", return_value="/usr/bin/aws"):
                creds = export_aws_profile_credentials()

        self.assertEqual(creds, {})
        self.assertEqual(call_count[0], 0)

    def test_skips_export_when_no_profile(self) -> None:
        """AWS_PROFILE not set → no aws cli call."""
        call_count = [0]

        def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
            call_count[0] += 1
            mock_result = unittest.mock.Mock()
            mock_result.returncode = 0
            mock_result.stdout = "{}"
            return mock_result

        with unittest.mock.patch("subprocess.run", side_effect=fake_run):
            with unittest.mock.patch("shutil.which", return_value="/usr/bin/aws"):
                creds = export_aws_profile_credentials()

        self.assertEqual(creds, {})
        self.assertEqual(call_count[0], 0)

    def test_handles_export_failure(self) -> None:
        """aws cli fails → empty dict, no crash."""
        os.environ["AWS_PROFILE"] = "test-profile"

        def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
            mock_result = unittest.mock.Mock()
            mock_result.returncode = 1
            mock_result.stdout = ""
            mock_result.stderr = "profile not found"
            return mock_result

        import io
        captured = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured):
            with unittest.mock.patch("subprocess.run", side_effect=fake_run):
                with unittest.mock.patch("shutil.which", return_value="/usr/bin/aws"):
                    creds = export_aws_profile_credentials()

        self.assertEqual(creds, {})
        warning = captured.getvalue()
        self.assertIn("warning:", warning)
        self.assertIn("test-profile", warning)

    def test_handles_missing_aws_cli(self) -> None:
        """aws CLI not installed → empty dict, warning logged."""
        os.environ["AWS_PROFILE"] = "test-profile"

        import io
        captured = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured):
            with unittest.mock.patch("shutil.which", return_value=None):
                creds = export_aws_profile_credentials()

        self.assertEqual(creds, {})
        warning = captured.getvalue()
        self.assertIn("warning:", warning)
        self.assertIn("aws CLI not found", warning)

    def test_parses_json_output(self) -> None:
        """correctly extracts AccessKeyId/SecretAccessKey/SessionToken from JSON."""
        os.environ["AWS_PROFILE"] = "test-profile"

        # test with minimal output (no session token)
        json_output = '{"AccessKeyId": "AKIA123", "SecretAccessKey": "secret456"}'

        def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
            mock_result = unittest.mock.Mock()
            mock_result.returncode = 0
            mock_result.stdout = json_output
            mock_result.stderr = ""
            return mock_result

        with unittest.mock.patch("subprocess.run", side_effect=fake_run):
            with unittest.mock.patch("shutil.which", return_value="/usr/bin/aws"):
                creds = export_aws_profile_credentials()

        self.assertEqual(creds["AWS_ACCESS_KEY_ID"], "AKIA123")
        self.assertEqual(creds["AWS_SECRET_ACCESS_KEY"], "secret456")
        self.assertNotIn("AWS_SESSION_TOKEN", creds)

    def test_handles_invalid_json(self) -> None:
        """invalid JSON output → empty dict, warning logged."""
        os.environ["AWS_PROFILE"] = "test-profile"

        def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
            mock_result = unittest.mock.Mock()
            mock_result.returncode = 0
            mock_result.stdout = "not valid json"
            mock_result.stderr = ""
            return mock_result

        import io
        captured = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured):
            with unittest.mock.patch("subprocess.run", side_effect=fake_run):
                with unittest.mock.patch("shutil.which", return_value="/usr/bin/aws"):
                    creds = export_aws_profile_credentials()

        self.assertEqual(creds, {})
        warning = captured.getvalue()
        self.assertIn("warning:", warning)
        self.assertIn("parse credentials JSON", warning)

    def test_handles_oserror_from_subprocess(self) -> None:
        """subprocess.run raises OSError → empty dict, warning logged."""
        os.environ["AWS_PROFILE"] = "test-profile"

        import io
        captured = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured):
            with unittest.mock.patch("subprocess.run", side_effect=OSError("cannot execute")):
                with unittest.mock.patch("shutil.which", return_value="/usr/bin/aws"):
                    creds = export_aws_profile_credentials()

        self.assertEqual(creds, {})
        warning = captured.getvalue()
        self.assertIn("warning:", warning)
        self.assertIn("failed to run aws CLI", warning)

class TestBedrockSkipKeychain(EnvTestCase):
    """tests for bedrock mode skipping keychain and claude_home checks."""
    env_vars = ["RALPHEX_CLAUDE_PROVIDER"] + BEDROCK_ENV_VARS
    save_argv = True

    def test_skips_credentials_extraction_when_bedrock(self) -> None:
        """bedrock provider skips extract_macos_credentials (creds_temp is None)."""
        captured_creds_temp: list[object] = []

        def fake_run_docker(
            image: str, port: int, volumes: list[str], extra_env: list[str],
            bind_port: bool, ralphex_args: list[str], docker_gid: int | None = None, **kwargs: object,
        ) -> int:
            # capture what we need via side effect - creds_temp should be None for bedrock
            return 0

        extract_calls = [0]

        def fake_extract(claude_home: Path) -> Optional[Path]:
            extract_calls[0] += 1
            return None

        import io
        captured_stderr = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured_stderr):
            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", side_effect=fake_extract):
                    with unittest.mock.patch("ralphex_dk.export_aws_profile_credentials", return_value={}):
                        sys.argv = ["ralphex-dk", "--claude-provider", "bedrock", "plan.md"]
                        result = main()

        self.assertEqual(result, 0)
        # extract_macos_credentials should NOT be called for bedrock
        self.assertEqual(extract_calls[0], 0)

    def test_skips_claude_home_check_when_bedrock(self) -> None:
        """bedrock provider skips claude_home.is_dir() check - no error if ~/.claude missing."""
        import io
        captured_stderr = io.StringIO()

        def fake_run_docker(
            image: str, port: int, volumes: list[str], extra_env: list[str],
            bind_port: bool, ralphex_args: list[str], docker_gid: int | None = None, **kwargs: object,
        ) -> int:
            return 0

        # use a non-existent claude home path
        with tempfile.TemporaryDirectory() as tmpdir:
            fake_claude_home = Path(tmpdir) / "nonexistent_claude"
            # do NOT create the directory

            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                        with unittest.mock.patch("ralphex_dk.export_aws_profile_credentials", return_value={}):
                            # mock Path.home to return our temp dir so claude_home resolves to nonexistent
                            with unittest.mock.patch.object(Path, "home", return_value=Path(tmpdir)):
                                sys.argv = ["ralphex-dk", "--claude-provider", "bedrock", "plan.md"]
                                result = main()

        self.assertEqual(result, 0)
        # should NOT see "directory not found" error
        self.assertNotIn("directory not found", captured_stderr.getvalue())

    def test_normal_mode_still_extracts_credentials(self) -> None:
        """default provider still calls extract_macos_credentials for backwards compat."""
        extract_calls = [0]

        def fake_extract(claude_home: Path) -> Optional[Path]:
            extract_calls[0] += 1
            return None

        def fake_run_docker(
            image: str, port: int, volumes: list[str], extra_env: list[str],
            bind_port: bool, ralphex_args: list[str], docker_gid: int | None = None, **kwargs: object,
        ) -> int:
            return 0

        import io
        captured_stderr = io.StringIO()
        fake_claude_dir = tempfile.mkdtemp()
        # place a fake credentials.json so fail-fast doesn't trigger on macOS
        (Path(fake_claude_dir) / ".credentials.json").write_text("{}")
        try:
            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("ralphex_dk.extract_macos_credentials", side_effect=fake_extract):
                        sys.argv = ["ralphex-dk", "plan.md"]
                        os.environ["CLAUDE_CONFIG_DIR"] = fake_claude_dir
                        try:
                            result = main()
                        finally:
                            os.environ.pop("CLAUDE_CONFIG_DIR", None)
        finally:
            shutil.rmtree(fake_claude_dir, ignore_errors=True)

        self.assertEqual(result, 0)
        # extract_macos_credentials SHOULD be called for default provider
        self.assertEqual(extract_calls[0], 1)

    def test_fails_fast_on_macos_when_no_credentials(self) -> None:
        """on macOS, default provider with no on-disk creds and failed keychain extraction exits with 1."""
        run_calls = [0]

        def fake_run_docker(
            image: str, port: int, volumes: list[str], extra_env: list[str],
            bind_port: bool, ralphex_args: list[str], docker_gid: int | None = None, **kwargs: object,
        ) -> int:
            run_calls[0] += 1
            return 0

        captured_stderr = io.StringIO()
        fake_claude_dir = tempfile.mkdtemp()
        # no .credentials.json on disk
        try:
            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("ralphex_dk.platform.system", return_value="Darwin"):
                    with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                        with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                            sys.argv = ["ralphex-dk", "plan.md"]
                            os.environ["CLAUDE_CONFIG_DIR"] = fake_claude_dir
                            try:
                                result = main()
                            finally:
                                os.environ.pop("CLAUDE_CONFIG_DIR", None)
        finally:
            shutil.rmtree(fake_claude_dir, ignore_errors=True)

        self.assertEqual(result, 1)
        self.assertEqual(run_calls[0], 0)
        err = captured_stderr.getvalue()
        self.assertIn("no Claude credentials found", err)
        self.assertIn("keychain", err)

    def test_no_fail_fast_when_credentials_on_disk(self) -> None:
        """on macOS, default provider with on-disk creds proceeds even if extraction returns None."""
        run_calls = [0]

        def fake_run_docker(
            image: str, port: int, volumes: list[str], extra_env: list[str],
            bind_port: bool, ralphex_args: list[str], docker_gid: int | None = None, **kwargs: object,
        ) -> int:
            run_calls[0] += 1
            return 0

        captured_stderr = io.StringIO()
        fake_claude_dir = tempfile.mkdtemp()
        (Path(fake_claude_dir) / ".credentials.json").write_text("{}")
        try:
            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("ralphex_dk.platform.system", return_value="Darwin"):
                    with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                        with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                            sys.argv = ["ralphex-dk", "plan.md"]
                            os.environ["CLAUDE_CONFIG_DIR"] = fake_claude_dir
                            try:
                                result = main()
                            finally:
                                os.environ.pop("CLAUDE_CONFIG_DIR", None)
        finally:
            shutil.rmtree(fake_claude_dir, ignore_errors=True)

        self.assertEqual(result, 0)
        self.assertEqual(run_calls[0], 1)

    def test_no_fail_fast_on_non_darwin(self) -> None:
        """on non-Darwin, no fail-fast even when extraction returns None and no creds on disk."""
        run_calls = [0]

        def fake_run_docker(
            image: str, port: int, volumes: list[str], extra_env: list[str],
            bind_port: bool, ralphex_args: list[str], docker_gid: int | None = None, **kwargs: object,
        ) -> int:
            run_calls[0] += 1
            return 0

        captured_stderr = io.StringIO()
        fake_claude_dir = tempfile.mkdtemp()
        try:
            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("ralphex_dk.platform.system", return_value="Linux"):
                    with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                        with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                            sys.argv = ["ralphex-dk", "plan.md"]
                            os.environ["CLAUDE_CONFIG_DIR"] = fake_claude_dir
                            try:
                                result = main()
                            finally:
                                os.environ.pop("CLAUDE_CONFIG_DIR", None)
        finally:
            shutil.rmtree(fake_claude_dir, ignore_errors=True)

        self.assertEqual(result, 0)
        self.assertEqual(run_calls[0], 1)

    def test_startup_message_shows_bedrock_mode(self) -> None:
        """startup output includes 'bedrock' and 'keychain skipped'."""
        def fake_run_docker(
            image: str, port: int, volumes: list[str], extra_env: list[str],
            bind_port: bool, ralphex_args: list[str], docker_gid: int | None = None, **kwargs: object,
        ) -> int:
            return 0

        import io
        captured_stderr = io.StringIO()
        with unittest.mock.patch("sys.stderr", captured_stderr):
            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                    with unittest.mock.patch("ralphex_dk.export_aws_profile_credentials", return_value={}):
                        sys.argv = ["ralphex-dk", "--claude-provider", "bedrock", "plan.md"]
                        result = main()

        self.assertEqual(result, 0)
        output = captured_stderr.getvalue()
        self.assertIn("bedrock", output.lower())
        self.assertIn("keychain skipped", output.lower())

class TestBedrockValidation(EnvTestCase):
    """tests for validate_bedrock_config() function."""
    env_vars = ["CLAUDE_CODE_USE_BEDROCK", "AWS_REGION", "AWS_PROFILE",
                "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK"]

    def test_warns_missing_aws_region(self) -> None:
        """warns when AWS_REGION is not set."""
        # set other vars, but not AWS_REGION
        os.environ["CLAUDE_CODE_USE_BEDROCK"] = "1"
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
        os.environ["AWS_SECRET_ACCESS_KEY"] = "secret"

        warnings = validate_bedrock_config()

        self.assertEqual(len(warnings), 1)
        self.assertIn("AWS_REGION", warnings[0])

    def test_warns_no_credentials_found(self) -> None:
        """warns when neither AWS_PROFILE nor AWS_ACCESS_KEY_ID is set."""
        os.environ["CLAUDE_CODE_USE_BEDROCK"] = "1"
        os.environ["AWS_REGION"] = "us-east-1"
        # no credentials set

        warnings = validate_bedrock_config()

        self.assertEqual(len(warnings), 1)
        self.assertIn("no AWS credentials found", warnings[0])

    def test_no_warning_with_profile(self) -> None:
        """no credential warning when AWS_PROFILE is set."""
        os.environ["CLAUDE_CODE_USE_BEDROCK"] = "1"
        os.environ["AWS_REGION"] = "us-east-1"
        os.environ["AWS_PROFILE"] = "my-profile"

        warnings = validate_bedrock_config()

        self.assertEqual(len(warnings), 0)

    def test_no_warning_with_explicit_creds(self) -> None:
        """no credential warning when both access key and secret key are set."""
        os.environ["CLAUDE_CODE_USE_BEDROCK"] = "1"
        os.environ["AWS_REGION"] = "us-east-1"
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
        os.environ["AWS_SECRET_ACCESS_KEY"] = "secret"

        warnings = validate_bedrock_config()

        self.assertEqual(len(warnings), 0)

    def test_no_warning_with_bearer_token(self) -> None:
        """no credential warning when AWS_BEARER_TOKEN_BEDROCK is set."""
        os.environ["CLAUDE_CODE_USE_BEDROCK"] = "1"
        os.environ["AWS_REGION"] = "us-east-1"
        os.environ["AWS_BEARER_TOKEN_BEDROCK"] = "token123"

        warnings = validate_bedrock_config()

        self.assertEqual(len(warnings), 0)

    def test_warns_missing_secret_key(self) -> None:
        """warns when AWS_ACCESS_KEY_ID is set but AWS_SECRET_ACCESS_KEY is missing."""
        os.environ["CLAUDE_CODE_USE_BEDROCK"] = "1"
        os.environ["AWS_REGION"] = "us-east-1"
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
        # AWS_SECRET_ACCESS_KEY is NOT set

        warnings = validate_bedrock_config()

        self.assertEqual(len(warnings), 1)
        self.assertIn("AWS_SECRET_ACCESS_KEY", warnings[0])

    def test_no_warning_when_set_via_e_flag(self) -> None:
        """no warning when vars are provided via -E flags."""
        # nothing in os.environ
        extra_env = [
            "-e", "CLAUDE_CODE_USE_BEDROCK=1",
            "-e", "AWS_REGION=us-east-1",
            "-e", "AWS_ACCESS_KEY_ID=AKIATEST",
            "-e", "AWS_SECRET_ACCESS_KEY=secret",
        ]

        warnings = validate_bedrock_config(extra_env)

        self.assertEqual(len(warnings), 0)

    def test_e_flag_overrides_env(self) -> None:
        """values from -E flags take precedence over os.environ."""
        # os.environ has empty/unset value
        os.environ["CLAUDE_CODE_USE_BEDROCK"] = ""
        # -E flag has proper value
        extra_env = [
            "-e", "CLAUDE_CODE_USE_BEDROCK=1",
            "-e", "AWS_REGION=us-east-1",
            "-e", "AWS_ACCESS_KEY_ID=AKIATEST",
            "-e", "AWS_SECRET_ACCESS_KEY=secret",
        ]

        warnings = validate_bedrock_config(extra_env)

        self.assertEqual(len(warnings), 0)

    def test_inherit_form_uses_os_environ(self) -> None:
        """inherit form (-e VAR) uses os.environ value."""
        os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
        os.environ["AWS_SECRET_ACCESS_KEY"] = "secret"
        extra_env = [
            "-e", "CLAUDE_CODE_USE_BEDROCK=1",
            "-e", "AWS_REGION=us-east-1",
            "-e", "AWS_ACCESS_KEY_ID",  # inherit form
            "-e", "AWS_SECRET_ACCESS_KEY",  # inherit form
        ]

        warnings = validate_bedrock_config(extra_env)

        self.assertEqual(len(warnings), 0)

class TestParseEnvFlags(unittest.TestCase):
    """tests for parse_env_flags() function."""

    def test_empty_list(self) -> None:
        """empty list returns empty values and explicit sets."""
        result = parse_env_flags([])
        self.assertEqual(result.values, {})
        self.assertEqual(result.explicit, set())

        result = parse_env_flags(None)
        self.assertEqual(result.values, {})
        self.assertEqual(result.explicit, set())

    def test_assignment_form_is_explicit(self) -> None:
        """VAR=value form is tracked as explicit."""
        extra_env = ["-e", "AWS_REGION=us-east-1", "-e", "AWS_ACCESS_KEY_ID=AKIATEST"]
        result = parse_env_flags(extra_env)
        self.assertEqual(result.values["AWS_REGION"], "us-east-1")
        self.assertEqual(result.values["AWS_ACCESS_KEY_ID"], "AKIATEST")
        self.assertIn("AWS_REGION", result.explicit)
        self.assertIn("AWS_ACCESS_KEY_ID", result.explicit)

    def test_inherit_form_not_explicit(self) -> None:
        """VAR form (inherit) is not tracked as explicit."""
        os.environ["MY_VAR"] = "my_value"
        try:
            extra_env = ["-e", "MY_VAR"]
            result = parse_env_flags(extra_env)
            self.assertEqual(result.values["MY_VAR"], "my_value")
            self.assertNotIn("MY_VAR", result.explicit)
        finally:
            os.environ.pop("MY_VAR", None)

    def test_mixed_forms(self) -> None:
        """mix of explicit and inherit forms."""
        os.environ["INHERIT_VAR"] = "inherited"
        try:
            extra_env = ["-e", "EXPLICIT_VAR=explicit", "-e", "INHERIT_VAR"]
            result = parse_env_flags(extra_env)
            self.assertEqual(result.values["EXPLICIT_VAR"], "explicit")
            self.assertEqual(result.values["INHERIT_VAR"], "inherited")
            self.assertIn("EXPLICIT_VAR", result.explicit)
            self.assertNotIn("INHERIT_VAR", result.explicit)
        finally:
            os.environ.pop("INHERIT_VAR", None)

class TestExtractEnvFromFlags(unittest.TestCase):
    """tests for extract_env_from_flags() function."""

    def test_empty_list(self) -> None:
        """empty list returns empty dict."""
        self.assertEqual(extract_env_from_flags([]), {})
        self.assertEqual(extract_env_from_flags(None), {})

    def test_assignment_form(self) -> None:
        """parses VAR=value form."""
        extra_env = ["-e", "AWS_REGION=us-east-1", "-e", "AWS_ACCESS_KEY_ID=AKIATEST"]
        result = extract_env_from_flags(extra_env)
        self.assertEqual(result["AWS_REGION"], "us-east-1")
        self.assertEqual(result["AWS_ACCESS_KEY_ID"], "AKIATEST")

    def test_inherit_form(self) -> None:
        """parses VAR form (inherit from os.environ)."""
        os.environ["MY_VAR"] = "my_value"
        try:
            extra_env = ["-e", "MY_VAR"]
            result = extract_env_from_flags(extra_env)
            self.assertEqual(result["MY_VAR"], "my_value")
        finally:
            os.environ.pop("MY_VAR", None)

    def test_inherit_form_missing_env(self) -> None:
        """inherit form skips vars not in os.environ."""
        os.environ.pop("MISSING_VAR", None)
        extra_env = ["-e", "MISSING_VAR"]
        result = extract_env_from_flags(extra_env)
        self.assertNotIn("MISSING_VAR", result)

    def test_value_with_equals(self) -> None:
        """values can contain equals signs."""
        extra_env = ["-e", "MY_VAR=foo=bar=baz"]
        result = extract_env_from_flags(extra_env)
        self.assertEqual(result["MY_VAR"], "foo=bar=baz")


class TestBuildDockerCommand(unittest.TestCase):
    """tests for build_docker_command() function."""

    def test_build_docker_command_basic(self) -> None:
        """verify command structure includes base env vars and correct order."""
        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = False
            cmd = build_docker_command(
                image="test-image:latest",
                port="8080",
                volumes=["-v", "/src:/dst"],
                env_vars=["-e", "FOO=bar"],
                bind_port=False,
                args=["--help"],
            )

        # verify docker run command structure
        self.assertEqual(cmd[0], "docker")
        self.assertEqual(cmd[1], "run")

        # no -it flag when not a tty
        self.assertNotIn("-it", cmd)

        # --rm is present
        self.assertIn("--rm", cmd)

        # verify base env vars are present (check one key one)
        self.assertIn("CLAUDE_CONFIG_DIR=/home/app/.claude", cmd)

        # verify extra env var is present
        self.assertIn("FOO=bar", cmd)

        # verify volumes are present
        self.assertIn("/src:/dst", cmd)

        # verify workdir
        idx_w = cmd.index("-w")
        self.assertEqual(cmd[idx_w + 1], "/workspace")

        # verify image and entrypoint
        self.assertIn("test-image:latest", cmd)
        self.assertIn("/srv/ralphex", cmd)

        # verify args
        self.assertIn("--help", cmd)

        # verify order: volumes before image, image before args
        vol_idx = cmd.index("/src:/dst")
        img_idx = cmd.index("test-image:latest")
        args_idx = cmd.index("--help")
        self.assertLess(vol_idx, img_idx)
        self.assertLess(img_idx, args_idx)

    def test_build_docker_command_with_serve(self) -> None:
        """verify port binding AND RALPHEX_WEB_HOST=0.0.0.0 env var injection."""
        # ensure RALPHEX_WEB_HOST is not in env
        saved = os.environ.pop("RALPHEX_WEB_HOST", None)
        try:
            with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                mock_stdin.isatty.return_value = False
                cmd = build_docker_command(
                    image="test-image:latest",
                    port="9090",
                    volumes=[],
                    env_vars=[],
                    bind_port=True,
                    args=["--serve"],
                )

            # verify port binding
            port_idx = cmd.index("-p")
            self.assertEqual(cmd[port_idx + 1], "127.0.0.1:9090:8080")

            # verify RALPHEX_WEB_HOST is injected
            self.assertIn("RALPHEX_WEB_HOST=0.0.0.0", cmd)
        finally:
            if saved is not None:
                os.environ["RALPHEX_WEB_HOST"] = saved

    def test_build_docker_command_with_serve_web_host_set(self) -> None:
        """verify RALPHEX_WEB_HOST is NOT injected when already set in env."""
        saved = os.environ.get("RALPHEX_WEB_HOST")
        os.environ["RALPHEX_WEB_HOST"] = "127.0.0.1"
        try:
            with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                mock_stdin.isatty.return_value = False
                cmd = build_docker_command(
                    image="test-image:latest",
                    port="8080",
                    volumes=[],
                    env_vars=[],
                    bind_port=True,
                    args=[],
                )

            # verify RALPHEX_WEB_HOST=0.0.0.0 is NOT in the command
            self.assertNotIn("RALPHEX_WEB_HOST=0.0.0.0", cmd)
        finally:
            if saved is not None:
                os.environ["RALPHEX_WEB_HOST"] = saved
            else:
                os.environ.pop("RALPHEX_WEB_HOST", None)

    def test_build_docker_command_interactive(self) -> None:
        """verify -it flag when stdin is tty."""
        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = True
            cmd = build_docker_command(
                image="test-image:latest",
                port="8080",
                volumes=[],
                env_vars=[],
                bind_port=False,
                args=[],
            )

        # verify -it flag is present when tty
        self.assertIn("-it", cmd)


class TestDockerNetwork(EnvTestCase):
    """tests for RALPHEX_DOCKER_NETWORK / --network support."""
    env_vars = ["RALPHEX_IMAGE", "RALPHEX_PORT", "RALPHEX_DOCKER_NETWORK"]
    save_argv = True

    def test_build_docker_command_with_network(self) -> None:
        """verify --network flag in docker command."""
        cmd = build_docker_command(
            image="test-image:latest", port="8080", volumes=[], env_vars=[],
            bind_port=False, args=[], network="host",
        )
        idx = cmd.index("--network")
        self.assertEqual(cmd[idx + 1], "host")

    def test_build_docker_command_without_network(self) -> None:
        """verify no --network flag when network is empty."""
        cmd = build_docker_command(
            image="test-image:latest", port="8080", volumes=[], env_vars=[],
            bind_port=False, args=[],
        )
        self.assertNotIn("--network", cmd)

    def test_build_docker_command_named_network(self) -> None:
        """verify --network with named network."""
        cmd = build_docker_command(
            image="test-image:latest", port="8080", volumes=[], env_vars=[],
            bind_port=False, args=[], network="my-compose-net",
        )
        idx = cmd.index("--network")
        self.assertEqual(cmd[idx + 1], "my-compose-net")


class TestDetectInheritedEnvVars(unittest.TestCase):
    """tests for detect_inherited_env_vars() function."""

    def test_detect_inherited_env_vars(self) -> None:
        """verify extraction of inherited (no =value) env var names."""
        # mixed explicit and inherited vars
        extra_env = ["-e", "VAR1=value1", "-e", "VAR2", "-e", "VAR3=value3", "-e", "VAR4"]
        inherited = detect_inherited_env_vars(extra_env)
        self.assertEqual(inherited, ["VAR2", "VAR4"])

    def test_all_explicit(self) -> None:
        """verify empty list when all vars have explicit values."""
        extra_env = ["-e", "VAR1=value1", "-e", "VAR2=value2"]
        inherited = detect_inherited_env_vars(extra_env)
        self.assertEqual(inherited, [])

    def test_all_inherited(self) -> None:
        """verify all vars returned when none have values."""
        extra_env = ["-e", "VAR1", "-e", "VAR2"]
        inherited = detect_inherited_env_vars(extra_env)
        self.assertEqual(inherited, ["VAR1", "VAR2"])

    def test_empty_list(self) -> None:
        """verify empty list for empty input."""
        inherited = detect_inherited_env_vars([])
        self.assertEqual(inherited, [])

    def test_trailing_e_flag(self) -> None:
        """verify trailing -e without value is handled gracefully."""
        # trailing -e with no following element should be skipped
        extra_env = ["-e", "VAR1=value1", "-e"]
        inherited = detect_inherited_env_vars(extra_env)
        self.assertEqual(inherited, [])

    def test_empty_value_not_inherited(self) -> None:
        """verify VAR= (empty value) is not considered inherited."""
        # VAR= has explicit empty value, not inherited
        extra_env = ["-e", "VAR1=", "-e", "VAR2"]
        inherited = detect_inherited_env_vars(extra_env)
        self.assertEqual(inherited, ["VAR2"])


class TestDetectExplicitSecrets(unittest.TestCase):
    """tests for detect_explicit_secrets() function."""

    def test_detects_sensitive_with_values(self) -> None:
        """verify detection of sensitive vars with explicit values."""
        extra_env = ["-e", "API_KEY=secret123", "-e", "DEBUG=1", "-e", "AWS_SECRET_ACCESS_KEY=abc"]
        secrets = detect_explicit_secrets(extra_env)
        self.assertEqual(secrets, ["API_KEY", "AWS_SECRET_ACCESS_KEY"])

    def test_ignores_inherited_sensitive(self) -> None:
        """verify inherited (no value) sensitive vars are NOT flagged."""
        extra_env = ["-e", "API_KEY", "-e", "AWS_SECRET_ACCESS_KEY"]
        secrets = detect_explicit_secrets(extra_env)
        self.assertEqual(secrets, [])

    def test_ignores_non_sensitive_with_values(self) -> None:
        """verify non-sensitive vars with values are NOT flagged."""
        extra_env = ["-e", "DEBUG=1", "-e", "VERBOSE=true"]
        secrets = detect_explicit_secrets(extra_env)
        self.assertEqual(secrets, [])

    def test_empty_list(self) -> None:
        """verify empty list for empty input."""
        secrets = detect_explicit_secrets([])
        self.assertEqual(secrets, [])

    def test_mixed_inherited_and_explicit(self) -> None:
        """verify only explicit sensitive vars are flagged."""
        extra_env = ["-e", "API_KEY=secret", "-e", "TOKEN", "-e", "DEBUG=1"]
        secrets = detect_explicit_secrets(extra_env)
        self.assertEqual(secrets, ["API_KEY"])


class TestDryRun(EnvTestCase):
    """tests for --dry-run functionality."""

    env_vars = ["RALPHEX_IMAGE", "RALPHEX_PORT", "RALPHEX_EXTRA_ENV",
                "RALPHEX_EXTRA_VOLUMES", "RALPHEX_CLAUDE_PROVIDER", "CLAUDE_CONFIG_DIR",
                "RALPHEX_WEB_HOST"]
    save_argv = True

    def test_dry_run_output_format(self) -> None:
        """verify shlex.join() produces valid shell command."""
        import shlex

        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--dry-run"]

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                            mock_stdin.isatty.return_value = False
                            with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                                with unittest.mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
                                    with unittest.mock.patch("sys.stderr", new_callable=io.StringIO):
                                        result = main()

            self.assertEqual(result, 0)
            output = mock_stdout.getvalue().strip()
            # verify output starts with docker run
            self.assertTrue(output.startswith("docker run"), f"output should start with 'docker run', got: {output}")
            # verify shlex.split can parse it (valid shell command)
            parts = shlex.split(output)
            self.assertIn("docker", parts)
            self.assertIn("run", parts)
            # verify essential command components are present
            self.assertIn("--rm", parts)
            self.assertIn("/srv/ralphex", parts)
            # verify image appears before entrypoint
            img_indices = [i for i, p in enumerate(parts) if "ralphex" in p and ":" in p]
            entrypoint_idx = parts.index("/srv/ralphex")
            self.assertTrue(any(i < entrypoint_idx for i in img_indices), "image should appear before entrypoint")

    def test_dry_run_inherited_env_warning(self) -> None:
        """verify warning printed for inherited (no value) env vars."""
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--dry-run", "-E", "FOO"]

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                            mock_stdin.isatty.return_value = False
                            with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                                with unittest.mock.patch("sys.stdout", new_callable=io.StringIO):
                                    with unittest.mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
                                        result = main()

            self.assertEqual(result, 0)
            stderr_output = mock_stderr.getvalue()
            self.assertIn("inherited env vars", stderr_output)
            self.assertIn("FOO", stderr_output)

    def test_dry_run_no_warning_explicit_values(self) -> None:
        """verify no warning when all env vars have explicit values."""
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--dry-run", "-E", "FOO=bar"]

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                            mock_stdin.isatty.return_value = False
                            with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                                with unittest.mock.patch("sys.stdout", new_callable=io.StringIO):
                                    with unittest.mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
                                        result = main()

            self.assertEqual(result, 0)
            stderr_output = mock_stderr.getvalue()
            self.assertNotIn("inherited env vars", stderr_output)

    def test_dry_run_explicit_secrets_warning(self) -> None:
        """verify warning printed for explicit values of sensitive env vars."""
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--dry-run", "-E", "API_KEY=secret123"]

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                            mock_stdin.isatty.return_value = False
                            with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                                with unittest.mock.patch("sys.stdout", new_callable=io.StringIO):
                                    with unittest.mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
                                        result = main()

            self.assertEqual(result, 0)
            stderr_output = mock_stderr.getvalue()
            self.assertIn("explicit values for sensitive vars", stderr_output)
            self.assertIn("API_KEY", stderr_output)

    def test_dry_run_no_explicit_secrets_warning_for_inherited(self) -> None:
        """verify no secrets warning when sensitive vars use inherited form."""
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            # API_KEY without =value - inherited form, should NOT trigger warning
            sys.argv = ["ralphex-dk", "--dry-run", "-E", "API_KEY"]

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                            mock_stdin.isatty.return_value = False
                            with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                                with unittest.mock.patch("sys.stdout", new_callable=io.StringIO):
                                    with unittest.mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
                                        result = main()

            self.assertEqual(result, 0)
            stderr_output = mock_stderr.getvalue()
            # should have inherited warning but NOT sensitive warning
            self.assertIn("inherited env vars", stderr_output)
            self.assertNotIn("explicit values for sensitive vars", stderr_output)

    def test_dry_run_does_not_execute_docker(self) -> None:
        """verify --dry-run returns without calling run_docker."""
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--dry-run"]

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                            mock_stdin.isatty.return_value = False
                            with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                                with unittest.mock.patch("ralphex_dk.run_docker") as mock_run_docker:
                                    with unittest.mock.patch("sys.stdout", new_callable=io.StringIO):
                                        with unittest.mock.patch("sys.stderr", new_callable=io.StringIO):
                                            result = main()

            self.assertEqual(result, 0)
            mock_run_docker.assert_not_called()


class TestDockerSocketGid(unittest.TestCase):
    """tests for get_docker_socket_gid() function."""

    def test_returns_gid_on_linux(self) -> None:
        """returns st_gid from os.stat() on linux."""
        mock_stat = unittest.mock.MagicMock()
        mock_stat.st_gid = 999
        with unittest.mock.patch("ralphex_dk.os.stat", return_value=mock_stat):
            with unittest.mock.patch("ralphex_dk.platform.system", return_value="Linux"):
                self.assertEqual(get_docker_socket_gid("/var/run/docker.sock"), 999)

    def test_returns_zero_on_macos(self) -> None:
        """returns 0 on macOS regardless of actual socket GID (docker desktop maps to root)."""
        mock_stat = unittest.mock.MagicMock()
        mock_stat.st_gid = 20  # staff group on macOS
        with unittest.mock.patch("ralphex_dk.os.stat", return_value=mock_stat):
            with unittest.mock.patch("ralphex_dk.platform.system", return_value="Darwin"):
                self.assertEqual(get_docker_socket_gid("/var/run/docker.sock"), 0)

    def test_returns_none_on_missing_socket(self) -> None:
        """returns None when socket doesn't exist."""
        with unittest.mock.patch("ralphex_dk.os.stat", side_effect=OSError("not found")):
            self.assertIsNone(get_docker_socket_gid("/var/run/docker.sock"))

    def test_returns_gid_zero_on_linux(self) -> None:
        """returns 0 when socket is owned by root group on linux."""
        mock_stat = unittest.mock.MagicMock()
        mock_stat.st_gid = 0
        with unittest.mock.patch("ralphex_dk.os.stat", return_value=mock_stat):
            with unittest.mock.patch("ralphex_dk.platform.system", return_value="Linux"):
                self.assertEqual(get_docker_socket_gid("/var/run/docker.sock"), 0)

    def test_custom_socket_path_on_linux(self) -> None:
        """works with custom socket path on linux."""
        mock_stat = unittest.mock.MagicMock()
        mock_stat.st_gid = 42
        with unittest.mock.patch("ralphex_dk.os.stat", return_value=mock_stat):
            with unittest.mock.patch("ralphex_dk.platform.system", return_value="Linux"):
                result = get_docker_socket_gid("/custom/docker.sock")
                self.assertEqual(result, 42)


class TestDockerSocketMount(EnvTestCase):
    """tests for docker socket mount in main()."""
    env_vars = ["RALPHEX_IMAGE", "RALPHEX_PORT", "RALPHEX_EXTRA_ENV",
                "RALPHEX_EXTRA_VOLUMES", "RALPHEX_CLAUDE_PROVIDER", "CLAUDE_CONFIG_DIR",
                "RALPHEX_WEB_HOST", "RALPHEX_DOCKER_SOCKET", "DOCKER_HOST"]
    save_argv = True

    def _run_main_with_docker(self, docker_flag: bool = False, socket_exists: bool = True,
                               socket_gid: int = 999, platform_name: str = "Linux",
                               ) -> tuple[int, list[str], list]:
        """helper: run main() with docker flag and mock socket, return (rc, captured_volumes, run_docker_calls)."""
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            argv = ["ralphex-dk"]
            if docker_flag:
                argv.append("--docker")
            argv.append("plan.md")
            sys.argv = argv

            captured_volumes: list[str] = []
            run_docker_calls: list[dict] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_volumes.extend(volumes)
                run_docker_calls.append({"docker_gid": docker_gid})
                return 0

            _real_exists = os.path.exists

            def fake_exists(p: str) -> bool:
                if p == DEFAULT_DOCKER_SOCKET:
                    return socket_exists
                return _real_exists(p)

            _real_stat = os.stat

            def fake_stat(p: str, *a: object, **kw: object) -> object:
                if p == DEFAULT_DOCKER_SOCKET:
                    mock_st = unittest.mock.MagicMock()
                    mock_st.st_gid = socket_gid
                    return mock_st
                return _real_stat(p, *a, **kw)

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                                with unittest.mock.patch("ralphex_dk.os.path.exists", side_effect=fake_exists):
                                    with unittest.mock.patch("ralphex_dk.os.stat", side_effect=fake_stat):
                                        with unittest.mock.patch("ralphex_dk.platform.system", return_value=platform_name):
                                            with unittest.mock.patch("sys.stderr", new_callable=io.StringIO):
                                                result = main()

            return result, captured_volumes, run_docker_calls

    def test_socket_mounted_when_flag_set(self) -> None:
        """socket volume appears when --docker flag is set and socket exists."""
        result, volumes, calls = self._run_main_with_docker(docker_flag=True, socket_exists=True)
        self.assertEqual(result, 0)
        # verify socket volume is in the volumes list
        socket_mount = f"{DEFAULT_DOCKER_SOCKET}:{DEFAULT_DOCKER_SOCKET}"
        self.assertIn(socket_mount, volumes)

    def test_no_socket_mount_without_flag(self) -> None:
        """socket volume absent when --docker flag not set."""
        result, volumes, _ = self._run_main_with_docker(docker_flag=False, socket_exists=True)
        self.assertEqual(result, 0)
        socket_mount = f"{DEFAULT_DOCKER_SOCKET}:{DEFAULT_DOCKER_SOCKET}"
        self.assertNotIn(socket_mount, volumes)

    def test_fails_when_socket_missing(self) -> None:
        """exits with error when --docker set but socket file doesn't exist."""
        result, volumes, calls = self._run_main_with_docker(docker_flag=True, socket_exists=False)
        self.assertEqual(result, 1)

    def test_no_selinux_suffix_on_socket(self) -> None:
        """socket mount must not have :z or :Z suffix (would break host docker)."""
        result, volumes, _ = self._run_main_with_docker(docker_flag=True, socket_exists=True)
        self.assertEqual(result, 0)
        # find the socket mount entry
        for i, v in enumerate(volumes):
            if DEFAULT_DOCKER_SOCKET in v and i > 0 and volumes[i - 1] == "-v":
                self.assertFalse(v.endswith(":z"), f"socket mount must not have :z suffix: {v}")
                self.assertFalse(v.endswith(":Z"), f"socket mount must not have :Z suffix: {v}")
                self.assertNotIn(",z", v, f"socket mount must not have ,z suffix: {v}")
                self.assertNotIn(",Z", v, f"socket mount must not have ,Z suffix: {v}")
                break

    def test_docker_gid_passed_when_detected_linux(self) -> None:
        """docker_gid is passed to run_docker with host GID on linux."""
        result, _, calls = self._run_main_with_docker(docker_flag=True, socket_exists=True, socket_gid=999)
        self.assertEqual(result, 0)
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["docker_gid"], 999)

    def test_docker_gid_zero_on_macos(self) -> None:
        """docker_gid is always 0 on macOS (docker desktop maps to root)."""
        result, _, calls = self._run_main_with_docker(
            docker_flag=True, socket_exists=True, socket_gid=20, platform_name="Darwin")
        self.assertEqual(result, 0)
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["docker_gid"], 0)

    def test_no_docker_gid_without_docker_flag(self) -> None:
        """docker_gid is None when docker flag is not set."""
        result, _, calls = self._run_main_with_docker(docker_flag=False, socket_exists=True)
        self.assertEqual(result, 0)
        self.assertEqual(len(calls), 1)
        self.assertIsNone(calls[0]["docker_gid"])

    def test_exits_early_when_socket_missing(self) -> None:
        """run_docker not called when socket doesn't exist (fail-fast)."""
        result, _, calls = self._run_main_with_docker(docker_flag=True, socket_exists=False)
        self.assertEqual(result, 1)
        self.assertEqual(len(calls), 0)

    def test_error_when_socket_missing(self) -> None:
        """error printed to stderr and exit 1 when --docker set but socket doesn't exist."""
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--docker", "plan.md"]

            real_exists = os.path.exists

            def fake_exists(path: object) -> bool:
                # only intercept socket path lookups, let everything else through
                if isinstance(path, str) and path.endswith("docker.sock"):
                    return False
                return real_exists(path)

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=lambda *a, **kw: 0):
                                with unittest.mock.patch("ralphex_dk.os.path.exists", side_effect=fake_exists):
                                    with unittest.mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
                                        result = main()

            self.assertEqual(result, 1)
            self.assertIn("error:", mock_stderr.getvalue())
            self.assertIn("not found", mock_stderr.getvalue())

    def test_env_var_enables_docker(self) -> None:
        """RALPHEX_DOCKER_SOCKET=1 enables socket mount without --docker flag."""
        os.environ["RALPHEX_DOCKER_SOCKET"] = "1"
        result, volumes, calls = self._run_main_with_docker(docker_flag=False, socket_exists=True, socket_gid=123)
        self.assertEqual(result, 0)
        socket_mount = f"{DEFAULT_DOCKER_SOCKET}:{DEFAULT_DOCKER_SOCKET}"
        self.assertIn(socket_mount, volumes)
        self.assertEqual(calls[0]["docker_gid"], 123)

    def test_docker_host_custom_socket(self) -> None:
        """DOCKER_HOST=unix:///custom/path uses that socket path instead of default."""
        custom_socket = "/run/user/1000/docker.sock"
        os.environ["DOCKER_HOST"] = f"unix://{custom_socket}"

        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--docker", "plan.md"]

            captured_volumes: list[str] = []
            run_docker_calls: list[dict] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str],
                                docker_gid: int | None = None, **kwargs: object) -> int:
                captured_volumes.extend(volumes)
                run_docker_calls.append({"docker_gid": docker_gid})
                return 0

            _real_exists = os.path.exists

            def fake_exists(p: str) -> bool:
                if p == custom_socket:
                    return True
                return _real_exists(p)

            _real_stat = os.stat

            def fake_stat(p: str, *a: object, **kw: object) -> object:
                if p == custom_socket:
                    mock_st = unittest.mock.MagicMock()
                    mock_st.st_gid = 42
                    return mock_st
                return _real_stat(p, *a, **kw)

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                            with unittest.mock.patch("ralphex_dk.run_docker", side_effect=fake_run_docker):
                                with unittest.mock.patch("ralphex_dk.os.path.exists", side_effect=fake_exists):
                                    with unittest.mock.patch("ralphex_dk.os.stat", side_effect=fake_stat):
                                        with unittest.mock.patch("ralphex_dk.platform.system", return_value="Linux"):
                                            with unittest.mock.patch("sys.stderr", new_callable=io.StringIO):
                                                result = main()

            self.assertEqual(result, 0)
            # custom host socket should be mounted to the default path inside the container
            socket_mount = f"{custom_socket}:{DEFAULT_DOCKER_SOCKET}"
            self.assertIn(socket_mount, captured_volumes)
            self.assertEqual(run_docker_calls[0]["docker_gid"], 42)


class TestBuildDockerCommandDockerGid(unittest.TestCase):
    """tests for DOCKER_GID env var in build_docker_command()."""

    def test_docker_gid_in_command(self) -> None:
        """DOCKER_GID env var appears when docker_gid is provided."""
        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = False
            cmd = build_docker_command(
                image="test:latest", port="8080", volumes=[], env_vars=[],
                bind_port=False, args=[], docker_gid=999,
            )
        self.assertIn("-e", cmd)
        self.assertIn("DOCKER_GID=999", cmd)

    def test_no_docker_gid_when_none(self) -> None:
        """DOCKER_GID absent when docker_gid is None."""
        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = False
            cmd = build_docker_command(
                image="test:latest", port="8080", volumes=[], env_vars=[],
                bind_port=False, args=[], docker_gid=None,
            )
        self.assertNotIn("DOCKER_GID=999", cmd)
        # no DOCKER_GID env var at all
        for i, part in enumerate(cmd):
            self.assertFalse(part.startswith("DOCKER_GID="), f"unexpected DOCKER_GID in cmd: {cmd}")

    def test_docker_gid_before_volumes(self) -> None:
        """DOCKER_GID env var appears before volume mounts in command."""
        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = False
            cmd = build_docker_command(
                image="test:latest", port="8080", volumes=["-v", "/a:/b"], env_vars=[],
                bind_port=False, args=[], docker_gid=42,
            )
        gid_idx = cmd.index("DOCKER_GID=42")
        vol_idx = cmd.index("/a:/b")
        self.assertLess(gid_idx, vol_idx)

    def test_docker_gid_zero(self) -> None:
        """DOCKER_GID=0 works (root group)."""
        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
            mock_stdin.isatty.return_value = False
            cmd = build_docker_command(
                image="test:latest", port="8080", volumes=[], env_vars=[],
                bind_port=False, args=[], docker_gid=0,
            )
        self.assertIn("DOCKER_GID=0", cmd)


class TestDockerLinuxWarning(EnvTestCase):
    """tests for Linux security warning when --docker is used."""
    env_vars = ["RALPHEX_IMAGE", "RALPHEX_PORT", "RALPHEX_EXTRA_ENV",
                "RALPHEX_EXTRA_VOLUMES", "RALPHEX_CLAUDE_PROVIDER", "CLAUDE_CONFIG_DIR",
                "RALPHEX_WEB_HOST", "RALPHEX_DOCKER_SOCKET", "DOCKER_HOST"]
    save_argv = True

    def _run_main_with_platform(self, platform_name: str, socket_exists: bool = True,
                                 socket_gid: int = 999) -> str:
        """helper: run main() with --docker, mocked platform, return stderr output."""
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--docker", "plan.md"]

            _real_exists = os.path.exists

            def fake_exists(p: str) -> bool:
                if p == DEFAULT_DOCKER_SOCKET:
                    return socket_exists
                return _real_exists(p)

            _real_stat = os.stat

            def fake_stat(p: str, *a: object, **kw: object) -> object:
                if p == DEFAULT_DOCKER_SOCKET:
                    mock_st = unittest.mock.MagicMock()
                    mock_st.st_gid = socket_gid
                    return mock_st
                return _real_stat(p, *a, **kw)

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                            with unittest.mock.patch("ralphex_dk.run_docker", return_value=0):
                                with unittest.mock.patch("ralphex_dk.os.path.exists", side_effect=fake_exists):
                                    with unittest.mock.patch("ralphex_dk.os.stat", side_effect=fake_stat):
                                        with unittest.mock.patch("ralphex_dk.platform.system", return_value=platform_name):
                                            with unittest.mock.patch("sys.stderr", new_callable=io.StringIO) as mock_stderr:
                                                main()

            return mock_stderr.getvalue()

    def test_warning_on_linux(self) -> None:
        """warning printed when platform is Linux and socket is mounted."""
        stderr = self._run_main_with_platform("Linux")
        self.assertIn("host Docker socket", stderr)
        self.assertIn("host-level Docker access", stderr)

    def test_no_warning_on_macos(self) -> None:
        """no warning on macOS (VM boundary provides isolation)."""
        stderr = self._run_main_with_platform("Darwin")
        self.assertNotIn("host Docker socket", stderr)
        self.assertNotIn("host-level Docker access", stderr)

    def test_error_instead_of_warning_when_socket_missing(self) -> None:
        """error exit when socket doesn't exist, no Linux warning reached."""
        stderr = self._run_main_with_platform("Linux", socket_exists=False)
        self.assertIn("error:", stderr)
        self.assertNotIn("host-level Docker access", stderr)


class TestDryRunDocker(EnvTestCase):
    """tests for --dry-run with --docker flag."""
    env_vars = ["RALPHEX_IMAGE", "RALPHEX_PORT", "RALPHEX_EXTRA_ENV",
                "RALPHEX_EXTRA_VOLUMES", "RALPHEX_CLAUDE_PROVIDER", "CLAUDE_CONFIG_DIR",
                "RALPHEX_WEB_HOST", "RALPHEX_DOCKER_SOCKET", "DOCKER_HOST"]
    save_argv = True

    def test_dry_run_shows_socket_mount_and_docker_gid(self) -> None:
        """--dry-run --docker shows socket mount and DOCKER_GID in output."""
        import shlex

        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            claude_home = tmp / ".claude"
            claude_home.mkdir()
            (claude_home / ".credentials.json").write_text("{}")

            sys.argv = ["ralphex-dk", "--dry-run", "--docker"]

            _real_exists = os.path.exists

            def fake_exists(p: str) -> bool:
                if p == DEFAULT_DOCKER_SOCKET:
                    return True
                return _real_exists(p)

            _real_stat = os.stat

            def fake_stat(p: str, *a: object, **kw: object) -> object:
                if p == DEFAULT_DOCKER_SOCKET:
                    mock_st = unittest.mock.MagicMock()
                    mock_st.st_gid = 123
                    return mock_st
                return _real_stat(p, *a, **kw)

            with unittest.mock.patch("ralphex_dk.Path.home", return_value=tmp):
                with unittest.mock.patch("ralphex_dk.os.getcwd", return_value=str(tmp)):
                    with unittest.mock.patch.dict(os.environ, {"PWD": str(tmp)}, clear=False):
                        with unittest.mock.patch("ralphex_dk.sys.stdin") as mock_stdin:
                            mock_stdin.isatty.return_value = False
                            with unittest.mock.patch("ralphex_dk.extract_macos_credentials", return_value=None):
                                with unittest.mock.patch("ralphex_dk.os.path.exists", side_effect=fake_exists):
                                    with unittest.mock.patch("ralphex_dk.os.stat", side_effect=fake_stat):
                                        with unittest.mock.patch("ralphex_dk.platform.system", return_value="Linux"):
                                            with unittest.mock.patch("sys.stdout", new_callable=io.StringIO) as mock_stdout:
                                                with unittest.mock.patch("sys.stderr", new_callable=io.StringIO):
                                                    result = main()

            self.assertEqual(result, 0)
            output = mock_stdout.getvalue().strip()
            parts = shlex.split(output)

            # verify socket mount is in the command
            socket_mount = f"{DEFAULT_DOCKER_SOCKET}:{DEFAULT_DOCKER_SOCKET}"
            self.assertIn(socket_mount, parts)

            # verify DOCKER_GID=123 is passed as env var in the command
            self.assertIn("DOCKER_GID=123", parts)


def run_tests() -> None:
    """run all unit tests."""
    loader = unittest.TestLoader()
    suite = unittest.TestSuite()
    for tc in [TestResolvePath, TestSymlinkTargetDirs, TestShouldBindPort, TestBuildVolumes,
               TestBuildVolumesGitignore, TestDetectGitWorktree, TestDetectTimezone,
               TestExtractCredentials, TestScheduleCleanup,
               TestBuildDockerCmd, TestKeychainServiceName, TestBuildVolumesClaudeHome,
               TestExtractCredentialsClaudeHome, TestSelinuxEnabled, TestSelinuxVolumeSuffix,
               TestClaudeConfigDirEnv, TestIsSensitiveName, TestBuildEnvVars,
               TestMergeEnvFlags, TestMergeVolumeFlags, TestBuildParser,
               TestMainArgparse, TestHelpFlag, TestClaudeProvider, TestDockerEnabled, TestResolveDockerSocket, TestAwsCredentialExport,
               TestBedrockSkipKeychain, TestBedrockValidation, TestParseEnvFlags, TestExtractEnvFromFlags,
               TestBuildDockerCommand, TestDetectInheritedEnvVars, TestDetectExplicitSecrets, TestDryRun,
               TestDockerSocketGid, TestDockerSocketMount, TestBuildDockerCommandDockerGid,
               TestDockerLinuxWarning, TestDryRunDocker, TestDockerNetwork]:
        suite.addTests(loader.loadTestsFromTestCase(tc))
    runner = unittest.TextTestRunner(verbosity=2)
    result = runner.run(suite)
    if not result.wasSuccessful():
        sys.exit(1)


if __name__ == "__main__":
    run_tests()
