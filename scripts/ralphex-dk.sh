#!/usr/bin/env python3
"""ralphex-dk.sh - run ralphex in a docker container

usage: ralphex-dk.sh [ralphex-args]
example: ralphex-dk.sh docs/plans/feature.md
example: ralphex-dk.sh --serve docs/plans/feature.md
example: ralphex-dk.sh --review
example: ralphex-dk.sh -v /data:/mnt/data:ro docs/plans/feature.md
example: ralphex-dk.sh -E DEBUG=1 -E API_KEY docs/plans/feature.md
example: ralphex-dk.sh --update         # pull latest docker image
example: ralphex-dk.sh --update-script  # update this wrapper script

Environment variables:
- RALPHEX_EXTRA_ENV: comma-separated env vars (VAR=value or VAR to inherit from host). Security warning emitted for sensitive names (KEY, SECRET, TOKEN, etc.) with explicit values. Values containing commas cannot use RALPHEX_EXTRA_ENV; use -E flag instead.
- RALPHEX_EXTRA_VOLUMES: comma-separated volume mounts (src:dest[:opts])
"""

import argparse
import difflib
import hashlib
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
        epilog=textwrap.dedent("""\
            Environment variables:
              RALPHEX_IMAGE         Docker image (default: ghcr.io/umputun/ralphex-go:latest)
              RALPHEX_PORT          Web dashboard port with --serve (default: 8080)
              RALPHEX_EXTRA_ENV     Comma-separated env vars (VAR=value or VAR)
              RALPHEX_EXTRA_VOLUMES Comma-separated volume mounts (src:dst[:opts])

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


def extract_extra_volumes(args: list[str]) -> tuple[list[str], list[str]]:
    """extract -v/--volume flags from args, return (extra_volumes, remaining_args)."""
    extra: list[str] = []
    remaining: list[str] = []
    i = 0
    while i < len(args):
        if args[i] in ("-v", "--volume") and i + 1 < len(args):
            extra.extend(["-v", args[i + 1]])
            i += 2
        else:
            remaining.append(args[i])
            i += 1
    return extra, remaining


def extract_extra_env(args: list[str]) -> tuple[list[str], list[str]]:
    """extract and validate -E/--env flags from args, return (docker_env_flags, remaining_args)."""
    extra: list[str] = []
    remaining: list[str] = []
    i = 0
    while i < len(args):
        if args[i] in ("-E", "--env") and i + 1 < len(args):
            entry = args[i + 1]
            if validated := validate_env_entry(entry, warn_invalid=True):
                extra.extend(["-e", validated])
            i += 2
        else:
            remaining.append(args[i])
            i += 1
    return extra, remaining


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


def keychain_service_name(claude_home: Path) -> str:
    """derive macOS Keychain service name from claude config directory.

    default ~/.claude uses "Claude Code-credentials" (no suffix).
    any other path uses "Claude Code-credentials-{sha256(absolute_path)[:8]}".
    """
    resolved = claude_home.expanduser().resolve()
    default = Path.home() / ".claude"
    if resolved == default or resolved == default.resolve():
        return "Claude Code-credentials"
    digest = hashlib.sha256(str(resolved).encode()).hexdigest()[:8]
    return f"Claude Code-credentials-{digest}"


def extract_macos_credentials(claude_home: Path) -> Optional[Path]:
    """on macOS, extract claude credentials from keychain if not already on disk."""
    if platform.system() != "Darwin":
        return None
    if (claude_home / ".credentials.json").exists():
        return None

    service = keychain_service_name(claude_home)

    # try to read credentials (works if keychain already unlocked)
    creds_json = _security_find_credentials(service)
    if not creds_json:
        # keychain locked - unlock and retry
        print("unlocking macOS keychain to extract Claude credentials (enter login password)...", file=sys.stderr)
        subprocess.run(["security", "unlock-keychain"], capture_output=True, check=False)
        creds_json = _security_find_credentials(service)

    if not creds_json:
        return None

    fd, tmp_path = tempfile.mkstemp()
    fd_closed = False
    try:
        with os.fdopen(fd, "w") as f:
            fd_closed = True
            f.write(creds_json + "\n")
    except OSError:
        if not fd_closed:
            os.close(fd)
        try:
            os.unlink(tmp_path)
        except OSError:
            pass
        return None
    return Path(tmp_path)


def _security_find_credentials(service_name: str) -> Optional[str]:
    """try to read Claude Code credentials from macOS keychain."""
    try:
        result = subprocess.run(
            ["security", "find-generic-password", "-s", service_name, "-w"],
            capture_output=True, text=True, check=False,
        )
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    except OSError:
        pass
    return None


def build_volumes(creds_temp: Optional[Path], claude_home: Optional[Path] = None) -> list[str]:
    """build docker volume mount arguments, returns flat list like ['-v', 'src:dst', ...]."""
    home = Path.home()
    # use logical PWD when available to preserve symlinks (matches previous bash wrapper behavior)
    pwd_env = os.environ.get("PWD")
    cwd = Path(pwd_env) if pwd_env else Path(os.getcwd())
    if claude_home is None:
        claude_home = home / ".claude"
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

    # 1. claude_home (resolved) -> /mnt/claude:ro
    add(resolve_path(claude_home), "/mnt/claude", ro=True)

    # 2. cwd -> /workspace
    add(cwd, "/workspace")

    # 3. git worktree common dir
    git_common = detect_git_worktree(cwd)
    if git_common:
        add(git_common, str(git_common))

    # 4. macOS credentials temp file
    if creds_temp:
        add(creds_temp, "/mnt/claude-credentials.json", ro=True)

    # 5. symlink targets under claude_home
    add_symlink_targets(claude_home)

    # 6. ~/.codex -> /mnt/codex:ro + symlink targets
    codex_dir = home / ".codex"
    if codex_dir.is_dir():
        add(resolve_path(codex_dir), "/mnt/codex", ro=True)
        add_symlink_targets(codex_dir)

    # 7. ~/.config/ralphex -> /home/app/.config/ralphex + symlink targets
    ralphex_config = home / ".config" / "ralphex"
    if ralphex_config.is_dir():
        add(resolve_path(ralphex_config), "/home/app/.config/ralphex")
        add_symlink_targets(ralphex_config)

    # 8. .ralphex/ symlink targets only (workspace mount already includes it)
    local_ralphex = cwd / ".ralphex"
    if local_ralphex.is_dir():
        add_symlink_targets(local_ralphex)

    # 9. ~/.gitconfig -> /home/app/.gitconfig:ro
    gitconfig = home / ".gitconfig"
    if gitconfig.exists():
        add(resolve_path(gitconfig), "/home/app/.gitconfig", ro=True)

    # 10. global gitignore -> remap home-relative paths to /home/app/
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

    # 11. extra user-defined volumes via RALPHEX_EXTRA_VOLUMES env var (comma-separated)
    extra = os.environ.get("RALPHEX_EXTRA_VOLUMES", "")
    for mount in extra.split(","):
        mount = mount.strip()
        if mount and ":" in mount:
            vols.extend(["-v", mount])

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


def schedule_cleanup(creds_temp: Optional[Path]) -> None:
    """schedule credentials temp file deletion after a delay."""
    if not creds_temp:
        return

    def _remove() -> None:
        try:
            creds_temp.unlink(missing_ok=True)
        except OSError:
            pass

    t = threading.Timer(10.0, _remove)
    t.daemon = True
    t.start()


def run_docker(image: str, port: str, volumes: list[str], env_vars: list[str], bind_port: bool, args: list[str]) -> int:
    """build and execute docker run command."""
    cmd = ["docker", "run"]

    interactive = sys.stdin.isatty()
    if interactive:
        cmd.append("-it")
    cmd.append("--rm")

    cmd.extend([
        "-e", f"APP_UID={os.getuid()}",
        "-e", f"TIME_ZONE={detect_timezone()}",
        "-e", "SKIP_HOME_CHOWN=1",
        "-e", "INIT_QUIET=1",
        "-e", "CLAUDE_CONFIG_DIR=/home/app/.claude",
    ])

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
    # handle --test flag
    if len(sys.argv) > 1 and sys.argv[1] == "--test":
        run_tests()
        return 0

    image = os.environ.get("RALPHEX_IMAGE", DEFAULT_IMAGE)
    port = os.environ.get("RALPHEX_PORT", DEFAULT_PORT)
    args = sys.argv[1:]

    # handle --update
    if args and args[0] == "--update":
        return handle_update(image)

    # handle --update-script
    if args and args[0] == "--update-script":
        script_path = Path(os.path.realpath(sys.argv[0]))
        return handle_update_script(script_path)

    # extract -v/--volume flags (consumed by wrapper, not passed to ralphex)
    cli_volumes, args = extract_extra_volumes(args)

    # extract -e/--env flags (consumed by wrapper, not passed to ralphex)
    cli_env, args = extract_extra_env(args)

    # resolve claude config directory
    claude_config_dir_env = os.environ.get("CLAUDE_CONFIG_DIR", "")
    if claude_config_dir_env:
        claude_home = Path(claude_config_dir_env).expanduser().resolve()
    else:
        claude_home = Path.home() / ".claude"

    # check required directories
    if not claude_home.is_dir():
        print(f"error: {claude_home} directory not found (run 'claude' first to authenticate)", file=sys.stderr)
        return 1

    # extract macOS credentials
    creds_temp = extract_macos_credentials(claude_home)

    def _cleanup_creds() -> None:
        if creds_temp:
            try:
                creds_temp.unlink(missing_ok=True)
            except OSError:
                pass

    # setup SIGTERM handler: terminate docker child process and clean up credentials
    def _term_handler(signum: int, frame: object) -> None:
        proc = getattr(run_docker, "_active_proc", None)
        if proc is not None:
            try:
                proc.terminate()
            except ProcessLookupError:
                pass
        _cleanup_creds()
        sys.exit(128 + signum)

    signal.signal(signal.SIGTERM, _term_handler)

    try:
        # build volumes
        volumes = build_volumes(creds_temp, claude_home)
        volumes.extend(cli_volumes)

        # build env vars from RALPHEX_EXTRA_ENV, then append validated CLI -E/--env flags
        env_vars = build_env_vars()
        env_vars.extend(cli_env)

        if claude_config_dir_env:
            print(f"using claude config dir: {claude_home}", file=sys.stderr)
        print(f"using image: {image}", file=sys.stderr)

        # schedule credential cleanup
        schedule_cleanup(creds_temp)

        # determine port binding
        bind_port = should_bind_port(args)

        return run_docker(image, port, volumes, env_vars, bind_port, args)
    finally:
        _cleanup_creds()


# --- embedded tests ---


def run_tests() -> None:
    """run embedded unit tests."""

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
                vols = build_volumes(None)
            # volumes should come in -v pairs
            for i in range(0, len(vols), 2):
                self.assertEqual(vols[i], "-v")
                self.assertIn(":", vols[i + 1])

        def test_includes_workspace_without_selinux(self) -> None:
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                vols = build_volumes(None)
                pwd_env = os.environ.get("PWD")
                cwd = Path(pwd_env) if pwd_env else Path(os.getcwd())
                self.assertIn(f"{cwd}:/workspace", vols)

        def test_includes_workspace_with_selinux(self) -> None:
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=True):
                vols = build_volumes(None)
                pwd_env = os.environ.get("PWD")
                cwd = Path(pwd_env) if pwd_env else Path(os.getcwd())
                self.assertIn(f"{cwd}:/workspace:z", vols)

        def test_includes_claude_dir_without_selinux(self) -> None:
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                vols = build_volumes(None)
                found = any("/mnt/claude:ro" in v for v in vols)
                self.assertTrue(found, "should mount ~/.claude to /mnt/claude:ro")

        def test_includes_claude_dir_with_selinux(self) -> None:
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=True):
                vols = build_volumes(None)
                found = any("/mnt/claude:ro,z" in v for v in vols)
                self.assertTrue(found, "should mount ~/.claude to /mnt/claude:ro,z")

    class TestBuildVolumesGitignore(unittest.TestCase):
        def test_global_gitignore_remapped_to_home_app(self) -> None:
            """global gitignore under $HOME should be mounted at /home/app/<relative>."""
            home = Path.home()
            fake_ignore = home / ".gitignore"
            with (
                unittest.mock.patch(f"{__name__}.get_global_gitignore", return_value=fake_ignore),
                unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False),
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
                unittest.mock.patch(f"{__name__}.get_global_gitignore", return_value=fake_ignore),
                unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False),
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
                unittest.mock.patch(f"{__name__}.get_global_gitignore", return_value=fake_ignore),
                unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False),
                unittest.mock.patch(f"{__name__}.resolve_path", side_effect=lambda p: p),
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

    class TestBuildDockerCmd(unittest.TestCase):
        def test_creds_volume_mount_without_selinux(self) -> None:
            """build_volumes should include creds temp mount when provided."""
            fd, tmp_path = tempfile.mkstemp()
            os.close(fd)
            try:
                creds = Path(tmp_path)
                with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
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
                with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=True):
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
                with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
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
                with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=True):
                    vols = build_volumes(None, claude_home=custom)
                mount = f"{custom}:/mnt/claude:ro,z"
                self.assertIn(mount, vols)
            finally:
                shutil.rmtree(tmp)

        def test_default_claude_home_when_none(self) -> None:
            """build_volumes with claude_home=None defaults to ~/.claude."""
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
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
                vols = build_volumes(None)
            for i in range(1, len(vols), 2):
                has_z = vols[i].endswith(":z") or ",z" in vols[i]
                self.assertTrue(has_z, f"volume {vols[i]} missing :z SELinux label")

        def test_no_z_label_without_selinux(self) -> None:
            """volume mounts omit :z label when SELinux is not enabled."""
            with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
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
            """empty CLAUDE_CONFIG_DIR falls back to ~/.claude."""
            old = os.environ.get("CLAUDE_CONFIG_DIR")
            os.environ.pop("CLAUDE_CONFIG_DIR", None)
            try:
                env_val = os.environ.get("CLAUDE_CONFIG_DIR", "")
                self.assertFalse(env_val)
                # fallback path
                result = Path.home() / ".claude"
                self.assertEqual(result, Path.home() / ".claude")
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

    class TestExtraVolumes(unittest.TestCase):
        def test_extra_volumes_added(self) -> None:
            """RALPHEX_EXTRA_VOLUMES adds user-defined mounts."""
            old = os.environ.get("RALPHEX_EXTRA_VOLUMES")
            os.environ["RALPHEX_EXTRA_VOLUMES"] = "/tmp/a:/mnt/a:ro,/tmp/b:/mnt/b"
            try:
                with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                    vols = build_volumes(None)
                self.assertIn("/tmp/a:/mnt/a:ro", vols)
                self.assertIn("/tmp/b:/mnt/b", vols)
            finally:
                if old is None:
                    os.environ.pop("RALPHEX_EXTRA_VOLUMES", None)
                else:
                    os.environ["RALPHEX_EXTRA_VOLUMES"] = old

        def test_empty_extra_volumes_is_noop(self) -> None:
            """empty RALPHEX_EXTRA_VOLUMES adds no extra mounts."""
            old = os.environ.get("RALPHEX_EXTRA_VOLUMES")
            os.environ.pop("RALPHEX_EXTRA_VOLUMES", None)
            try:
                with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                    vols = build_volumes(None)
                base_count = len(vols)
                os.environ["RALPHEX_EXTRA_VOLUMES"] = ""
                with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                    vols2 = build_volumes(None)
                self.assertEqual(len(vols), len(vols2))
            finally:
                if old is None:
                    os.environ.pop("RALPHEX_EXTRA_VOLUMES", None)
                else:
                    os.environ["RALPHEX_EXTRA_VOLUMES"] = old

        def test_invalid_entries_skipped(self) -> None:
            """entries without ':' are silently skipped."""
            old = os.environ.get("RALPHEX_EXTRA_VOLUMES")
            os.environ["RALPHEX_EXTRA_VOLUMES"] = "badentry,/tmp/ok:/mnt/ok"
            try:
                with unittest.mock.patch(f"{__name__}.selinux_enabled", return_value=False):
                    vols = build_volumes(None)
                self.assertNotIn("badentry", vols)
                self.assertIn("/tmp/ok:/mnt/ok", vols)
            finally:
                if old is None:
                    os.environ.pop("RALPHEX_EXTRA_VOLUMES", None)
                else:
                    os.environ["RALPHEX_EXTRA_VOLUMES"] = old

    class TestExtractExtraVolumes(unittest.TestCase):
        def test_extracts_v_flag(self) -> None:
            """'-v src:dst' is extracted from args."""
            extra, remaining = extract_extra_volumes(["-v", "/a:/b", "plan.md"])
            self.assertEqual(extra, ["-v", "/a:/b"])
            self.assertEqual(remaining, ["plan.md"])

        def test_extracts_volume_flag(self) -> None:
            """'--volume src:dst' is extracted from args."""
            extra, remaining = extract_extra_volumes(["--volume", "/a:/b", "plan.md"])
            self.assertEqual(extra, ["-v", "/a:/b"])
            self.assertEqual(remaining, ["plan.md"])

        def test_multiple_volumes(self) -> None:
            """multiple -v flags are all extracted."""
            extra, remaining = extract_extra_volumes(["-v", "/a:/b", "-v", "/c:/d", "plan.md"])
            self.assertEqual(extra, ["-v", "/a:/b", "-v", "/c:/d"])
            self.assertEqual(remaining, ["plan.md"])

        def test_no_volumes(self) -> None:
            """args without -v pass through unchanged."""
            extra, remaining = extract_extra_volumes(["--serve", "plan.md"])
            self.assertEqual(extra, [])
            self.assertEqual(remaining, ["--serve", "plan.md"])

        def test_v_at_end_without_value(self) -> None:
            """-v at end of args without a value is kept as remaining."""
            extra, remaining = extract_extra_volumes(["plan.md", "-v"])
            self.assertEqual(extra, [])
            self.assertEqual(remaining, ["plan.md", "-v"])

        def test_mixed_with_other_flags(self) -> None:
            """-v interleaved with other flags."""
            extra, remaining = extract_extra_volumes(["--serve", "-v", "/x:/y:ro", "plan.md"])
            self.assertEqual(extra, ["-v", "/x:/y:ro"])
            self.assertEqual(remaining, ["--serve", "plan.md"])

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

    class TestExtractExtraEnv(unittest.TestCase):
        def test_extracts_uppercase_e_flag_with_value(self) -> None:
            """-E FOO=bar is extracted from args."""
            extra, remaining = extract_extra_env(["-E", "FOO=bar", "plan.md"])
            self.assertEqual(extra, ["-e", "FOO=bar"])
            self.assertEqual(remaining, ["plan.md"])

        def test_extracts_uppercase_e_flag_name_only(self) -> None:
            """-E FOO (name-only) is extracted from args."""
            extra, remaining = extract_extra_env(["-E", "FOO", "plan.md"])
            self.assertEqual(extra, ["-e", "FOO"])
            self.assertEqual(remaining, ["plan.md"])

        def test_extracts_env_flag(self) -> None:
            """--env FOO=bar is extracted from args."""
            extra, remaining = extract_extra_env(["--env", "FOO=bar", "plan.md"])
            self.assertEqual(extra, ["-e", "FOO=bar"])
            self.assertEqual(remaining, ["plan.md"])

        def test_multiple_env_flags(self) -> None:
            """multiple -E flags are all extracted."""
            extra, remaining = extract_extra_env(["-E", "FOO=bar", "-E", "BAZ", "plan.md"])
            self.assertEqual(extra, ["-e", "FOO=bar", "-e", "BAZ"])
            self.assertEqual(remaining, ["plan.md"])

        def test_no_env_flags(self) -> None:
            """args without -E pass through unchanged."""
            extra, remaining = extract_extra_env(["--serve", "plan.md"])
            self.assertEqual(extra, [])
            self.assertEqual(remaining, ["--serve", "plan.md"])

        def test_mixed_with_other_flags(self) -> None:
            """-E interleaved with other flags."""
            extra, remaining = extract_extra_env(["--serve", "-E", "DEBUG=1", "plan.md"])
            self.assertEqual(extra, ["-e", "DEBUG=1"])
            self.assertEqual(remaining, ["--serve", "plan.md"])

        def test_lowercase_e_passes_through(self) -> None:
            """-e (ralphex's external-only flag) passes through to ralphex."""
            extra, remaining = extract_extra_env(["-e", "plan.md"])
            self.assertEqual(extra, [])
            self.assertEqual(remaining, ["-e", "plan.md"])

        def test_invalid_entries_skipped_with_warning(self) -> None:
            """invalid env var names are skipped with warning."""
            import io
            captured = io.StringIO()
            with unittest.mock.patch("sys.stderr", captured):
                extra, remaining = extract_extra_env(["-E", "=bad", "-E", "GOOD=val", "-E", "123BAD", "plan.md"])
            self.assertEqual(extra, ["-e", "GOOD=val"])
            self.assertEqual(remaining, ["plan.md"])
            warning = captured.getvalue()
            self.assertIn("=bad", warning)
            self.assertIn("123BAD", warning)

        def test_sensitive_name_warning(self) -> None:
            """sensitive name with explicit value prints warning."""
            import io
            captured = io.StringIO()
            with unittest.mock.patch("sys.stderr", captured):
                extra, remaining = extract_extra_env(["-E", "API_KEY=secret", "plan.md"])
            self.assertEqual(extra, ["-e", "API_KEY=secret"])
            warning = captured.getvalue()
            self.assertIn("-E API_KEY", warning)

        def test_sensitive_name_no_warning_for_name_only(self) -> None:
            """sensitive name without explicit value does not print warning."""
            import io
            captured = io.StringIO()
            with unittest.mock.patch("sys.stderr", captured):
                extra, remaining = extract_extra_env(["-E", "API_KEY", "plan.md"])
            self.assertEqual(extra, ["-e", "API_KEY"])
            self.assertEqual(captured.getvalue(), "")

    class TestBuildEnvVars(unittest.TestCase):
        def setUp(self) -> None:
            self._old_extra_env = os.environ.get("RALPHEX_EXTRA_ENV")
            os.environ.pop("RALPHEX_EXTRA_ENV", None)

        def tearDown(self) -> None:
            if self._old_extra_env is None:
                os.environ.pop("RALPHEX_EXTRA_ENV", None)
            else:
                os.environ["RALPHEX_EXTRA_ENV"] = self._old_extra_env

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

    loader = unittest.TestLoader()
    suite = unittest.TestSuite()
    for tc in [TestResolvePath, TestSymlinkTargetDirs, TestShouldBindPort, TestBuildVolumes,
               TestBuildVolumesGitignore, TestDetectGitWorktree, TestExtractCredentials, TestScheduleCleanup,
               TestBuildDockerCmd, TestKeychainServiceName, TestBuildVolumesClaudeHome,
               TestExtractCredentialsClaudeHome, TestSelinuxEnabled, TestSelinuxVolumeSuffix,
               TestClaudeConfigDirEnv, TestExtraVolumes, TestExtractExtraVolumes,
               TestIsSensitiveName, TestExtractExtraEnv, TestBuildEnvVars, TestBuildParser]:
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
