---
name: ralphex-update
description: Smart-merge updated ralphex defaults into customized prompts/agents
allowed-tools: read bash write
---

# ralphex-update - Smart Prompt Merging

**SCOPE**: Compare current embedded defaults with user's installed config, and intelligently merge updates into customized files. Preserves user intent while incorporating structural changes.

## Step 0: Verify CLI Installation

```bash
which ralphex
```

**If not found**, guide installation:
- **macOS (Homebrew)**: `brew install umputun/apps/ralphex`
- **Any platform with Go**: `go install github.com/umputun/ralphex/cmd/ralphex@latest`

**Do not proceed until `which ralphex` succeeds.**

## Step 1: Extract Current Defaults

Create temp directory and dump embedded defaults:

```bash
DUMP_DIR=$(mktemp -d /tmp/ralphex-defaults-XXXX)
ralphex --dump-defaults "$DUMP_DIR"
echo "$DUMP_DIR"
```

Save the dump directory path for later use.

## Step 2: Determine Config Directory

Resolve the user's config directory:

```bash
# check environment variable first
echo "${RALPHEX_CONFIG_DIR:-}"
```

If `RALPHEX_CONFIG_DIR` is empty, use default:
- **macOS/Linux**: `~/.config/ralphex/`

Verify the directory exists:
```bash
ls -la <config-dir>/
```

If it doesn't exist, inform user that ralphex hasn't been configured yet and there's nothing to update.

## How ralphex Config Files Work

ralphex installs config, prompt, and agent files with all content **commented out** (every line prefixed with `# `). At runtime, `stripComments()` removes these lines, finds nothing, and falls back to **embedded defaults** compiled into the binary. These all-commented files are functionally identical to missing files — they are do-nothing placeholders.

When ralphex is updated, new embedded defaults take effect **automatically** for every file that hasn't been customized. No file changes are needed.

A file is **customized** only if it contains at least one uncommented, non-empty line that was intentionally modified by the user. The `--dump-defaults` command produces the raw (uncommented) embedded content for comparison.

## Step 3: Compare Files

For each file in the defaults dump (`config`, `prompts/*.txt`, `agents/*.txt`), compare with the corresponding file in the user's config directory. Use pi's `read` tool (or `bash` with `diff`/`grep`) to read and compare both sides.

**Algorithm to detect customized files**: a file is customized if it contains at least one non-empty line that does NOT start with `#`. Files that are missing, empty, or contain only comment lines (`# ...`) and whitespace are do-nothing defaults.

**Classify each file into one of these categories:**

### Skip (do-nothing default)
- File is missing in user's config, OR
- File is empty, OR
- File contains only comments and whitespace (every non-empty line starts with `#`)
- **Action**: no action needed — embedded defaults handle it automatically

Note: files that exist only in the dump directory (no corresponding user file) are also do-nothing — do NOT offer to install them. Files that exist only in the user's config directory (no corresponding dump file) are user-created custom files — ignore them entirely.

### Skip (unchanged)
- File has uncommented content that matches the raw dump default
- **Action**: no action needed — user's file already matches current defaults

**How to compare**: strip all `#`-prefixed lines from BOTH the user's file and the dump file, then compare the remaining non-comment content. This handles the config file where the dump has descriptive comment lines mixed with value lines — only the actual values matter for comparison.

### Smart merge needed
- File has uncommented content that differs from the raw dump default (after stripping `#`-prefixed lines from both sides)
- **Action**: needs the agent to semantically analyze and propose a merge

## Step 4: Present Summary

Show the user a summary with two groups:

```
ralphex config update summary:

No changes needed (N files):
  prompts/task.txt, prompts/review_first.txt, agents/quality.txt, prompts/codex.txt, ...

Smart merge needed (N files):
  prompts/review_second.txt, agents/implementation.txt
```

If nothing needs merging, report "all config files are up to date — no changes needed" and skip to cleanup.

Otherwise, ask the user inline whether to proceed (pi is interactive — ask directly and wait for the reply):
- **Yes, proceed**: Review and merge customized files one at a time
- **Skip, just show details**: Show what changed without modifying anything

If user chooses "Skip, just show details": for each file needing smart merge, show the diff between the user's file and the new default, then skip to Step 6 (Cleanup) without modifying any files.

## Step 5: Process Smart Merges

For each customized file that needs merging:

1. **Read both versions** - the new default and the user's current version (pi `read`)
2. **Analyze the differences semantically**:
   - What did the user customize? (added content, changed wording, different instructions)
   - What changed in the new default? (structural changes, new template variables, new sections, removed sections)
3. **Propose a merged version** that:
   - Preserves user additions not present in defaults
   - Applies structural/pattern changes from new defaults
   - Updates template variable references (e.g., new `{{VARIABLE}}` usage)
   - Preserves user's tone and style choices
   - Flags direct conflicts where both changed the same thing
4. **Show the user**:
   - Brief summary of what changed in defaults
   - Brief summary of what user customized
   - The proposed merged version
5. **Ask the user inline** for each file how to handle `<filename>`:
   - **Accept merge**: Use the proposed merged version
   - **Keep mine**: Keep your current version unchanged
   - **Use new default**: Replace with new default (discard customizations)

6. Apply the user's choice with pi's `write` tool

## Step 6: Cleanup

Remove the temp directory:

```bash
rm -rf <dump-dir>
```

Report final summary:
```
Update complete:
  Skipped: N files (no changes needed)
  Smart-merged: N files (M accepted, K kept)
```

## Merge Principles

When proposing smart merges, follow these rules:

- **Preserve user additions**: content the user added that doesn't exist in defaults should be kept
- **Apply structural changes**: if defaults restructured prompts (e.g., changed from sequential to parallel agents), apply the new structure while keeping user's custom content
- **Update template variables**: if new `{{VARIABLE}}` references were added to defaults, include them in the merge
- **Preserve user tone/style**: if user rewrote instructions in a different style, keep their style while incorporating new functionality
- **Flag conflicts clearly**: if both user and defaults changed the same section differently, present both versions and let the user choose
- **Don't lose information**: when in doubt, keep both versions with clear markers

## Constraints

- This command is ONLY for updating ralphex configuration files
- Do NOT modify any project source code
- Do NOT run ralphex execution or review
- Do NOT touch files outside the config directory
- Always clean up the temp directory when done
