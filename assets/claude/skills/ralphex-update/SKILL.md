---
description: Smart-merge updated ralphex defaults into customized prompts/agents
allowed-tools: [Bash, Read, Write, Glob, AskUserQuestion]
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

## Step 3: Compare Files

For each file in the defaults dump (`config`, `prompts/*.txt`, `agents/*.txt`), compare with the corresponding file in the user's config directory.

**Classify each file into one of these categories:**

### Category A: Missing or All-Commented (auto-update candidate)
- File doesn't exist in user's config, OR
- File exists but contains only comments and whitespace (never customized)
- **Action**: can be auto-updated with new commented default

### Category B: Customized (needs smart merge)
- File exists with actual (uncommented) content that differs from the new default
- **Action**: needs Claude to semantically analyze and propose merge

### Category C: No Change Needed
- File matches the current default (either exact match or equivalent content)
- **Action**: skip

### Category D: New File
- File exists in defaults but has no corresponding file in user's config at all
- **Action**: offer to install as commented template

**How to classify**: Read both the default file (from dump dir) and the user's file. Check if user's file has any uncommented lines. Compare the default content with user's content.

## Step 4: Present Summary

Show the user a summary grouped by category:

```
ralphex config update summary:

No changes needed:
  prompts/task.txt, prompts/review_first.txt, agents/quality.txt

Auto-update (never customized):
  prompts/codex.txt, agents/documentation.txt

Smart merge needed (customized):
  prompts/review_second.txt, agents/implementation.txt

New files available:
  prompts/finalize.txt
```

Use AskUserQuestion to confirm proceeding:
- header: "Proceed"
- question: "Apply updates? Auto-updates will install new commented defaults. Smart merges will be reviewed one by one."
- options:
  - label: "Yes, proceed"
    description: "Apply auto-updates and review smart merges one at a time"
  - label: "Skip, just show details"
    description: "Show what changed without modifying anything"

## Step 5: Process Auto-Updates (Category A and D)

For files that were never customized (all-commented or missing):
1. Read the new default content
2. Comment out all lines (prefix with `# `)
3. Write to the user's config directory

Report: "Updated N files with new defaults (commented out)"

## Step 6: Process Smart Merges (Category B)

For each customized file that needs merging:

1. **Read both versions** - the new default and the user's current version
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
5. **Use AskUserQuestion** for each file:
   - header: "Merge"
   - question: "How to handle <filename>?"
   - options:
     - label: "Accept merge"
       description: "Use the proposed merged version"
     - label: "Keep mine"
       description: "Keep your current version unchanged"
     - label: "Use new default"
       description: "Replace with new default (discard customizations)"

6. Apply the user's choice

## Step 7: Cleanup

Remove the temp directory:

```bash
rm -rf <dump-dir>
```

Report final summary:
```
Update complete:
  Auto-updated: N files
  Smart-merged: N files (M accepted, K kept)
  Skipped: N files (no changes)
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
