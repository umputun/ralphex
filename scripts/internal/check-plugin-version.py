#!/usr/bin/env python3
import argparse
import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MANIFESTS = (
    (".claude-plugin/plugin.json", ("version",)),
    (".claude-plugin/marketplace.json", ("plugins", 0, "version")),
    ("plugins/ralphex/.codex-plugin/plugin.json", ("version",)),
    ("plugins/ralphex/plugin.json", ("version",)),
)
PAYLOAD_PATHS = ("assets/claude/skills", "plugins/ralphex/skills")


def json_version(data, components):
    value = data
    for component in components:
        value = value[component]
    if not isinstance(value, str) or not value:
        raise ValueError("version must be a non-empty string")
    return value


def current_versions(root):
    versions = {}
    for path, components in MANIFESTS:
        try:
            data = json.loads((root / path).read_text())
            versions[path] = json_version(data, components)
        except Exception as exc:
            raise ValueError(f"{path}: cannot read plugin version: {exc}") from exc
    return versions


def base_versions(root, base):
    versions = {}
    for path, components in MANIFESTS:
        exists = subprocess.run(
            ["git", "cat-file", "-e", f"{base}:{path}"],
            cwd=root,
            capture_output=True,
            text=True,
            check=False,
        )
        if exists.returncode != 0:
            continue
        result = subprocess.run(
            ["git", "show", f"{base}:{path}"],
            cwd=root,
            capture_output=True,
            text=True,
            check=False,
        )
        if result.returncode != 0:
            raise ValueError(f"{path}: cannot read version from base {base}: {result.stderr.strip()}")
        try:
            versions[path] = json_version(json.loads(result.stdout), components)
        except Exception as exc:
            raise ValueError(f"{path}: invalid version at base {base}: {exc}") from exc
    return versions


def common_version(versions, label):
    unique = set(versions.values())
    if len(unique) != 1:
        details = ", ".join(f"{path}={version}" for path, version in versions.items())
        raise ValueError(f"{label} plugin versions do not agree: {details}")
    return unique.pop()


def payload_changed(root, base):
    result = subprocess.run(
        ["git", "diff", "--quiet", base, "--", *PAYLOAD_PATHS],
        cwd=root,
        check=False,
    )
    if result.returncode not in (0, 1):
        raise ValueError(f"cannot compare distributed skill payload with base {base}")
    if result.returncode == 1:
        return True
    untracked = subprocess.run(
        ["git", "ls-files", "--others", "--exclude-standard", "--", *PAYLOAD_PATHS],
        cwd=root,
        capture_output=True,
        text=True,
        check=True,
    )
    return bool(untracked.stdout.strip())


def validate(root, base):
    current = common_version(current_versions(root), "current")
    if base and set(base) == {"0"}:
        base = ""
    if not base:
        return f"Plugin manifests agree on version {current}; no git base provided"

    verify = subprocess.run(
        ["git", "rev-parse", "--verify", f"{base}^{{commit}}"],
        cwd=root,
        capture_output=True,
        text=True,
        check=False,
    )
    if verify.returncode != 0:
        raise ValueError(f"git base is not available: {base}")
    resolved_base = verify.stdout.strip()
    baseline_versions = base_versions(root, resolved_base)
    baseline = common_version(baseline_versions, f"base {base}") if baseline_versions else None
    changed = payload_changed(root, resolved_base)
    if changed and baseline is not None and current == baseline:
        raise ValueError(
            f"distributed skill payload changed from {base}, but plugin version remains {current}"
        )
    if changed and baseline is None:
        state = "new with no plugin version present at the base"
    else:
        state = "changed with a version bump" if changed else "unchanged"
    return f"Plugin manifests agree on version {current}; payload is {state} from {base}"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="", help="git commit to compare distributed skill payload against")
    parser.add_argument("--root", type=Path, default=ROOT)
    args = parser.parse_args()
    try:
        message = validate(args.root.resolve(), args.base)
    except (OSError, subprocess.SubprocessError, ValueError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise SystemExit(1)
    print(message)


if __name__ == "__main__":
    main()
