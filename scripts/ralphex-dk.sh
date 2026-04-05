#!/usr/bin/env python3
"""ralphex-dk.sh - run ralphex in a docker container

Usage: ralphex-dk.sh [wrapper-flags] [ralphex-args]
       ralphex-dk.sh [wrapper-flags] -- [ralphex-args]

Wrapper-specific flags (parsed by this script):
  -E, --env VAR[=val]        extra env var to pass to container (repeatable)
  -v, --volume src:dst[:opts] extra volume mount (repeatable)
  --docker                   mount host Docker socket into container
  --dry-run                  print docker command without executing
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
  ralphex-dk.sh --docker docs/plans/feature.md        # mount host Docker socket
  ralphex-dk.sh --docker --dry-run                   # verify socket mount in command
  ralphex-dk.sh --dry-run                            # show docker command
  ralphex-dk.sh --dry-run -E FOO                     # warns about inherited var
  ralphex-dk.sh --update
  ralphex-dk.sh --update-script

Environment variables:
  RALPHEX_IMAGE          Docker image (default: ghcr.io/umputun/ralphex-go:latest)
  RALPHEX_PORT           Web dashboard port with --serve (default: 8080)
  RALPHEX_DOCKER_SOCKET  Enable Docker socket mount ("1", "true", "yes")
  RALPHEX_DOCKER_NETWORK Docker network mode (e.g., "host", "my-network")
  RALPHEX_EXTRA_ENV      Comma-separated env vars (VAR=value or VAR to inherit)
  RALPHEX_EXTRA_VOLUMES  Comma-separated volume mounts (src:dst[:opts])

Note: RALPHEX_EXTRA_ENV emits warnings for sensitive names (KEY, SECRET, TOKEN,
etc.) with explicit values. Values containing commas must use -E flag instead.
"""

import argparse
import dataclasses
import difflib
import hashlib
import os
import platform
import re
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import textwrap
import threading
from pathlib import Path
from types import FrameType
from typing import Optional
from urllib.request import urlopen

DEFAULT_IMAGE = "ghcr.io/umputun/ralphex-go:latest"
DEFAULT_PORT = "8080"
SCRIPT_URL = "https://raw.githubusercontent.com/umputun/ralphex/master/scripts/ralphex-dk.sh"
SENSITIVE_PATTERNS = ["KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH"]
VALID_CLAUDE_PROVIDERS = ["default", "bedrock"]
DEFAULT_DOCKER_SOCKET = "/var/run/docker.sock"

# environment variables to pass through when using bedrock provider
BEDROCK_ENV_VARS = [
    # core bedrock config (auto-set to 1 when bedrock provider is selected)
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
              RALPHEX_IMAGE          Docker image (default: ghcr.io/umputun/ralphex-go:latest)
              RALPHEX_PORT           Web dashboard port with --serve (default: 8080)
              RALPHEX_DOCKER_SOCKET  Enable Docker socket mount ("1", "true", "yes")
              RALPHEX_DOCKER_NETWORK Docker network mode (e.g., "host", "my-network")
              RALPHEX_EXTRA_ENV      Comma-separated env vars (VAR=value or VAR)
              RALPHEX_EXTRA_VOLUMES  Comma-separated volume mounts (src:dst[:opts])

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
                        help="run unit tests and exit (requires full repo)")
    parser.add_argument("-h", "--help", action="store_true", dest="help",
                        help="show this help and ralphex help, then exit")
    parser.add_argument("--claude-provider", dest="claude_provider", metavar="PROVIDER",
                        choices=VALID_CLAUDE_PROVIDERS,
                        help="claude provider: 'default' or 'bedrock' (env: RALPHEX_CLAUDE_PROVIDER)")
    parser.add_argument("--docker", action="store_true", dest="docker",
                        help="mount host Docker socket into container (env: RALPHEX_DOCKER_SOCKET)")
    parser.add_argument("--network", dest="network", metavar="MODE",
                        help="Docker network mode, e.g. 'host' (env: RALPHEX_DOCKER_NETWORK)")
    parser.add_argument("--dry-run", action="store_true", dest="dry_run",
                        help="print docker command that would be run, without executing")
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


def is_docker_enabled(cli_flag: bool) -> bool:
    """check if docker socket mount is enabled via CLI flag or env var.

    priority: CLI --docker flag > RALPHEX_DOCKER_SOCKET env var > False
    env var truthy values: "1", "true", "yes" (case-insensitive).
    """
    if cli_flag:
        return True
    return os.environ.get("RALPHEX_DOCKER_SOCKET", "").strip().lower() in ("1", "true", "yes")


def resolve_docker_socket() -> str:
    """resolve docker socket path from DOCKER_HOST env var or use default.

    if DOCKER_HOST is set with unix:// scheme, extracts the socket path.
    otherwise falls back to DEFAULT_DOCKER_SOCKET.
    non-unix schemes (tcp://, etc.) are ignored — socket mounting only works with unix sockets.
    """
    docker_host = os.environ.get("DOCKER_HOST", "").strip()
    if docker_host.startswith("unix://"):
        return docker_host[len("unix://"):]
    return DEFAULT_DOCKER_SOCKET


def get_docker_socket_gid(socket_path: str) -> Optional[int]:
    """get the group ID of the docker socket file for use inside the container.

    on macOS, docker desktop maps mounted socket to root:root (GID 0) inside
    the linux VM, so host GID is irrelevant. on linux, host GID is preserved.
    returns None if socket doesn't exist or can't be stat'd.
    """
    try:
        st = os.stat(socket_path)
    except OSError:
        return None
    if platform.system() == "Darwin":
        return 0  # docker desktop always maps to root:root inside container
    return st.st_gid


@dataclasses.dataclass
class ParsedEnvFlags:
    """parsed result from docker -e flags."""
    values: dict[str, str]  # var name -> value (resolved from os.environ for inherit form)
    explicit: set[str]  # var names with explicit VAR=value form


def parse_env_flags(extra_env: Optional[list[str]]) -> ParsedEnvFlags:
    """parse docker -e flags list into values dict and explicit names set.

    parses ["-e", "VAR=value", "-e", "VAR2", ...] format.
    for inherit-form entries (just "VAR"), resolves value from os.environ.
    tracks which vars were set with explicit values (VAR=value form).
    """
    values: dict[str, str] = {}
    explicit: set[str] = set()
    if not extra_env:
        return ParsedEnvFlags(values, explicit)
    i = 0
    while i < len(extra_env):
        if extra_env[i] == "-e" and i + 1 < len(extra_env):
            entry = extra_env[i + 1]
            if "=" in entry:
                var_name, value = entry.split("=", 1)
                values[var_name] = value
                explicit.add(var_name)
            else:
                # inherit form: use current env value if set
                if entry in os.environ:
                    values[entry] = os.environ[entry]
            i += 2
        else:
            i += 1
    return ParsedEnvFlags(values, explicit)


def build_bedrock_env_args(existing_env: Optional[list[str]] = None) -> list[str]:
    """build docker -e flags for bedrock env vars that are actually set.

    always sets CLAUDE_CODE_USE_BEDROCK=1 (since this function is only called when
    bedrock provider is explicitly selected). other vars are passed through only
    if they have values in the host environment.
    skips vars that are already explicitly set in existing_env (from -E flags).
    """
    parsed = parse_env_flags(existing_env)
    already_set = set(parsed.values.keys())

    result: list[str] = []

    # always set CLAUDE_CODE_USE_BEDROCK=1 when bedrock provider is selected
    # (this function is only called when user explicitly chose bedrock)
    if "CLAUDE_CODE_USE_BEDROCK" not in already_set:
        result.extend(["-e", "CLAUDE_CODE_USE_BEDROCK=1"])

    # check if user is providing explicit credential VALUES via -E VAR=value flags
    # if so, we should NOT inherit any session token from host env to avoid mixing
    # credentials from different sources (e.g., stale session token with new key/secret)
    # NOTE: inherit form (-E VAR) means user wants host values, so we should pass session token too
    user_provides_explicit_creds = "AWS_ACCESS_KEY_ID" in parsed.explicit or "AWS_SECRET_ACCESS_KEY" in parsed.explicit
    skip_session_token = user_provides_explicit_creds and "AWS_SESSION_TOKEN" not in already_set

    for var in BEDROCK_ENV_VARS:
        # skip vars that are already set via -E flags
        if var in already_set:
            continue
        # skip CLAUDE_CODE_USE_BEDROCK - already handled above with explicit =1
        if var == "CLAUDE_CODE_USE_BEDROCK":
            continue
        # skip session token when user provides explicit credential values to avoid mixing
        if skip_session_token and var == "AWS_SESSION_TOKEN":
            continue
        # only pass vars that exist in env AND have non-empty values
        value = os.environ.get(var, "")
        if value:
            result.extend(["-e", var])
    return result


def export_aws_profile_credentials(extra_env: Optional[list[str]] = None) -> dict[str, str]:
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
            ["aws", "configure", "export-credentials", "--profile", profile, "--output", "json"],
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


def extract_env_from_flags(extra_env: Optional[list[str]]) -> dict[str, str]:
    """extract env vars from docker -e flags list.

    parses ["-e", "VAR=value", "-e", "VAR2", ...] into dict.
    for inherit-form entries (just "VAR"), uses os.environ value.
    """
    return parse_env_flags(extra_env).values


def detect_inherited_env_vars(extra_env: list[str]) -> list[str]:
    """extract var names that use inherit form (no =value) from docker -e flags.

    parses ["-e", "VAR=value", "-e", "VAR2", ...] and returns list of var names
    that don't have explicit values (just "VAR" without "=").
    these vars won't work when copying the command to a different shell.
    """
    inherited: list[str] = []
    i = 0
    while i < len(extra_env):
        if extra_env[i] == "-e" and i + 1 < len(extra_env):
            entry = extra_env[i + 1]
            if "=" not in entry:
                inherited.append(entry)
            i += 2
        else:
            i += 1
    return inherited


def detect_explicit_secrets(extra_env: list[str]) -> list[str]:
    """detect env vars with explicit values that have sensitive names.

    parses ["-e", "VAR=value", ...] and returns list of var names where
    the var has an explicit value AND is_sensitive_name() returns True.
    used to warn users about secrets in dry-run output.
    """
    secrets: list[str] = []
    i = 0
    while i < len(extra_env):
        if extra_env[i] == "-e" and i + 1 < len(extra_env):
            entry = extra_env[i + 1]
            if "=" in entry:
                name = entry.split("=", 1)[0]
                if is_sensitive_name(name):
                    secrets.append(name)
            i += 2
        else:
            i += 1
    return secrets


def validate_bedrock_config(extra_env: Optional[list[str]] = None) -> list[str]:
    """validate bedrock configuration and return list of warning messages.

    checks for common misconfigurations when using bedrock provider:
    - AWS_REGION should be set
    - either AWS_PROFILE or AWS_ACCESS_KEY_ID should be set for credentials

    note: CLAUDE_CODE_USE_BEDROCK is auto-set to 1 when bedrock provider is selected.
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


def build_docker_command(
    image: str,
    port: str,
    volumes: list[str],
    env_vars: list[str],
    bind_port: bool,
    args: list[str],
    docker_gid: Optional[int] = None,
    network: str = "",
) -> list[str]:
    """build docker run command as a list of arguments.

    includes: docker run, interactive flag (-it when stdin is tty), --rm,
    DOCKER_GID env var (if provided, for baseimage user group setup),
    network mode, base env vars, extra env vars, port binding with RALPHEX_WEB_HOST,
    volumes, workdir, image, entrypoint, and args.
    """
    cmd = ["docker", "run"]

    interactive = sys.stdin.isatty()
    if interactive:
        cmd.append("-it")
    cmd.append("--rm")

    if network:
        cmd.extend(["--network", network])

    if docker_gid is not None:
        cmd.extend(["-e", f"DOCKER_GID={docker_gid}"])

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

    return cmd


def run_docker(image: str, port: str, volumes: list[str], env_vars: list[str], bind_port: bool, args: list[str],
               docker_gid: Optional[int] = None, network: str = "") -> int:
    """build and execute docker run command."""
    cmd = build_docker_command(image, port, volumes, env_vars, bind_port, args, docker_gid=docker_gid, network=network)

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
            return
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            try:
                proc.kill()
            except ProcessLookupError:
                pass
    try:
        proc.wait()
    except KeyboardInterrupt:
        _terminate_proc()
        try:
            proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            try:
                proc.kill()
            except ProcessLookupError:
                pass
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
    network = parsed.network or os.environ.get("RALPHEX_DOCKER_NETWORK", "")

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
            volumes = build_volumes(creds_temp, claude_home)
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
            else:
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    try:
                        proc.kill()
                    except ProcessLookupError:
                        pass
                    else:
                        try:
                            proc.wait(timeout=1)
                        except (subprocess.TimeoutExpired, ProcessLookupError):
                            pass
        _cleanup_creds()
        sys.exit(128 + signum)

    signal.signal(signal.SIGTERM, _term_handler)

    dry_run_completed = False
    try:
        # build volumes (base + extra from env var + CLI)
        volumes = build_volumes(creds_temp, claude_home)
        volumes.extend(extra_volumes)

        # docker socket mount (when --docker flag or RALPHEX_DOCKER_SOCKET env var is set)
        docker_gid: Optional[int] = None
        if is_docker_enabled(parsed.docker):
            socket_path = resolve_docker_socket()
            if os.path.exists(socket_path):
                # mount socket without :z/:Z — relabeling the docker socket can break host docker.
                # always mount to DEFAULT_DOCKER_SOCKET inside the container so the docker client
                # finds it at the standard path regardless of host DOCKER_HOST setting.
                volumes.extend(["-v", f"{socket_path}:{DEFAULT_DOCKER_SOCKET}"])
                docker_gid = get_docker_socket_gid(socket_path)
            else:
                print(f"error: --docker specified but {socket_path} not found", file=sys.stderr)
                _cleanup_creds()
                return 1

            # warn on Linux about host Docker access (macOS has VM isolation, no warning needed)
            if platform.system() == "Linux":
                print("warning: --docker mounts host Docker socket — containers have host-level Docker access",
                      file=sys.stderr)

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

        # determine port binding
        bind_port = should_bind_port(ralphex_args)

        # handle --dry-run: print command without executing
        if parsed.dry_run:
            cmd = build_docker_command(image, port, volumes, extra_env, bind_port, ralphex_args, docker_gid=docker_gid, network=network)
            inherited = detect_inherited_env_vars(extra_env)
            if inherited:
                print(f"note: inherited env vars ({', '.join(inherited)}) require these variables "
                      "to be set in your shell when running the command", file=sys.stderr)
            if creds_temp:
                print(f"note: credentials extracted to {creds_temp} (delete after use)", file=sys.stderr)
            explicit_secrets = detect_explicit_secrets(extra_env)
            if explicit_secrets:
                print(f"note: output contains explicit values for sensitive vars ({', '.join(explicit_secrets)}); "
                      "avoid sharing or logging this output", file=sys.stderr)
            print(shlex.join(cmd))
            # skip credential cleanup in dry-run mode so command is runnable
            dry_run_completed = True
            return 0

        # schedule credential cleanup (only for actual runs)
        schedule_cleanup(creds_temp)

        return run_docker(image, port, volumes, extra_env, bind_port, ralphex_args, docker_gid=docker_gid, network=network)
    finally:
        # only skip cleanup if dry-run completed successfully (user got the file path warning)
        if not dry_run_completed:
            _cleanup_creds()


def run_tests() -> None:
    """run unit tests from ralphex_dk_test module."""
    script_dir = str(Path(__file__).resolve().parent)
    # test module lives in ralphex-dk/ subdirectory next to the script
    test_dir = os.path.join(script_dir, "ralphex-dk")
    for d in (script_dir, test_dir):
        if d not in sys.path:
            sys.path.insert(0, d)
    try:
        import ralphex_dk_test
    except ImportError:
        print("error: test module not found. tests require the full repository, "
              "not a standalone install.", file=sys.stderr)
        sys.exit(1)
    ralphex_dk_test.run_tests()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print("\r\033[K", end="")
        sys.exit(130)
