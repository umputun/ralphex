#!/usr/bin/env python3
import json, re, sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
SCHEMA = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
FIELDS = {"$schema", "name", "version", "description", "author", "homepage", "repository", "license", "keywords", "extensions"}
SKILL_FIELDS = {"name", "description", "license", "compatibility", "metadata", "allowed-tools"}
NAME_RE = re.compile(r"^[a-z0-9](?:[a-z0-9.-]{0,62}[a-z0-9])?$")

errors = []
legacy_paths = list(ROOT.glob("plugins/*/.codex-plugin/plugin.json"))
if not legacy_paths:
    errors.append(f"{ROOT / 'plugins'}: no portable plugins found")
for legacy_path in legacy_paths:
    plugin_root = legacy_path.parents[1]
    portable_path = plugin_root / "plugin.json"
    if not portable_path.is_file():
        errors.append(f"{plugin_root}: missing root plugin.json")
        continue
    try:
        portable = json.loads(portable_path.read_text())
        legacy = json.loads(legacy_path.read_text())
    except Exception as exc:
        errors.append(f"{plugin_root}: invalid JSON: {exc}")
        continue
    if portable.get("$schema") != SCHEMA: errors.append(f"{portable_path}: invalid $schema")
    if set(portable) - FIELDS: errors.append(f"{portable_path}: unsupported fields {sorted(set(portable) - FIELDS)}")
    name = portable.get("name")
    if not isinstance(name, str) or not NAME_RE.fullmatch(name) or "--" in name or ".." in name:
        errors.append(f"{portable_path}: invalid name")
    for key in ("name", "version", "description", "author", "homepage", "repository", "license", "keywords"):
        if portable.get(key) != legacy.get(key): errors.append(f"{plugin_root}: portable/Codex metadata mismatch: {key}")
    for path in plugin_root.rglob("*"):
        if path.is_symlink() and not path.resolve().is_relative_to(plugin_root.resolve()):
            errors.append(f"{path}: symlink escapes plugin root")
    skills = plugin_root / "skills"
    if skills.exists() and not skills.is_dir(): errors.append(f"{skills}: fixed skill location is not a directory")
    if skills.is_dir():
        for skill_file in skills.glob("*/SKILL.md"):
            text = skill_file.read_text()
            match = re.match(r"^---\n(.*?)\n---\n", text, re.S)
            if not match:
                errors.append(f"{skill_file}: invalid frontmatter")
                continue
            try:
                data = yaml.safe_load(match.group(1)) or {}
            except Exception as exc:
                errors.append(f"{skill_file}: invalid YAML: {exc}")
                continue
            if set(data) - SKILL_FIELDS: errors.append(f"{skill_file}: unsupported frontmatter fields")
            if data.get("name") != skill_file.parent.name: errors.append(f"{skill_file}: name/directory mismatch")
            description = data.get("description")
            if not isinstance(description, str) or not 1 <= len(description) <= 1024: errors.append(f"{skill_file}: invalid description")
            if "allowed-tools" in data and not isinstance(data["allowed-tools"], str): errors.append(f"{skill_file}: allowed-tools must be a string")
            metadata = data.get("metadata")
            if metadata is not None and (not isinstance(metadata, dict) or any(not isinstance(k, str) or not isinstance(v, str) for k, v in metadata.items())):
                errors.append(f"{skill_file}: metadata values must be strings")
            if len(text.splitlines()) > 500: print(f"WARNING: {skill_file}: exceeds recommended 500 lines", file=sys.stderr)
if errors:
    print("\n".join(f"ERROR: {error}" for error in errors), file=sys.stderr)
    raise SystemExit(1)
print("Portable plugin validation passed")
