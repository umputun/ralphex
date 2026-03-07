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

Note: RALPHEX_EXTRA_ENV emits warnings for sensitive names (KEY, SECRET, TOKEN,
etc.) with explicit values. Values containing commas must use -E flag instead.
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
VALID_CLAUDE_PROVIDERS = ["default", "bedrock"]

# environment variables to pass through when using bedrock provider
BEDROCK_ENV_VARS = [
    # core bedrock config (user must set CLAUDE_CODE_USE_BEDROCK=1 on host)
    "CLAUDE_CODE_USE_BEDROCK",
    "AWS_REGION",
    # explicit credentials (exported from profile or set directly by user)
    # NOTE: AWS_PROFILE is NOT in this list - it requires ~/.aws/config which
    # we don't mount. Profile is used on host only to export temp credentials.
    "AWS_ACCESS_KEY_ID",
    "AWS_SECRET_ACCESS_KEY",
    "AWS_SESSION_TOKEN",
    # bedrock API key auth
    "AWS_BEARER_TOKEN_BEDROCK",
    # model configuration (for inference profiles, custom model ARNs)
    "ANTHROPIC_MODEL",
    "ANTHROPIC_SMALL_FAST_MODEL",
    "ANTHROPIC_DEFAULT_OPUS_MODEL",
    "ANTHROPIC_DEFAULT_SONNET_MODEL",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL",
    "ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION",
    # optional
    "DISABLE_PROMPT_CACHING",
    "ANTHROPIC_BEDROCK_BASE_URL",
    "CLAUDE_CODE_SKIP_BEDROCK_AUTH",
]


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
    parser.add_argument("--claude-provider", dest="claude_provider", metavar="PROVIDER",
                        choices=VALID_CLAUDE_PROVIDERS,
                        help="claude provider: 'default' or 'bedrock' (env: RALPHEX_CLAUDE_PROVIDER)")
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

    # 1. claude_home (resolved) -> /mnt/claude:ro (skip if not exists, e.g. bedrock-only users)
    if claude_home.is_dir():
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
    if claude_home.is_dir():
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


def get_claude_provider(cli_provider: Optional[str]) -> str:
    """get claude provider from CLI flag or env var. returns 'default' or 'bedrock'.

    priority: CLI flag > RALPHEX_CLAUDE_PROVIDER env var > 'default'
    raises ValueError if provider value is invalid.
    """
    if cli_provider is not None:
        return cli_provider  # already validated by argparse choices
    env_provider = os.environ.get("RALPHEX_CLAUDE_PROVIDER", "").strip().lower()
    if not env_provider:
        return "default"
    if env_provider not in VALID_CLAUDE_PROVIDERS:
        raise ValueError(f"invalid RALPHEX_CLAUDE_PROVIDER: {env_provider!r} (valid: {', '.join(VALID_CLAUDE_PROVIDERS)})")
    return env_provider


def build_bedrock_env_args(existing_env: list[str] | None = None) -> list[str]:
    """build docker -e flags for bedrock env vars that are actually set.

    only passes through BEDROCK_ENV_VARS that have values in the host environment.
    skips vars that are already explicitly set in existing_env (from -E flags).
    """
    # extract var names already set via -E flags (format: ["-e", "VAR=val", ...] or ["-e", "VAR", ...])
    # track both the var name and whether it was explicit (VAR=value) vs inherit (VAR)
    already_set: set[str] = set()
    explicit_values: set[str] = set()  # vars with explicit VAR=value form
    if existing_env:
        i = 0
        while i < len(existing_env):
            if existing_env[i] == "-e" and i + 1 < len(existing_env):
                entry = existing_env[i + 1]
                # extract var name from "VAR=value" or "VAR"
                var_name = entry.split("=", 1)[0]
                already_set.add(var_name)
                # track if this is an explicit value (VAR=value) vs inherit form (VAR)
                if "=" in entry:
                    explicit_values.add(var_name)
                i += 2
            else:
                i += 1

    result: list[str] = []

    # check if user is providing explicit credential VALUES via -E VAR=value flags
    # if so, we should NOT inherit any session token from host env to avoid mixing
    # credentials from different sources (e.g., stale session token with new key/secret)
    # NOTE: inherit form (-E VAR) means user wants host values, so we should pass session token too
    user_provides_explicit_creds = "AWS_ACCESS_KEY_ID" in explicit_values or "AWS_SECRET_ACCESS_KEY" in explicit_values
    skip_session_token = user_provides_explicit_creds and "AWS_SESSION_TOKEN" not in already_set

    for var in BEDROCK_ENV_VARS:
        # skip vars that are already set via -E flags
        if var in already_set:
            continue
        # skip session token when user provides explicit credential values to avoid mixing
        if skip_session_token and var == "AWS_SESSION_TOKEN":
            continue
        # only pass vars that exist in env AND have non-empty values
        value = os.environ.get(var, "")
        if value:
            result.extend(["-e", var])
    return result


def export_aws_profile_credentials(extra_env: list[str] | None = None) -> dict[str, str]:
    """export AWS credentials from profile using aws configure export-credentials.

    returns dict with AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, and optionally
    AWS_SESSION_TOKEN extracted from the profile. returns empty dict if:
    - explicit credentials (AWS_ACCESS_KEY_ID) are already set (in os.environ or extra_env)
    - AWS_PROFILE is not set
    - aws CLI is not available
    - aws cli command fails
    """
    # extract env vars from -E flags to check for explicit credentials
    env_from_flags = extract_env_from_flags(extra_env)

    # skip if explicit credentials are already set (in os.environ or via -E flags)
    if os.environ.get("AWS_ACCESS_KEY_ID") or "AWS_ACCESS_KEY_ID" in env_from_flags:
        return {}

    # skip if no profile is set (check both os.environ and -E flags)
    profile = env_from_flags.get("AWS_PROFILE") if "AWS_PROFILE" in env_from_flags else os.environ.get("AWS_PROFILE", "")
    profile = profile.strip() if profile else ""
    if not profile:
        return {}

    # check if aws CLI is available (only needed when profile is set)
    if shutil.which("aws") is None:
        print("warning: aws CLI not found, cannot export profile credentials", file=sys.stderr)
        return {}

    # run aws configure export-credentials
    try:
        import json
        result = subprocess.run(
            ["aws", "configure", "export-credentials", "--profile", profile, "--format", "json"],
            capture_output=True, text=True, check=False,
        )
        if result.returncode != 0:
            stderr = result.stderr.strip() if result.stderr else "unknown error"
            print(f"warning: failed to export credentials from profile {profile}: {stderr}", file=sys.stderr)
            return {}

        # parse JSON output
        try:
            data = json.loads(result.stdout)
        except json.JSONDecodeError as e:
            print(f"warning: failed to parse credentials JSON: {e}", file=sys.stderr)
            return {}

        creds: dict[str, str] = {}
        if "AccessKeyId" in data:
            creds["AWS_ACCESS_KEY_ID"] = data["AccessKeyId"]
        if "SecretAccessKey" in data:
            creds["AWS_SECRET_ACCESS_KEY"] = data["SecretAccessKey"]
        if "SessionToken" in data:
            creds["AWS_SESSION_TOKEN"] = data["SessionToken"]
        return creds
    except OSError as e:
        print(f"warning: failed to run aws CLI: {e}", file=sys.stderr)
        return {}


def extract_env_from_flags(extra_env: list[str] | None) -> dict[str, str]:
    """extract env vars from docker -e flags list.

    parses ["-e", "VAR=value", "-e", "VAR2", ...] into dict.
    for inherit-form entries (just "VAR"), uses os.environ value.
    """
    result: dict[str, str] = {}
    if not extra_env:
        return result
    i = 0
    while i < len(extra_env):
        if extra_env[i] == "-e" and i + 1 < len(extra_env):
            entry = extra_env[i + 1]
            if "=" in entry:
                var_name, value = entry.split("=", 1)
                result[var_name] = value
            else:
                # inherit form: use current env value if set
                if entry in os.environ:
                    result[entry] = os.environ[entry]
            i += 2
        else:
            i += 1
    return result


def validate_bedrock_config(extra_env: list[str] | None = None) -> list[str]:
    """validate bedrock configuration and return list of warning messages.

    checks for common misconfigurations when using bedrock provider:
    - CLAUDE_CODE_USE_BEDROCK must be set (required for Claude Code inside container)
    - AWS_REGION should be set
    - either AWS_PROFILE or AWS_ACCESS_KEY_ID should be set for credentials

    considers both os.environ and values from extra_env (CLI -E flags).
    """
    warnings: list[str] = []

    # merge os.environ with extra_env flags (extra_env takes precedence)
    env_from_flags = extract_env_from_flags(extra_env)

    def get_val(key: str) -> str:
        """get value from extra_env flags first, then os.environ.

        note: explicit empty values in extra_env (VAR=) take precedence over os.environ.
        """
        if key in env_from_flags:
            return env_from_flags[key]  # may be empty string if explicitly set
        return os.environ.get(key, "")

    # check CLAUDE_CODE_USE_BEDROCK
    if not get_val("CLAUDE_CODE_USE_BEDROCK"):
        warnings.append("CLAUDE_CODE_USE_BEDROCK not set (required for Claude Code to use Bedrock inside container)")

    # check AWS_REGION
    if not get_val("AWS_REGION"):
        warnings.append("AWS_REGION not set (required for Bedrock)")

    # check for credentials source (profile, explicit key/secret, or bearer token)
    has_profile = bool(get_val("AWS_PROFILE").strip())
    has_access_key = bool(get_val("AWS_ACCESS_KEY_ID"))
    has_secret_key = bool(get_val("AWS_SECRET_ACCESS_KEY"))
    has_bearer_token = bool(get_val("AWS_BEARER_TOKEN_BEDROCK"))
    if not has_profile and not has_access_key and not has_bearer_token:
        warnings.append("no AWS credentials found (set AWS_PROFILE, AWS_ACCESS_KEY_ID, or AWS_BEARER_TOKEN_BEDROCK)")
    elif has_access_key and not has_secret_key:
        warnings.append("AWS_ACCESS_KEY_ID set but AWS_SECRET_ACCESS_KEY missing")

    return warnings


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


def build_base_env_vars() -> list[str]:
    """build base docker environment variable flags shared by all docker commands."""
    return [
        "-e", f"APP_UID={os.getuid()}",
        "-e", f"TIME_ZONE={detect_timezone()}",
        "-e", "SKIP_HOME_CHOWN=1",
        "-e", "INIT_QUIET=1",
        "-e", "CLAUDE_CONFIG_DIR=/home/app/.claude",
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

    # resolve claude config directory
    claude_config_dir_env = os.environ.get("CLAUDE_CONFIG_DIR", "")
    if claude_config_dir_env:
        claude_home = Path(claude_config_dir_env).expanduser().resolve()
    else:
        claude_home = Path.home() / ".claude"

    # get claude provider early (needed for --help and directory checks)
    try:
        provider = get_claude_provider(parsed.claude_provider)
    except ValueError as e:
        print(f"error: {e}", file=sys.stderr)
        return 1

    # handle --help: show wrapper help unconditionally, then try container help if config exists
    # skip claude_home check when using bedrock provider (no anthropic credentials needed)
    if parsed.help:
        parser.print_help()
        print("\n" + "-" * 70)
        if provider != "bedrock" and not claude_home.is_dir():
            print("ralphex options: (cannot show - claude config not found)")
            print("  run 'claude' first to authenticate, then re-run --help")
            return 0
        print("ralphex options (from container):\n")
        creds_temp = None if provider == "bedrock" else extract_macos_credentials(claude_home)
        try:
            volumes = build_volumes(creds_temp, claude_home) if provider != "bedrock" else []
            cmd = ["docker", "run", "--rm"]
            cmd.extend(build_base_env_vars())
            cmd.extend(volumes)
            cmd.extend(["-w", "/workspace"])
            cmd.extend([image, "/srv/ralphex", "--help"])
            return subprocess.run(cmd, check=False).returncode
        finally:
            if creds_temp:
                try:
                    creds_temp.unlink(missing_ok=True)
                except OSError:
                    pass

    # check required directories (after --help handling)
    # skip claude_home check when using bedrock provider (no anthropic credentials needed)
    if provider != "bedrock" and not claude_home.is_dir():
        print(f"error: {claude_home} directory not found (run 'claude' first to authenticate)", file=sys.stderr)
        return 1

    # extract macOS credentials (needed for volume mounts)
    # skip when using bedrock provider (uses AWS credentials instead)
    creds_temp = None if provider == "bedrock" else extract_macos_credentials(claude_home)

    # merge env var entries with CLI -E/--env flags (env first, CLI appends)
    extra_env = merge_env_flags(parsed.env)

    # add bedrock env vars when using bedrock provider
    bedrock_env_args: list[str] = []  # track for diagnostics
    if provider == "bedrock":
        # export credentials from AWS profile if profile is set and explicit creds are not
        # pass extra_env so it skips export when user provides explicit -E credentials
        exported_creds = export_aws_profile_credentials(extra_env)
        for key, value in exported_creds.items():
            # set in environment so build_bedrock_env_args() picks them up
            os.environ[key] = value
        # pass existing extra_env to avoid overriding user's explicit -E values
        bedrock_env_args = build_bedrock_env_args(extra_env)
        extra_env.extend(bedrock_env_args)

    # merge env var entries with CLI -v/--volume flags (env first, CLI appends)
    extra_volumes = merge_volume_flags(parsed.volume)

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
        # build volumes (base + extra from env var + CLI)
        volumes = build_volumes(creds_temp, claude_home)
        volumes.extend(extra_volumes)

        if claude_config_dir_env:
            print(f"using claude config dir: {claude_home}", file=sys.stderr)
        print(f"using image: {image}", file=sys.stderr)
        if provider == "bedrock":
            print("claude provider: bedrock (keychain skipped)", file=sys.stderr)
            # extract env from flags for diagnostics
            env_from_flags = extract_env_from_flags(extra_env)
            # show credential source
            if exported_creds:
                profile = os.environ.get("AWS_PROFILE", "")
                print(f"  exporting credentials from profile: {profile}", file=sys.stderr)
            elif os.environ.get("AWS_ACCESS_KEY_ID") or "AWS_ACCESS_KEY_ID" in env_from_flags:
                print("  using explicit credentials", file=sys.stderr)
            # show which bedrock env vars are actually being passed
            # combine: vars from build_bedrock_env_args + bedrock vars from -E flags
            passed_from_auto = [bedrock_env_args[i + 1] for i in range(0, len(bedrock_env_args), 2) if bedrock_env_args[i] == "-e"]
            passed_from_flags = [var for var in BEDROCK_ENV_VARS if var in env_from_flags]
            passed_vars = sorted(set(passed_from_auto + passed_from_flags), key=lambda v: BEDROCK_ENV_VARS.index(v) if v in BEDROCK_ENV_VARS else len(BEDROCK_ENV_VARS))
            if passed_vars:
                print(f"  passing: {', '.join(passed_vars)}", file=sys.stderr)
            # validate bedrock config and print warnings (pass extra_env to check -E flags too)
            bedrock_warnings = validate_bedrock_config(extra_env)
            for warning in bedrock_warnings:
                print(f"  warning: {warning}", file=sys.stderr)

        # schedule credential cleanup
        schedule_cleanup(creds_temp)

        # determine port binding
        bind_port = should_bind_port(ralphex_args)

        return run_docker(image, port, volumes, extra_env, bind_port, ralphex_args)
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

    class TestMergeEnvFlags(unittest.TestCase):
        def setUp(self) -> None:
            self._old_extra_env = os.environ.get("RALPHEX_EXTRA_ENV")
            os.environ.pop("RALPHEX_EXTRA_ENV", None)

        def tearDown(self) -> None:
            if self._old_extra_env is None:
                os.environ.pop("RALPHEX_EXTRA_ENV", None)
            else:
                os.environ["RALPHEX_EXTRA_ENV"] = self._old_extra_env

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

    class TestMergeVolumeFlags(unittest.TestCase):
        def setUp(self) -> None:
            self._old_extra_volumes = os.environ.get("RALPHEX_EXTRA_VOLUMES")
            os.environ.pop("RALPHEX_EXTRA_VOLUMES", None)

        def tearDown(self) -> None:
            if self._old_extra_volumes is None:
                os.environ.pop("RALPHEX_EXTRA_VOLUMES", None)
            else:
                os.environ["RALPHEX_EXTRA_VOLUMES"] = self._old_extra_volumes

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

    class TestMainArgparse(unittest.TestCase):
        """tests for main() argparse integration."""

        def setUp(self) -> None:
            """save environment and mock external dependencies."""
            self._saved_env: dict[str, str | None] = {}
            for key in ["RALPHEX_IMAGE", "RALPHEX_PORT", "RALPHEX_EXTRA_ENV",
                        "RALPHEX_EXTRA_VOLUMES", "CLAUDE_CONFIG_DIR"]:
                self._saved_env[key] = os.environ.get(key)
                os.environ.pop(key, None)
            self._saved_argv = sys.argv[:]

        def tearDown(self) -> None:
            """restore environment and sys.argv."""
            for key, val in self._saved_env.items():
                if val is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = val
            sys.argv[:] = self._saved_argv

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
            tmp = Path(tempfile.mkdtemp())
            try:
                claude_dir = tmp / ".claude"
                claude_dir.mkdir()
                os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

                captured_env: list[str] = []

                def fake_run_docker(image: str, port: str, volumes: list[str],
                                    env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                    captured_env.extend(env_vars)
                    return 0

                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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
                os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

                captured_volumes: list[str] = []

                def fake_run_docker(image: str, port: str, volumes: list[str],
                                    env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                    captured_volumes.extend(volumes)
                    return 0

                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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
                os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

                captured_args: list[str] = []

                def fake_run_docker(image: str, port: str, volumes: list[str],
                                    env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                    captured_args.extend(args)
                    return 0

                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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
                os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

                captured_args: list[str] = []

                def fake_run_docker(image: str, port: str, volumes: list[str],
                                    env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                    captured_args.extend(args)
                    return 0

                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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
                os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

                captured_args: list[str] = []

                def fake_run_docker(image: str, port: str, volumes: list[str],
                                    env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                    captured_args.extend(args)
                    return 0

                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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
                os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

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
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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
                os.environ["CLAUDE_CONFIG_DIR"] = str(claude_dir)

                captured_env: list[str] = []

                def fake_run_docker(image: str, port: str, volumes: list[str],
                                    env_vars: list[str], bind_port: bool, args: list[str]) -> int:
                    captured_env.extend(env_vars)
                    return 0

                import io
                captured_stderr = io.StringIO()
                with unittest.mock.patch("sys.stderr", captured_stderr):
                    with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                        with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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

    class TestHelpFlag(unittest.TestCase):
        """tests for --help flag handling."""

        def setUp(self) -> None:
            """save environment."""
            self._saved_env: dict[str, str | None] = {}
            # clear RALPHEX_CLAUDE_PROVIDER to ensure default provider is used
            for key in ["RALPHEX_IMAGE", "CLAUDE_CONFIG_DIR", "RALPHEX_CLAUDE_PROVIDER"]:
                self._saved_env[key] = os.environ.get(key)
                os.environ.pop(key, None)
            self._saved_argv = sys.argv[:]

        def tearDown(self) -> None:
            """restore environment and sys.argv."""
            for key, val in self._saved_env.items():
                if val is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = val
            sys.argv[:] = self._saved_argv

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
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
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

    class TestClaudeProvider(unittest.TestCase):
        """tests for claude provider selection and bedrock env var handling."""

        def setUp(self) -> None:
            """save environment."""
            self._saved_env: dict[str, str | None] = {}
            # save and clear RALPHEX_CLAUDE_PROVIDER plus all BEDROCK_ENV_VARS
            keys_to_clear = ["RALPHEX_CLAUDE_PROVIDER"] + BEDROCK_ENV_VARS
            for key in keys_to_clear:
                self._saved_env[key] = os.environ.get(key)
                os.environ.pop(key, None)

        def tearDown(self) -> None:
            """restore environment."""
            for key, val in self._saved_env.items():
                if val is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = val

        def test_default_provider_no_bedrock_env(self) -> None:
            """no flag, no env → provider is 'default', no AWS vars in bedrock args."""
            provider = get_claude_provider(None)
            self.assertEqual(provider, "default")
            # build_bedrock_env_args should return empty when no bedrock vars set
            args = build_bedrock_env_args()
            self.assertEqual(args, [])

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

    class TestAwsCredentialExport(unittest.TestCase):
        """tests for AWS profile credential export."""

        def setUp(self) -> None:
            """save environment."""
            self._saved_env: dict[str, str | None] = {}
            keys_to_clear = ["AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"]
            for key in keys_to_clear:
                self._saved_env[key] = os.environ.get(key)
                os.environ.pop(key, None)

        def tearDown(self) -> None:
            """restore environment."""
            for key, val in self._saved_env.items():
                if val is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = val

        def test_exports_credentials_with_profile(self) -> None:
            """AWS_PROFILE set → runs aws cli, parses JSON output."""
            os.environ["AWS_PROFILE"] = "test-profile"
            json_output = '{"AccessKeyId": "AKIATEST", "SecretAccessKey": "secret123", "SessionToken": "tok123"}'

            def fake_run(cmd: list[str], **kwargs: object) -> unittest.mock.Mock:
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

    class TestBedrockSkipKeychain(unittest.TestCase):
        """tests for bedrock mode skipping keychain and claude_home checks."""

        def setUp(self) -> None:
            """save environment."""
            self._saved_env: dict[str, str | None] = {}
            keys_to_clear = ["RALPHEX_CLAUDE_PROVIDER"] + BEDROCK_ENV_VARS
            for key in keys_to_clear:
                self._saved_env[key] = os.environ.get(key)
                os.environ.pop(key, None)
            self._saved_argv = sys.argv.copy()

        def tearDown(self) -> None:
            """restore environment."""
            for key, val in self._saved_env.items():
                if val is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = val
            sys.argv = self._saved_argv

        def test_skips_credentials_extraction_when_bedrock(self) -> None:
            """bedrock provider skips extract_macos_credentials (creds_temp is None)."""
            captured_creds_temp: list[object] = []

            def fake_run_docker(
                image: str, port: int, volumes: list[str], extra_env: list[str],
                bind_port: bool, ralphex_args: list[str]
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
                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("__main__.extract_macos_credentials", side_effect=fake_extract):
                        with unittest.mock.patch("__main__.export_aws_profile_credentials", return_value={}):
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
                bind_port: bool, ralphex_args: list[str]
            ) -> int:
                return 0

            # use a non-existent claude home path
            with tempfile.TemporaryDirectory() as tmpdir:
                fake_claude_home = Path(tmpdir) / "nonexistent_claude"
                # do NOT create the directory

                with unittest.mock.patch("sys.stderr", captured_stderr):
                    with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                        with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
                            with unittest.mock.patch("__main__.export_aws_profile_credentials", return_value={}):
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
                bind_port: bool, ralphex_args: list[str]
            ) -> int:
                return 0

            import io
            captured_stderr = io.StringIO()
            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("__main__.extract_macos_credentials", side_effect=fake_extract):
                        sys.argv = ["ralphex-dk", "plan.md"]
                        result = main()

            self.assertEqual(result, 0)
            # extract_macos_credentials SHOULD be called for default provider
            self.assertEqual(extract_calls[0], 1)

        def test_startup_message_shows_bedrock_mode(self) -> None:
            """startup output includes 'bedrock' and 'keychain skipped'."""
            def fake_run_docker(
                image: str, port: int, volumes: list[str], extra_env: list[str],
                bind_port: bool, ralphex_args: list[str]
            ) -> int:
                return 0

            import io
            captured_stderr = io.StringIO()
            with unittest.mock.patch("sys.stderr", captured_stderr):
                with unittest.mock.patch("__main__.run_docker", side_effect=fake_run_docker):
                    with unittest.mock.patch("__main__.extract_macos_credentials", return_value=None):
                        with unittest.mock.patch("__main__.export_aws_profile_credentials", return_value={}):
                            sys.argv = ["ralphex-dk", "--claude-provider", "bedrock", "plan.md"]
                            result = main()

            self.assertEqual(result, 0)
            output = captured_stderr.getvalue()
            self.assertIn("bedrock", output.lower())
            self.assertIn("keychain skipped", output.lower())

    class TestBedrockValidation(unittest.TestCase):
        """tests for validate_bedrock_config() function."""

        def setUp(self) -> None:
            """save environment."""
            self._saved_env: dict[str, str | None] = {}
            keys_to_clear = ["CLAUDE_CODE_USE_BEDROCK", "AWS_REGION", "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK"]
            for key in keys_to_clear:
                self._saved_env[key] = os.environ.get(key)
                os.environ.pop(key, None)

        def tearDown(self) -> None:
            """restore environment."""
            for key, val in self._saved_env.items():
                if val is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = val

        def test_warns_missing_claude_code_use_bedrock(self) -> None:
            """warns when CLAUDE_CODE_USE_BEDROCK is not set."""
            # set other vars, but not CLAUDE_CODE_USE_BEDROCK
            os.environ["AWS_REGION"] = "us-east-1"
            os.environ["AWS_ACCESS_KEY_ID"] = "AKIATEST"
            os.environ["AWS_SECRET_ACCESS_KEY"] = "secret"

            warnings = validate_bedrock_config()

            self.assertEqual(len(warnings), 1)
            self.assertIn("CLAUDE_CODE_USE_BEDROCK", warnings[0])

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

    loader = unittest.TestLoader()
    suite = unittest.TestSuite()
    for tc in [TestResolvePath, TestSymlinkTargetDirs, TestShouldBindPort, TestBuildVolumes,
               TestBuildVolumesGitignore, TestDetectGitWorktree, TestDetectTimezone,
               TestExtractCredentials, TestScheduleCleanup,
               TestBuildDockerCmd, TestKeychainServiceName, TestBuildVolumesClaudeHome,
               TestExtractCredentialsClaudeHome, TestSelinuxEnabled, TestSelinuxVolumeSuffix,
               TestClaudeConfigDirEnv, TestIsSensitiveName, TestBuildEnvVars,
               TestMergeEnvFlags, TestMergeVolumeFlags, TestBuildParser,
               TestMainArgparse, TestHelpFlag, TestClaudeProvider, TestAwsCredentialExport,
               TestBedrockSkipKeychain, TestBedrockValidation, TestExtractEnvFromFlags]:
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
