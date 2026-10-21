#!/usr/bin/env python3
import argparse
import json
import os
import re
import stat
import sys
from pathlib import Path

import yaml

SCHEMA = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
FIELDS = {"$schema", "name", "version", "description", "author", "homepage", "repository", "license", "keywords", "extensions"}
SKILL_FIELDS = {"name", "description", "license", "compatibility", "metadata", "allowed-tools"}
AGENT_FIELDS = {"interface", "policy"}
AGENT_INTERFACE_FIELDS = {"display_name", "short_description", "default_prompt"}
AGENT_POLICY_FIELDS = {"allow_implicit_invocation"}
NAME_RE = re.compile(r"^[a-z0-9](?:[a-z0-9.-]{0,62}[a-z0-9])?$")


def read_regular_text(path, errors):
    try:
        mode = path.lstat().st_mode
    except OSError as exc:
        errors.append(f"{path}: cannot inspect file: {exc}")
        return None
    if not stat.S_ISREG(mode):
        errors.append(f"{path}: expected a regular file")
        return None
    try:
        return path.read_text()
    except OSError as exc:
        errors.append(f"{path}: cannot read file: {exc}")
        return None


def load_json(path, errors):
    text = read_regular_text(path, errors)
    if text is None:
        return None
    try:
        data = json.loads(text)
    except Exception as exc:
        errors.append(f"{path}: invalid JSON: {exc}")
        return None
    if not isinstance(data, dict):
        errors.append(f"{path}: expected a JSON object")
        return None
    return data


def checked_directory(root, base, value, label, errors, expected=None):
    if not isinstance(value, str) or not value:
        errors.append(f"{label}: path must be a non-empty string")
        return None
    relative = Path(value)
    if relative.is_absolute():
        errors.append(f"{label}: path must be relative: {value}")
        return None

    root = Path(os.path.abspath(root))
    candidate = Path(os.path.abspath(base / relative))
    if not candidate.is_relative_to(root):
        errors.append(f"{label}: path escapes repository root: {value}")
        return None
    if expected is not None and candidate != expected:
        errors.append(f"{label}: path must resolve to {expected}: {value}")
        return None

    current = root
    for component in candidate.relative_to(root).parts:
        current /= component
        try:
            mode = current.lstat().st_mode
        except OSError as exc:
            errors.append(f"{label}: referenced path cannot be inspected: {value}: {exc}")
            return None
        if stat.S_ISLNK(mode):
            errors.append(f"{label}: symlink path component is not allowed: {current}")
            return None
        if not stat.S_ISDIR(mode):
            errors.append(f"{label}: referenced path is not a directory: {value}")
            return None
    return candidate


def marketplace_plugin_roots(root, errors):
    path = root / ".agents/plugins/marketplace.json"
    marketplace = load_json(path, errors)
    if marketplace is None:
        return []
    plugins = marketplace.get("plugins")
    if not isinstance(plugins, list) or not plugins:
        errors.append(f"{path}: plugins must be a non-empty list")
        return []
    roots = []
    for index, entry in enumerate(plugins):
        label = f"{path}: plugins[{index}]"
        if not isinstance(entry, dict):
            errors.append(f"{label}: expected an object")
            continue
        name = entry.get("name")
        if not isinstance(name, str) or not NAME_RE.fullmatch(name):
            errors.append(f"{label}: invalid name")
        source = entry.get("source")
        if not isinstance(source, dict) or source.get("source") != "local":
            errors.append(f"{label}: source must be a local source object")
            continue
        plugin_root = checked_directory(root, root, source.get("path"), label, errors)
        if plugin_root is not None:
            roots.append((name, plugin_root))
    return roots


def validate_agent_yaml(agent_path, errors):
    text = read_regular_text(agent_path, errors)
    if text is None:
        return
    try:
        data = yaml.safe_load(text)
    except Exception as exc:
        errors.append(f"{agent_path}: invalid YAML: {exc}")
        return
    if not isinstance(data, dict):
        errors.append(f"{agent_path}: expected a mapping")
        return
    if set(data) - AGENT_FIELDS:
        errors.append(f"{agent_path}: unsupported fields {sorted(set(data) - AGENT_FIELDS)}")
    interface = data.get("interface")
    if not isinstance(interface, dict):
        errors.append(f"{agent_path}: interface must be a mapping")
    else:
        if set(interface) - AGENT_INTERFACE_FIELDS:
            errors.append(f"{agent_path}: unsupported interface fields {sorted(set(interface) - AGENT_INTERFACE_FIELDS)}")
        for key in AGENT_INTERFACE_FIELDS:
            if not isinstance(interface.get(key), str) or not interface[key].strip():
                errors.append(f"{agent_path}: interface.{key} must be a non-empty string")
    policy = data.get("policy")
    if not isinstance(policy, dict):
        errors.append(f"{agent_path}: policy must be a mapping")
    else:
        if set(policy) - AGENT_POLICY_FIELDS:
            errors.append(f"{agent_path}: unsupported policy fields {sorted(set(policy) - AGENT_POLICY_FIELDS)}")
        if policy.get("allow_implicit_invocation") is not False:
            errors.append(f"{agent_path}: policy.allow_implicit_invocation must be false")


def preflight_plugin_tree(plugin_root, errors):
    safe = True
    pending = [plugin_root]
    while pending:
        directory = pending.pop()
        try:
            with os.scandir(directory) as entries:
                for entry in entries:
                    if entry.is_symlink():
                        errors.append(f"{entry.path}: symlinks are not allowed in plugin tree")
                        safe = False
                    elif entry.is_dir(follow_symlinks=False):
                        pending.append(Path(entry.path))
        except OSError as exc:
            errors.append(f"{directory}: cannot inspect plugin tree: {exc}")
            safe = False
    return safe


def validate_plugin(root, expected_name, plugin_root, errors):
    if not preflight_plugin_tree(plugin_root, errors):
        return
    portable_path = plugin_root / "plugin.json"
    legacy_path = plugin_root / ".codex-plugin/plugin.json"
    portable = load_json(portable_path, errors)
    legacy = load_json(legacy_path, errors)
    if portable is None or legacy is None:
        return
    if portable.get("$schema") != SCHEMA:
        errors.append(f"{portable_path}: invalid $schema")
    if set(portable) - FIELDS:
        errors.append(f"{portable_path}: unsupported fields {sorted(set(portable) - FIELDS)}")
    name = portable.get("name")
    if not isinstance(name, str) or not NAME_RE.fullmatch(name) or "--" in name or ".." in name:
        errors.append(f"{portable_path}: invalid name")
    if expected_name != name:
        errors.append(f"{plugin_root}: marketplace/plugin name mismatch")
    for key in ("name", "version", "description", "author", "homepage", "repository", "license", "keywords"):
        if portable.get(key) != legacy.get(key):
            errors.append(f"{plugin_root}: portable/Codex metadata mismatch: {key}")

    fixed_skills = plugin_root / "skills"
    skills = checked_directory(
        root,
        plugin_root,
        "skills",
        f"{plugin_root}: fixed skills directory",
        errors,
        expected=fixed_skills,
    )
    legacy_skills = checked_directory(
        root,
        plugin_root,
        legacy.get("skills"),
        f"{legacy_path}: skills",
        errors,
        expected=fixed_skills,
    )
    if skills is None or legacy_skills is None:
        return

    skill_files = []
    agent_files = []
    try:
        with os.scandir(skills) as iterator:
            entries = sorted(iterator, key=lambda entry: entry.name)
    except OSError as exc:
        errors.append(f"{skills}: cannot inspect skills directory: {exc}")
        return
    for entry in entries:
        if entry.is_symlink():
            errors.append(f"{entry.path}: symlinks are not allowed in skills")
            continue
        if not entry.is_dir(follow_symlinks=False):
            errors.append(f"{entry.path}: expected a skill directory")
            continue
        skill_dir = Path(entry.path)
        skill_file = skill_dir / "SKILL.md"
        agent_file = skill_dir / "agents/openai.yaml"
        skill_files.append(skill_file)
        agent_files.append(agent_file)

    if not skill_files:
        errors.append(f"{skills}: no skills found")
    for skill_file in skill_files:
        text = read_regular_text(skill_file, errors)
        if text is None:
            continue
        match = re.match(r"^---\n(.*?)\n---\n", text, re.S)
        if not match:
            errors.append(f"{skill_file}: invalid frontmatter")
            continue
        try:
            data = yaml.safe_load(match.group(1)) or {}
        except Exception as exc:
            errors.append(f"{skill_file}: invalid YAML: {exc}")
            continue
        if not isinstance(data, dict):
            errors.append(f"{skill_file}: frontmatter must be a mapping")
            continue
        if set(data) - SKILL_FIELDS:
            errors.append(f"{skill_file}: unsupported frontmatter fields")
        if data.get("name") != skill_file.parent.name:
            errors.append(f"{skill_file}: name/directory mismatch")
        description = data.get("description")
        if not isinstance(description, str) or not 1 <= len(description) <= 1024:
            errors.append(f"{skill_file}: invalid description")
        if "allowed-tools" in data and not isinstance(data["allowed-tools"], str):
            errors.append(f"{skill_file}: allowed-tools must be a string")
        metadata = data.get("metadata")
        if metadata is not None and (not isinstance(metadata, dict) or any(not isinstance(k, str) or not isinstance(v, str) for k, v in metadata.items())):
            errors.append(f"{skill_file}: metadata values must be strings")
        if len(text.splitlines()) > 500:
            print(f"WARNING: {skill_file}: exceeds recommended 500 lines", file=sys.stderr)
    for agent_file in agent_files:
        validate_agent_yaml(agent_file, errors)


def validate(root):
    root = Path(os.path.abspath(root))
    errors = []
    marketplace_roots = marketplace_plugin_roots(root, errors)
    seen = set()
    for name, plugin_root in marketplace_roots:
        if plugin_root in seen:
            errors.append(f"{plugin_root}: duplicate marketplace plugin path")
            continue
        seen.add(plugin_root)
        validate_plugin(root, name, plugin_root, errors)
    return errors


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path(__file__).absolute().parents[1])
    args = parser.parse_args()
    errors = validate(args.root)
    if errors:
        print("\n".join(f"ERROR: {error}" for error in errors), file=sys.stderr)
        raise SystemExit(1)
    print("Portable plugin validation passed")


if __name__ == "__main__":
    main()
