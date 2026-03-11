#!/usr/bin/env python3
"""ralphex-dk.sh - run ralphex in a docker container

Usage: ralphex-dk.sh [wrapper-flags] [ralphex-args]
       ralphex-dk.sh [wrapper-flags] -- [ralphex-args]

Wrapper-specific flags (parsed by this script):
  -E, --env VAR[=val]        extra env var to pass to container (repeatable)
  -v, --volume src:dst[:opts] extra volume mount (repeatable)
  --update                   pull latest Docker image and exit
  --update-script            update this wrapper script and exit
  --test                     run embedded unit tests and exit
  -h, --help                 show wrapper + ralphex help, then exit

All other arguments are passed through to ralphex inside the container.
Use -- to explicitly separate wrapper flags from ralphex args.

Examples:
  ralphex-dk.sh docs/plans/feature.md
  ralphex-dk.sh --serve docs/plans/feature.md
  ralphex-dk.sh --review
  ralphex-dk.sh -v /data:/mnt/data:ro docs/plans/feature.md
  ralphex-dk.sh -E DEBUG=1 -E API_KEY docs/plans/feature.md
  ralphex-dk.sh -E FOO -- -v /ignored:path plan.md   # -v goes to ralphex
  ralphex-dk.sh --update
  ralphex-dk.sh --update-script

Environment variables:
  RALPHEX_IMAGE         Docker image (default: ghcr.io/umputun/ralphex-go:latest)
  RALPHEX_PORT          Web dashboard port with --serve (default: 8080)
  RALPHEX_EXTRA_ENV     Comma-separated env vars (VAR=value or VAR to inherit)
  RALPHEX_EXTRA_VOLUMES Comma-separated volume mounts (src:dst[:opts])
  GITHUB_TOKEN          GitHub token (passed through to container if set)

Note: RALPHEX_EXTRA_ENV emits warnings for sensitive names (KEY, SECRET, TOKEN,
etc.) with explicit values. Values containing commas must use -E flag instead.
"""

import argparse
import difflib
import os
import platform
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import textwrap
import threading
import unittest
import unittest.mock
from pathlib import Path
from types import FrameType
from typing import Optional
from urllib.request import urlopen

DEFAULT_IMAGE = "ghcr.io/umputun/ralphex-go:latest"
DEFAULT_PORT = "8080"
SCRIPT_URL = "https://raw.githubusercontent.com/umputun/ralphex/master/scripts/ralphex-dk.sh"
SENSITIVE_PATTERNS = ["KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH"]


def build_parser() -> argparse.ArgumentParser:
    """build argparse parser for wrapper-specific flags."""
    parser = argparse.ArgumentParser(
        prog="ralphex-dk",
        description="Run ralphex in a Docker container",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        add_help=False,
        allow_abbrev=False,
        epilog=textwrap.dedent("""\
            Environment variables:
              RALPHEX_IMAGE         Docker image (default: ghcr.io/umputun/ralphex-go:latest)
              RALPHEX_PORT          Web dashboard port with --serve (default: 8080)
              RALPHEX_EXTRA_ENV     Comma-separated env vars (VAR=value or VAR)
              RALPHEX_EXTRA_VOLUMES Comma-separated volume mounts (src:dst[:opts])
              GITHUB_TOKEN          GitHub token (passed through to container if set)

            All other arguments are passed through to ralphex.
        """),
    )
    parser.add_argument("-E", "--env", action="append", default=[], metavar="VAR[=val]",
                        help="extra env var to pass to container (can be repeated)")
    parser.add_argument("-v", "--volume", action="append", default=[], metavar="src:dst[:opts]",
                        help="extra volume mount (can be repeated)")
    parser.add_argument("--update", action="store_true",
                        help="pull latest Docker image and exit")
    parser.add_argument("--update-script", action="store_true",
                        help="update this wrapper script and exit")
    parser.add_argument("--test", action="store_true",
                        help="run embedded unit tests and exit")
    parser.add_argument("-h", "--help", action="store_true", dest="help",
                        help="show this help and ralphex help, then exit")
    return parser


def selinux_enabled() -> bool:
    """check if SELinux is enabled (Linux only). Returns True when SELinux is active (enforcing or permissive)."""
    if platform.system() != "Linux":
        return False
    return Path("/sys/fs/selinux/enforce").exists()


def is_sensitive_name(name: str) -> bool:
    """check if env var name contains sensitive patterns at word boundaries."""
    upper = name.upper()
    for pattern in SENSITIVE_PATTERNS:
        # check ALL occurrences of pattern, not just the first
        start = 0
        while True:
            idx = upper.find(pattern, start)
            if idx == -1:
                break
            # check left boundary: start of string or underscore
            left_ok = idx == 0 or upper[idx - 1] == "_"
            # check right boundary: end of string or underscore
            end = idx + len(pattern)
            right_ok = end == len(upper) or upper[end] == "_"
            if left_ok and right_ok:
                return True
            start = idx + 1  # move past this occurrence and try again
    return False


def resolve_path(path: Path) -> Path:
    """if symlink, resolve; otherwise return as-is."""
    if path.is_symlink():
        try:
            return path.resolve()
        except (OSError, RuntimeError):
            return path
    return path


def symlink_target_dirs(src: Path, maxdepth: int = 2) -> list[Path]:
    """collect unique parent directories of symlink targets inside a directory, limited to maxdepth."""
    if not src.is_dir():
        return []
    dirs: set[Path] = set()
    src_str = str(src)
    for root, dirnames, filenames in os.walk(src):
        depth = root[len(src_str):].count(os.sep)
        if depth >= maxdepth:
            dirnames.clear()  # don't descend further
            continue  # skip entries at this level to match find -maxdepth behavior
        if depth >= maxdepth - 1:
            entries = list(dirnames) + filenames  # save dirnames before clearing
            dirnames.clear()  # don't descend further, but still process entries at this level
        else:
            entries = list(dirnames) + filenames
        root_path = Path(root)
        for name in entries:
            entry = root_path / name
            if entry.is_symlink():
                try:
                    target = entry.resolve()
                    dirs.add(target.parent)
                except (OSError, RuntimeError):
                    continue
    return sorted(dirs)


def should_bind_port(args: list[str]) -> bool:
    """check for --serve or -s in arguments."""
    return "--serve" in args or "-s" in args


def detect_timezone() -> str:
    """detect host timezone for container. checks TZ env, /etc/timezone, timedatectl, defaults to UTC."""
    tz = os.environ.get("TZ", "")
    if tz:
        return tz
    try:
        tz = Path("/etc/timezone").read_text().strip()
        if tz:
            return tz
    except OSError:
        pass
    try:
        # try reading /etc/localtime symlink target (common on macOS and many Linux distros)
        link = os.readlink("/etc/localtime")
        # extract timezone from path like /usr/share/zoneinfo/America/New_York
        marker = "zoneinfo/"
        idx = link.find(marker)
        if idx >= 0:
            return link[idx + len(marker):]
    except OSError:
        pass
    return "UTC"


def detect_git_worktree(workspace: Path) -> Optional[Path]:
    """check if .git is a file (worktree), return absolute path to git common dir."""
    git_path = workspace / ".git"
    if not git_path.is_file():
        return None
    try:
        result = subprocess.run(
            ["git", "-C", str(workspace), "rev-parse", "--git-common-dir"],
            capture_output=True, text=True, check=False,
        )
        if result.returncode != 0 or not result.stdout.strip():
            return None
        common_dir = Path(result.stdout.strip())
        if not common_dir.is_absolute():
            common_dir = (workspace / common_dir).resolve()
        if common_dir.is_dir():
            return common_dir
    except OSError:
        pass
    return None


def get_global_gitignore() -> Optional[Path]:
    """run git config --global core.excludesFile and return path if it exists."""
    try:
        result = subprocess.run(
            ["git", "config", "--global", "core.excludesFile"],
            capture_output=True, text=True, check=False,
        )
        if result.returncode == 0 and result.stdout.strip():
            p = Path(result.stdout.strip()).expanduser()
            if p.exists():
                return p
    except OSError:
        pass
    return None


def build_volumes() -> list[str]:
    """build docker volume mount arguments, returns flat list like ['-v', 'src:dst', ...]."""
    home = Path.home()
    # use logical PWD when available to preserve symlinks (matches previous bash wrapper behavior)
    pwd_env = os.environ.get("PWD")
    cwd = Path(pwd_env) if pwd_env else Path(os.getcwd())
    copilot_home = home / ".copilot"
    vols: list[str] = []
    selinux = selinux_enabled()

    def add(src: Path, dst: str, ro: bool = False) -> None:
        opts: list[str] = []
        if ro:
            opts.append("ro")
        if selinux:
            opts.append("z")
        suffix = ":" + ",".join(opts) if opts else ""
        vols.extend(["-v", f"{src}:{dst}{suffix}"])

    def add_symlink_targets(src: Path) -> None:
        """add read-only mounts for symlink targets that live under $HOME."""
        for target in symlink_target_dirs(src):
            if target.is_dir() and target.is_relative_to(home):
                add(target, str(target), ro=True)

    # 1. ~/.copilot (resolved) -> /mnt/copilot:ro
    if copilot_home.is_dir():
        add(resolve_path(copilot_home), "/mnt/copilot", ro=True)
        add_symlink_targets(copilot_home)

    # 2. cwd -> /workspace
    add(cwd, "/workspace")

    # 3. git worktree common dir
    git_common = detect_git_worktree(cwd)
    if git_common:
        add(git_common, str(git_common))

    # 4. ~/.config/ralphex -> /home/app/.config/ralphex + symlink targets
    ralphex_config = home / ".config" / "ralphex"
    if ralphex_config.is_dir():
        add(resolve_path(ralphex_config), "/home/app/.config/ralphex")
        add_symlink_targets(ralphex_config)

    # 5. .ralphex/ symlink targets only (workspace mount already includes it)
    local_ralphex = cwd / ".ralphex"
    if local_ralphex.is_dir():
        add_symlink_targets(local_ralphex)

    # 6. ~/.gitconfig -> /home/app/.gitconfig:ro
    gitconfig = home / ".gitconfig"
    if gitconfig.exists():
        add(resolve_path(gitconfig), "/home/app/.gitconfig", ro=True)

    # 7. global gitignore -> remap home-relative paths to /home/app/
    # mount at both remapped path (for tilde refs in .gitconfig) and original
    # absolute path (for expanded absolute refs like /Users/alice/.gitignore)
    global_gitignore = get_global_gitignore()
    if global_gitignore:
        src = resolve_path(global_gitignore)
        if global_gitignore.is_relative_to(home):
            dst = "/home/app/" + str(global_gitignore.relative_to(home))
            add(src, dst, ro=True)
            # also mount at original absolute path so .gitconfig absolute refs work
            original = str(global_gitignore)
            if original != dst:
                add(src, original, ro=True)
        else:
            dst = str(global_gitignore)
            add(src, dst, ro=True)

    # note: RALPHEX_EXTRA_VOLUMES is handled by merge_volume_flags() in main()
    # to properly merge with CLI -v flags. do not duplicate processing here.

    return vols


# regex for valid env var name with optional =value
ENV_VAR_PATTERN = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*(=.*)?$")


def validate_env_entry(entry: str, warn_invalid: bool = False) -> Optional[str]:
    """validate a single env var entry. returns entry if valid, None if invalid."""
    if not ENV_VAR_PATTERN.match(entry):
        if warn_invalid:
            print(f"warning: skipping invalid env var: {entry}", file=sys.stderr)
        return None
    if "=" in entry:
        name = entry.split("=", 1)[0]
        if is_sensitive_name(name):
            print(f"warning: {name} has explicit value - use -E {name} to inherit from host for better security", file=sys.stderr)
    return entry


def build_env_vars() -> list[str]:
    """build docker -e flags from RALPHEX_EXTRA_ENV env var."""
    extra = os.environ.get("RALPHEX_EXTRA_ENV", "")
    if not extra:
        return []

    result: list[str] = []
    for entry in extra.split(","):
        entry = entry.strip()
        if entry and (validated := validate_env_entry(entry)):
            result.extend(["-e", validated])
    return result


def merge_env_flags(args_env: list[str]) -> list[str]:
    """merge RALPHEX_EXTRA_ENV with CLI -E flags, validate entries.

    env var entries come first, CLI entries append. invalid entries are skipped
    with a warning.
    """
    result: list[str] = []
    # env var entries first
    result.extend(build_env_vars())
    # cli entries append (with validation)
    for entry in args_env:
        if validated := validate_env_entry(entry, warn_invalid=True):
            result.extend(["-e", validated])
    return result


def merge_volume_flags(args_volume: list[str]) -> list[str]:
    """merge RALPHEX_EXTRA_VOLUMES with CLI -v flags, validate entries.

    env var entries come first, CLI entries append. entries without ':'
    are silently skipped (matching current behavior).
    """
    result: list[str] = []
    # env var entries first
    extra = os.environ.get("RALPHEX_EXTRA_VOLUMES", "")
    for mount in extra.split(","):
        mount = mount.strip()
        if mount and ":" in mount:
            result.extend(["-v", mount])
    # cli entries append (with validation)
    for mount in args_volume:
        if ":" in mount:
            result.extend(["-v", mount])
    return result


def handle_update(image: str) -> int:
    """pull latest docker image."""
    print(f"pulling latest image: {image}", file=sys.stderr)
    return subprocess.run(["docker", "pull", image], check=False).returncode


def handle_update_script(script_path: Path) -> int:
    """download latest wrapper script, show diff, prompt user to update."""
    print("checking for ralphex docker wrapper updates...", file=sys.stderr)
    fd, tmp_path = tempfile.mkstemp()
    try:
        # download
        fd_closed = False
        try:
            with urlopen(SCRIPT_URL, timeout=30) as resp:  # noqa: S310
                data = resp.read()
            with os.fdopen(fd, "wb") as f:
                fd_closed = True
                f.write(data)
        except OSError:
            if not fd_closed:
                os.close(fd)
            print("warning: failed to check for wrapper updates", file=sys.stderr)
            return 0

        # compare
        try:
            current = script_path.read_text()
            new = Path(tmp_path).read_text()
        except OSError:
            print("warning: failed to read script files for comparison", file=sys.stderr)
            return 0

        if current == new:
            print("wrapper is up to date", file=sys.stderr)
            return 0

        print("wrapper update available:", file=sys.stderr)
        # try git diff first (output to stderr like bash original), fall back to difflib
        try:
            git_diff = subprocess.run(
                ["git", "diff", "--no-index", str(script_path), tmp_path],
                check=False, stdout=sys.stderr,
            )
            git_diff_failed = git_diff.returncode > 1
        except OSError:
            git_diff_failed = True
        if git_diff_failed:
            # git diff not available or error, use difflib
            diff = difflib.unified_diff(
                current.splitlines(keepends=True), new.splitlines(keepends=True),
                fromfile=str(script_path), tofile="(new)",
            )
            sys.stderr.writelines(diff)

        sys.stderr.write("update wrapper? (y/N) ")
        sys.stderr.flush()
        answer = sys.stdin.readline()  # returns "" on EOF, treated as "no"

        if answer.strip().lower() == "y":
            shutil.copy2(tmp_path, str(script_path))
            script_path.chmod(script_path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
            print("wrapper updated", file=sys.stderr)
        else:
            print("wrapper update skipped", file=sys.stderr)
    finally:
        try:
            os.unlink(tmp_path)
        except OSError:
            pass
    return 0


def build_base_env_vars() -> list[str]:
    """build base docker environment variable flags shared by all docker commands."""
    return [
        "-e", f"APP_UID={os.getuid()}",
        "-e", f"TIME_ZONE={detect_timezone()}",
        "-e", "SKIP_HOME_CHOWN=1",
        "-e", "INIT_QUIET=1",
    ]


def run_docker(image: str, port: str, volumes: list[str], env_vars: list[str], bind_port: bool, args: list[str]) -> int:
    """build and execute docker run command."""
    cmd = ["docker", "run"]

    interactive = sys.stdin.isatty()
    if interactive:
        cmd.append("-it")
    cmd.append("--rm")

    cmd.extend(build_base_env_vars())

    # add extra env vars from RALPHEX_EXTRA_ENV and -e/--env CLI flags
    cmd.extend(env_vars)

    if bind_port:
        cmd.extend(["-p", f"127.0.0.1:{port}:8080"])
        if "RALPHEX_WEB_HOST" not in os.environ:
            cmd.extend(["-e", "RALPHEX_WEB_HOST=0.0.0.0"])

    cmd.extend(volumes)
    cmd.extend(["-w", "/workspace"])
    cmd.extend([image, "/srv/ralphex"])
    cmd.extend(args)

    # defer SIGTERM during Popen+assignment to prevent race where handler sees _active_proc unset.
    # using a deferred handler instead of SIG_IGN so the signal is not lost.
    _pending_sigterm: list[tuple[int, "FrameType | None"]] = []

    def _deferred_term(signum: int, frame: "FrameType | None") -> None:
        _pending_sigterm.append((signum, frame))

    old_handler = signal.signal(signal.SIGTERM, _deferred_term)
    try:
        proc = subprocess.Popen(cmd)  # noqa: S603
        run_docker._active_proc = proc  # type: ignore[attr-defined]
    finally:
        signal.signal(signal.SIGTERM, old_handler)
    # re-deliver deferred signal now that _active_proc is set and real handler is restored
    if _pending_sigterm and callable(old_handler):
        old_handler(*_pending_sigterm[0])

    def _terminate_proc() -> None:
        try:
            proc.terminate()
        except ProcessLookupError:
            pass
    try:
        proc.wait()
    except KeyboardInterrupt:
        _terminate_proc()
        proc.wait()
    finally:
        run_docker._active_proc = None  # type: ignore[attr-defined]
    return proc.returncode


def main() -> int:
    """entry point."""
    # parse wrapper-specific flags using argparse
    parser = build_parser()
    parsed, ralphex_args = parser.parse_known_args(sys.argv[1:])

    # handle --test flag
    if parsed.test:
        run_tests()
        return 0

    image = os.environ.get("RALPHEX_IMAGE", DEFAULT_IMAGE)
    port = os.environ.get("RALPHEX_PORT", DEFAULT_PORT)

    # handle --update
    if parsed.update:
        return handle_update(image)

    # handle --update-script
    if parsed.update_script:
        script_path = Path(os.path.realpath(sys.argv[0]))
        return handle_update_script(script_path)

    # handle --help: show wrapper help, then try container help
    if parsed.help:
        parser.print_help()
        print("\n" + "-" * 70)
        print("ralphex options (from container):\n")
        volumes = build_volumes()
        cmd = ["docker", "run", "--rm"]
        cmd.extend(build_base_env_vars())
        cmd.extend(volumes)
        cmd.extend(["-w", "/workspace"])
        cmd.extend([image, "/srv/ralphex", "--help"])
        return subprocess.run(cmd, check=False).returncode

    # merge env var entries with CLI -E/--env flags (env first, CLI appends)
    extra_env = merge_env_flags(parsed.env)

    # pass GITHUB_TOKEN to container if set in host environment,
    # but only if user hasn't already specified it via -E/RALPHEX_EXTRA_ENV
    github_token = os.environ.get("GITHUB_TOKEN", "")
    if github_token and not any(e == "GITHUB_TOKEN" or e.startswith("GITHUB_TOKEN=") for e in extra_env if e != "-e"):
        extra_env.extend(["-e", "GITHUB_TOKEN"])

    # merge env var entries with CLI -v/--volume flags (env first, CLI appends)
    extra_volumes = merge_volume_flags(parsed.volume)

    # setup SIGTERM handler: terminate docker child process
    def _term_handler(signum: int, frame: object) -> None:
        proc = getattr(run_docker, "_active_proc", None)
        if proc is not None:
            try:
                proc.terminate()
            except ProcessLookupError:
                pass
        sys.exit(128 + signum)

    signal.signal(signal.SIGTERM, _term_handler)

    # build volumes (base + extra from env var + CLI)
    volumes = build_volumes()
    volumes.extend(extra_volumes)

    print(f"using image: {image}", file=sys.stderr)

    # determine port binding
    bind_port = should_bind_port(ralphex_args)

    return run_docker(image, port, volumes, extra_env, bind_port, ralphex_args)


# --- embedded tests ---


def run_tests() -> None:
    """run embedded unit tests."""

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
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                vols = build_volumes()
            # volumes should come in -v pairs
            for i in range(0, len(vols), 2):
                self.assertEqual(vols[i], "-v")
                self.assertIn(":", vols[i + 1])

        def test_includes_workspace_without_selinux(self) -> None:
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                vols = build_volumes()
                pwd_env = os.environ.get("PWD")
                cwd = Path(pwd_env) if pwd_env else Path(os.getcwd())
                self.assertIn(f"{cwd}:/workspace", vols)

        def test_includes_workspace_with_selinux(self) -> None:
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=True):
                vols = build_volumes()
                pwd_env = os.environ.get("PWD")
                cwd = Path(pwd_env) if pwd_env else Path(os.getcwd())
                self.assertIn(f"{cwd}:/workspace:z", vols)

        def test_includes_copilot_dir_when_present(self) -> None:
            tmp = Path(tempfile.mkdtemp()).resolve()
            try:
                copilot_dir = tmp / ".copilot"
                copilot_dir.mkdir()
                with (
                    unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False),
                    unittest.mock.patch.object(Path, "home", return_value=tmp),
                ):
                    vols = build_volumes()
                found = any("/mnt/copilot:ro" in v for v in vols)
                self.assertTrue(found, "should mount ~/.copilot to /mnt/copilot:ro")
            finally:
                shutil.rmtree(tmp)

        def test_includes_copilot_dir_with_selinux(self) -> None:
            tmp = Path(tempfile.mkdtemp()).resolve()
            try:
                copilot_dir = tmp / ".copilot"
                copilot_dir.mkdir()
                with (
                    unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=True),
                    unittest.mock.patch.object(Path, "home", return_value=tmp),
                ):
                    vols = build_volumes()
                found = any("/mnt/copilot:ro,z" in v for v in vols)
                self.assertTrue(found, "should mount ~/.copilot to /mnt/copilot:ro,z")
            finally:
                shutil.rmtree(tmp)

        def test_no_copilot_mount_when_missing(self) -> None:
            tmp = Path(tempfile.mkdtemp()).resolve()
            try:
                # do NOT create .copilot directory
                with (
                    unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False),
                    unittest.mock.patch.object(Path, "home", return_value=tmp),
                ):
                    vols = build_volumes()
                found = any("/mnt/copilot" in v for v in vols)
                self.assertFalse(found, "should not mount /mnt/copilot when ~/.copilot is missing")
            finally:
                shutil.rmtree(tmp)

    class TestBuildVolumesGitignore(unittest.TestCase):
        def test_global_gitignore_remapped_to_home_app(self) -> None:
            """global gitignore under $HOME should be mounted at /home/app/<relative>."""
            home = Path.home()
            fake_ignore = home / ".gitignore"
            with (
                unittest.mock.patch(f"{__name__}.get_global_gitignore", return_value=fake_ignore),
                unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False),
            ):
                vols = build_volumes()
            expected_dst = "/home/app/.gitignore"
            found = any(expected_dst + ":ro" in v for v in vols)
            self.assertTrue(found, f"expected mount destination {expected_dst}:ro in volumes, got {vols}")

        def test_global_gitignore_also_mounted_at_original_absolute_path(self) -> None:
            """gitignore under $HOME should also be mounted at original absolute path for .gitconfig refs."""
            home = Path.home()
            fake_ignore = home / ".gitignore_global"
            with (
                unittest.mock.patch(f"{__name__}.get_global_gitignore", return_value=fake_ignore),
                unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False),
            ):
                vols = build_volumes()
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
                unittest.mock.patch(f"{__name__}.get_global_gitignore", return_value=fake_ignore),
                unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False),
                unittest.mock.patch(f"{__name__}.resolve_path", side_effect=lambda p: p),
            ):
                vols = build_volumes()
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
            """verify TIME_ZONE env var is included in docker command."""
            old = os.environ.get("TZ")
            try:
                os.environ["TZ"] = "Asia/Tokyo"
                # build a minimal docker command and check TIME_ZONE is set
                cmd = ["-e", f"TIME_ZONE={detect_timezone()}"]
                self.assertIn("-e", cmd)
                self.assertIn("TIME_ZONE=Asia/Tokyo", cmd)
            finally:
                if old is None:
                    os.environ.pop("TZ", None)
                else:
                    os.environ["TZ"] = old

    class TestSelinuxEnabled(unittest.TestCase):
        def test_returns_false_on_non_linux(self) -> None:
            """selinux_enabled returns False on non-Linux."""
            with unittest.mock.patch(f"{__name__}.platform") as mock_platform:
                mock_platform.system.return_value = "Darwin"
                self.assertFalse(selinux_enabled())

        def test_returns_false_when_enforce_missing(self) -> None:
            """selinux_enabled returns False when /sys/fs/selinux/enforce does not exist."""
            with unittest.mock.patch(f"{__name__}.platform") as mock_platform, \
                 unittest.mock.patch(f"{__name__}.Path") as mock_path:
                mock_platform.system.return_value = "Linux"
                mock_path.return_value.exists.return_value = False
                self.assertFalse(selinux_enabled())

        def test_returns_true_when_enforce_exists(self) -> None:
            """selinux_enabled returns True when /sys/fs/selinux/enforce exists."""
            with unittest.mock.patch(f"{__name__}.platform") as mock_platform, \
                 unittest.mock.patch(f"{__name__}.Path") as mock_path:
                mock_platform.system.return_value = "Linux"
                mock_path.return_value.exists.return_value = True
                self.assertTrue(selinux_enabled())

    class TestSelinuxVolumeSuffix(unittest.TestCase):
        def test_z_label_in_volumes_when_selinux(self) -> None:
            """volume mounts include :z label when SELinux is enabled."""
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=True):
                vols = build_volumes()
            for i in range(1, len(vols), 2):
                has_z = vols[i].endswith(":z") or ",z" in vols[i]
                self.assertTrue(has_z, f"volume {vols[i]} missing :z SELinux label")

        def test_no_z_label_without_selinux(self) -> None:
            """volume mounts omit :z label when SELinux is not enabled."""
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                vols = build_volumes()
            for i in range(1, len(vols), 2):
                self.assertNotIn(",z", vols[i])
                self.assertFalse(vols[i].endswith(":z"),
                                 f"volume {vols[i]} should not have :z without SELinux")

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

        def test_no_claude_provider_flag(self) -> None:
            """--claude-provider is no longer a recognized flag, passes through to ralphex."""
            parser = build_parser()
            args, unknown = parser.parse_known_args(["--claude-provider", "bedrock"])
            self.assertEqual(unknown, ["--claude-provider", "bedrock"])

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

    class TestMainArgparse(EnvTestCase):
        """tests for main() argparse integration."""
        env_vars = ["RALPHEX_IMAGE", "RALPHEX_PORT", "RALPHEX_EXTRA_ENV",
                    "RALPHEX_EXTRA_VOLUMES", "GITHUB_TOKEN", "GITHUB_TOKEN_FILE"]
        save_argv = True

        def test_update_flag_triggers_handle_update(self) -> None:
            """--update calls handle_update with image."""
            calls: list[str] = []
            with unittest.mock.patch("__main__.handle_update", side_effect=lambda img: (calls.append(img), 0)[1]):
                sys.argv = ["ralphex-dk", "--update"]
                result = main()
            self.assertEqual(calls, [DEFAULT_IMAGE])
            self.assertEqual(result, 0)

        def test_update_with_custom_image(self) -> None:
            """--update uses RALPHEX_IMAGE env var."""
            os.environ["RALPHEX_IMAGE"] = "custom:latest"
            calls: list[str] = []
            with unittest.mock.patch("__main__.handle_update", side_effect=lambda img: (calls.append(img), 0)[1]):
                sys.argv = ["ralphex-dk", "--update"]
                result = main()
            self.assertEqual(calls, ["custom:latest"])
            self.assertEqual(result, 0)

        def test_update_script_flag_triggers_handle_update_script(self) -> None:
            """--update-script calls handle_update_script."""
            calls: list[Path] = []
            with unittest.mock.patch("__main__.handle_update_script", side_effect=lambda p: (calls.append(p), 0)[1]):
                sys.argv = ["ralphex-dk", "--update-script"]
                result = main()
            self.assertEqual(len(calls), 1)
            self.assertEqual(result, 0)

        def test_env_flags_build_cli_env(self) -> None:
            """CLI -E/--env flags are converted to docker -e flags."""
            captured_env: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_env.extend(env_vars)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                sys.argv = ["ralphex-dk", "-E", "FOO=bar", "--env", "BAZ", "plan.md"]
                result = main()

            self.assertEqual(result, 0)
            self.assertIn("-e", captured_env)
            self.assertIn("FOO=bar", captured_env)
            self.assertIn("BAZ", captured_env)

        def test_volume_flags_build_cli_volumes(self) -> None:
            """CLI -v/--volume flags are added to volume list."""
            captured_volumes: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_volumes.extend(volumes)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                sys.argv = ["ralphex-dk", "-v", "/a:/b", "--volume", "/c:/d:ro", "plan.md"]
                result = main()

            self.assertEqual(result, 0)
            self.assertIn("-v", captured_volumes)
            self.assertIn("/a:/b", captured_volumes)
            self.assertIn("/c:/d:ro", captured_volumes)

        def test_ralphex_args_pass_through(self) -> None:
            """unknown args pass through to run_docker."""
            captured_args: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_args.extend(args)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                sys.argv = ["ralphex-dk", "--serve", "plan.md", "--review"]
                result = main()

            self.assertEqual(result, 0)
            self.assertEqual(captured_args, ["--serve", "plan.md", "--review"])

        def test_double_dash_delimiter_pass_through(self) -> None:
            """args after -- pass through unchanged to ralphex, including -- itself."""
            captured_args: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_args.extend(args)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                # -E FOO is consumed, but -v after -- is NOT consumed
                sys.argv = ["ralphex-dk", "-E", "FOO", "--", "-v", "/ignored", "plan.md"]
                result = main()

            self.assertEqual(result, 0)
            # -- and everything after it passes through
            self.assertEqual(captured_args, ["--", "-v", "/ignored", "plan.md"])

        def test_lowercase_e_passes_to_ralphex(self) -> None:
            """-e (ralphex's external-only flag) passes through to ralphex."""
            captured_args: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_args.extend(args)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                sys.argv = ["ralphex-dk", "-e", "plan.md"]
                result = main()

            self.assertEqual(result, 0)
            self.assertEqual(captured_args, ["-e", "plan.md"])

        def test_mixed_wrapper_and_ralphex_args(self) -> None:
            """wrapper args are separated from ralphex args correctly."""
            captured_args: list[str] = []
            captured_env: list[str] = []
            captured_volumes: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_args.extend(args)
                captured_env.extend(env_vars)
                captured_volumes.extend(volumes)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                sys.argv = ["ralphex-dk", "-E", "DEBUG=1", "--serve", "-v", "/data:/mnt", "plan.md", "-e"]
                result = main()

            self.assertEqual(result, 0)
            # wrapper args consumed
            self.assertIn("DEBUG=1", captured_env)
            self.assertIn("/data:/mnt", captured_volumes)
            # ralphex args passed through
            self.assertEqual(captured_args, ["--serve", "plan.md", "-e"])

        def test_invalid_env_entries_skipped_with_warning(self) -> None:
            """invalid -E entries are skipped with warning."""
            captured_env: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_env.extend(env_vars)
                return 0

            import io
            captured_stderr = io.StringIO()
            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    sys.argv = ["ralphex-dk", "-E", "=invalid", "-E", "VALID=val"]
                    result = main()

            self.assertEqual(result, 0)
            # only valid entry is included
            self.assertIn("VALID=val", captured_env)
            self.assertNotIn("=invalid", captured_env)
            # warning printed
            warning = captured_stderr.getvalue()
            self.assertIn("=invalid", warning)

        def test_github_token_passed_when_set(self) -> None:
            """GITHUB_TOKEN in host env is passed to container."""
            os.environ["GITHUB_TOKEN"] = "ghp_test_token"
            captured_env: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_env.extend(env_vars)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                sys.argv = ["ralphex-dk", "plan.md"]
                result = main()

            self.assertEqual(result, 0)
            self.assertIn("GITHUB_TOKEN", captured_env)

        def test_github_token_not_passed_when_unset(self) -> None:
            """GITHUB_TOKEN absent in host env is not added to container."""
            os.environ.pop("GITHUB_TOKEN", None)
            captured_env: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_env.extend(env_vars)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                sys.argv = ["ralphex-dk", "plan.md"]
                result = main()

            self.assertEqual(result, 0)
            self.assertNotIn("GITHUB_TOKEN", captured_env)

        def test_github_token_prefix_not_passed(self) -> None:
            """GITHUB_TOKEN_FILE in host env is NOT treated as GITHUB_TOKEN."""
            os.environ.pop("GITHUB_TOKEN", None)
            os.environ["GITHUB_TOKEN_FILE"] = "/run/secrets/gh_token"
            captured_env: list[str] = []

            def fake_run_docker(image: str, port: str, volumes: list[str],
                                env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                captured_env.extend(env_vars)
                return 0

            with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                sys.argv = ["ralphex-dk", "plan.md"]
                result = main()

            self.assertEqual(result, 0)
            # GITHUB_TOKEN_FILE should NOT cause GITHUB_TOKEN to be passed
            self.assertNotIn("GITHUB_TOKEN", captured_env)
            self.assertNotIn("GITHUB_TOKEN_FILE", captured_env)

    class TestHelpFlag(EnvTestCase):
        """tests for --help flag handling."""
        env_vars = ["RALPHEX_IMAGE"]
        save_argv = True

        def test_help_runs_container_for_ralphex_help(self) -> None:
            """--help runs container to show ralphex help."""
            docker_calls: list[list[str]] = []

            def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
                mock_result = unittest.mock.Mock()
                mock_result.returncode = 0
                mock_result.stdout = ""  # default for git commands
                if cmd and cmd[0] == "docker":
                    docker_calls.append(cmd)
                return mock_result

            with unittest.mock.patch("subprocess.run", side_effect=fake_run):
                sys.argv = ["ralphex-dk", "--help"]
                result = main()

            self.assertEqual(result, 0)
            # should have called docker run with --help
            self.assertEqual(len(docker_calls), 1)
            cmd = docker_calls[0]
            self.assertEqual(cmd[0], "docker")
            self.assertEqual(cmd[1], "run")
            self.assertIn("--help", cmd)

        def test_h_flag_same_as_help(self) -> None:
            """-h (short form) behaves same as --help."""
            docker_calls: list[list[str]] = []

            def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
                mock_result = unittest.mock.Mock()
                mock_result.returncode = 0
                mock_result.stdout = ""
                if cmd and cmd[0] == "docker":
                    docker_calls.append(cmd)
                return mock_result

            with unittest.mock.patch("subprocess.run", side_effect=fake_run):
                sys.argv = ["ralphex-dk", "-h"]
                result = main()

            self.assertEqual(result, 0)
            self.assertEqual(len(docker_calls), 1)
            self.assertIn("--help", docker_calls[0])

        def test_help_returns_container_exit_code(self) -> None:
            """main() returns exit code from container's --help."""
            def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
                mock_result = unittest.mock.Mock()
                mock_result.stdout = ""  # default for git commands
                if cmd and cmd[0] == "docker":
                    mock_result.returncode = 42  # docker run exit code
                else:
                    mock_result.returncode = 0  # git commands succeed
                return mock_result

            with unittest.mock.patch("subprocess.run", side_effect=fake_run):
                sys.argv = ["ralphex-dk", "--help"]
                result = main()

            self.assertEqual(result, 42)

        def test_help_with_env_flags_still_shows_help(self) -> None:
            """wrapper flags (-E, -v) before --help are parsed but help takes precedence."""
            docker_calls: list[list[str]] = []

            def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
                mock_result = unittest.mock.Mock()
                mock_result.returncode = 0
                mock_result.stdout = ""
                if cmd and cmd[0] == "docker":
                    docker_calls.append(cmd)
                return mock_result

            with unittest.mock.patch("subprocess.run", side_effect=fake_run):
                sys.argv = ["ralphex-dk", "-E", "FOO=bar", "-v", "/a:/b", "--help"]
                result = main()

            self.assertEqual(result, 0)
            # docker was still called for the help output
            self.assertEqual(len(docker_calls), 1)
            self.assertIn("--help", docker_calls[0])

    class TestHandleUpdateScript(unittest.TestCase):
        def test_up_to_date(self) -> None:
            """when current == remote, prints up-to-date message."""
            content = b"current content"
            tmp = Path(tempfile.mkdtemp())
            try:
                script = tmp / "wrapper.sh"
                script.write_bytes(content)

                def fake_urlopen(url: str, timeout: int = 30) -> unittest.mock.Mock:
                    ctx = unittest.mock.MagicMock()
                    ctx.__enter__.return_value.read.return_value = content
                    return ctx

                import io
                captured = io.StringIO()
                with unittest.mock.patch("__main__.urlopen", side_effect=fake_urlopen):
                    with unittest.mock.patch("sys.stderr", captured):
                        result = handle_update_script(script)

                self.assertEqual(result, 0)
                self.assertIn("up to date", captured.getvalue())
            finally:
                shutil.rmtree(tmp)

    loader = unittest.TestLoader()
    suite = unittest.TestSuite()
    for tc in [TestResolvePath, TestSymlinkTargetDirs, TestShouldBindPort, TestBuildVolumes,
               TestBuildVolumesGitignore, TestDetectGitWorktree, TestDetectTimezone,
               TestSelinuxEnabled, TestSelinuxVolumeSuffix,
               TestIsSensitiveName, TestBuildEnvVars,
               TestMergeEnvFlags, TestMergeVolumeFlags, TestBuildParser,
               TestMainArgparse, TestHelpFlag, TestHandleUpdateScript]:
        suite.addTests(loader.loadTestsFromTestCase(tc))
    runner = unittest.TextTestRunner(verbosity=2)
    result = runner.run(suite)
    if not result.wasSuccessful():
        sys.exit(1)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("\r\033[K", end="")
        sys.exit(130)
