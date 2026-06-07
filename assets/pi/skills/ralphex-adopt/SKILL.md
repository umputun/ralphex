---
name: ralphex-adopt
description: Convert plans from various source formats (OpenSpec, spec-kit, GitHub/GitLab issues with checklists, generic task-lists, free-form markdown) into ralphex-format plans in docs/plans/. Triggers on "ralphex-adopt", "adopt plan", "convert plan to ralphex", "import plan as ralphex".
allowed-tools: read bash write
---

# ralphex-adopt - Convert Plans Into ralphex Format

**SCOPE**: Read a source plan in some other format and produce a new ralphex-format plan at `docs/plans/YYYYMMDD-<slug>.md`. The source is never modified. Existing target files are never silently overwritten.

Supported source shapes:

- **OpenSpec change**: directory containing `proposal.md`, `tasks.md`, optional `specs/**/spec.md`
- **spec-kit spec**: directory or file with spec/plan/tasks separation
- **GitHub or GitLab issue**: URL, `#N`, or `owner/repo#N` with body that contains a task checklist
- **Generic task-list**: any structured markdown/text with headings and bullet items
- **Free-form markdown**: prose brain dump with no fixed structure

This is a single-skill conversion: discover, classify, ask focused questions when in doubt, draft, review, write. Do not modify code, do not run tests, do not commit. Output is the new plan file only.

**pi note**: pi appends skill arguments as user input — there is no `$ARGUMENTS` placeholder. References below to "the argument" mean that appended text. pi is interactive: every "ask the user" step is an inline question in chat; ask, then wait for the reply before proceeding. Use pi's built-in tools (`read`, `bash`, `write`) for all file and shell access.

## Step 0: Optional CLI Check

This check is **informational only**. Missing ralphex CLI must NOT break the flow — conversion does not require it. Do NOT block, exit, prompt the user, or wait for installation. Always continue to Step 1 regardless of the result.

```bash
which ralphex
```

If `which ralphex` returns non-zero, briefly mention that ralphex is needed to execute the converted plan later (not now), list install options once, and continue immediately:

- **macOS (Homebrew)**: `brew install umputun/apps/ralphex`
- **Linux (Debian/Ubuntu)**: download `.deb` from https://github.com/umputun/ralphex/releases
- **Linux (RHEL/Fedora)**: download `.rpm` from https://github.com/umputun/ralphex/releases
- **Any platform with Go**: `go install github.com/umputun/ralphex/cmd/ralphex@latest`

If `which ralphex` succeeds, say nothing and proceed.

## Step 1: Resolve Source From Argument Shape

Inspect the argument text (pi appends it as user input) and pick exactly one source by shape, in this order:

1. **Full URL** (starts with `http://` or `https://`):
   - GitHub issue/PR URL → use `gh issue view <url> --json title,body,labels` (or `gh pr view`)
   - GitLab issue/MR URL → use `glab issue view <url>` (or `glab mr view`)
   - Other URL → fetch with `curl -fsSL` only if it points at a raw markdown file; otherwise ask the user to paste the body

2. **Bare reference** `#N`:
   - Use the current git repository's host. Detect with `git remote get-url origin` and choose `gh` or `glab` accordingly.
   - If `git remote get-url origin` fails (not a git repo, or no `origin` remote), ask the user inline to disambiguate: "GitHub", "GitLab", "Provide qualified `owner/repo#N` instead", "Cancel". Re-resolve based on the answer.
   - GitHub: `gh issue view N --json title,body` (try `gh pr view N` if issue not found)
   - GitLab: `glab issue view N` (try `glab mr view N` if not found)

3. **Qualified reference** `owner/repo#N` or `group/project#N`:
   - GitHub: `gh issue view N --repo owner/repo`
   - GitLab: `glab issue view N --repo group/project`

4. **Existing path** — first probe the literal argument as a filesystem path with `test -e <argument>` (pi `bash`):
   - **File**: read with pi's `read` tool
   - **Directory**: list with `ls -la <path>` and inspect contents
     - If contains `proposal.md` AND `tasks.md` → likely OpenSpec, proceed to Step 2
     - If contains a single `*.md` → use that file
     - Otherwise ask the user which file inside the directory is the plan

5. **Bare name** — only if the argument failed every check above (not a URL, not `#N` or `owner/repo#N`, and `test -e` returned false). A bare name has no path separators and contains no path-like characters:
   - Search the filesystem with pi `bash` (`find . -iname '*<name>*.md'`, and check for `*<name>*/proposal.md`) for plausible matches
   - If exactly one match → use it
   - If multiple matches → ask the user inline to pick one (offer up to 4 most relevant; if more, summarize and ask the user to paste the path)
   - If no matches → ask the user whether they meant a path, an issue number, or something else

6. **No argument**:
   - Ask the user inline: "Where is the source plan?" with options like "Paste it", "Provide a file path", "Provide an issue number/URL", "Cancel".

After resolving, store: source kind (`github-issue`, `gitlab-issue`, `file`, `directory`, `pasted`), source content (full text or directory listing + key files), and source identifier for the slug suggestion.

## Step 2: Detect Format

Look at the resolved content and classify it as one of:

- **OpenSpec**: directory has both `proposal.md` and `tasks.md`. May also have `specs/**/spec.md` deltas.
- **spec-kit**: directory or single file shows the spec-kit shape — separate spec/plan/tasks sections, often with explicit "Specification", "Implementation Plan", "Tasks" headings.
- **Issue with checklist**: source kind is `github-issue` or `gitlab-issue`, and the body contains one or more `- [ ]` items.
- **Generic task-list**: any structured source with headings and bullet items that is not OpenSpec, spec-kit, or an issue. Section heading style and item-marker style may vary.
- **Free-form**: prose-only or near-prose source with no clear task list. Includes brain-dump style text.

If multiple signals point in different directions (e.g., a directory with both a `proposal.md` and a clearly spec-kit-shaped `plan.md`), ask the user to confirm which format to use before drafting.

## Step 3: Confidence Guard — Ask Before Drafting

Before writing any draft, scan the source for items the agent cannot confidently map. For each uncertainty, ask the user **before drafting**, never embed placeholder markers (`???`, `TBD`, `[FIXME]`) into the converted plan.

Common uncertainties:

- Which headings should become Task sections vs. Overview/Context vs. Technical Details?
- How should a long flat list be split into Tasks (logical phases vs. fixed groups)?
- A bullet item is vague ("clean up the auth module") — what concrete steps are intended?
- Source mixes intent (feature + refactor + bug fix) — should this become one plan or be flagged as multi-plan?
- Source is in a non-English natural language — ask whether to translate Overview/Context prose or preserve the original (the structural keyword `Task` in headers is always English regardless).
- Source is very large (>1000 lines) or very small (<10 lines) — confirm scope before processing.

Ask inline with concrete options. If the question is genuinely open-ended (more than 4 possibilities), present a numbered list in chat and ask the user to reply with a number.

Do not draft, then ask. Ask, then draft.

## Step 4: Convert Per Format

All converted plans must satisfy ralphex's plan-format rules:

- File starts with `# <Plan Title>` H1.
- Standard sections in order: `## Overview`, `## Context`, `## Development Approach`, `## Testing Strategy`, `## Progress Tracking`, optional `## Technical Details` (when source has architecture/spec details to preserve), `## Implementation Steps`, optional `## Post-Completion`.
- Task headers use the structural form `### Task <N>: <title>`. The keyword `Task` is **always English**, even when the plan title and task titles are in another natural language. ralphex's plan parser only recognizes English `Task` and `Iteration` keywords; localized variants (`Задача`, `タスク`, `Tarea`, etc.) will not be detected.
- Checkboxes (`- [ ]` / `- [x]`) appear **only inside Task sections**. Do not put checkboxes in Overview, Context, Success criteria, or any other section — they cause the executor to spawn extra iterations.
- Every Task should end with a "write tests" checkbox and a "run project tests" checkbox, phrased generically (project may be in any language).
- The final Task is always `### Task <last>: Verify acceptance criteria` containing items that re-run the test suite, run the project linter, and confirm requirements from Overview were met.

Per-format mapping rules:

### OpenSpec

- `proposal.md` "## Why" or equivalent → `## Overview` (the problem statement and motivation)
- `proposal.md` "## What Changes" → `## Context` (impacted components and constraints)
- `specs/**/spec.md` delta sections (ADDED / MODIFIED / REMOVED requirements) → `## Technical Details` (concrete behavior changes)
- `tasks.md` numbered list → `## Implementation Steps` grouped into `### Task N:` sections. Each top-level numbered group becomes a Task; sub-bullets become checkboxes.
- Add `write tests` and `run project tests` checkboxes to each Task even if absent in source.
- Append a final `### Task <last>: Verify acceptance criteria` Task.

### spec-kit

- "Specification" section → `## Overview` and `## Context`
- "Implementation Plan" / architecture section → `## Technical Details`
- "Tasks" section → `## Implementation Steps` with one `### Task N:` per logical phase
- Add `write tests`, `run project tests`, and final `Verify acceptance criteria` Task.

### GitHub / GitLab Issue with Checklist

- Issue title → `# <Plan Title>` (drop trailing punctuation, normalize whitespace)
- Issue body prose above the first checklist → `## Overview`
- Issue labels and metadata → `## Context` (e.g., "Reported in repo X, labels: bug, p1, area/auth")
- Top-level `- [ ]` items in body → `## Implementation Steps`
  - If the body has H3 sub-headings that group items, preserve those grouping into Tasks.
  - Otherwise, group every 5–7 items into one Task; create a synthetic title summarizing the group.
- Preserve `- [x]` checked state from the source.
- Add `write tests` and `run project tests` per Task; append final `Verify acceptance criteria` Task.

### Generic Task-List

- Infer the heading style (`#`, `##`, `###`, or numbered headings) from the source.
- Infer the item style (`- [ ]`, `* [ ]`, `1.`, `-`, plain dashes).
- Normalize:
  - Top-level grouping headings become `### Task N: <title>` (use English `Task` keyword regardless of the source language).
  - Item lines become `- [ ]` checkboxes inside the Task.
  - Preserve checked state if the source uses any form of "done" marker.
- If grouping is unclear (single flat list, ambiguous heading hierarchy), ask the user before drafting how to split.
- Add `write tests`, `run project tests`, and final `Verify acceptance criteria` Task.

### Free-Form Markdown

- Infer intent from the prose (feature / bug fix / refactor / migration / docs).
- First paragraph or two → `## Overview`.
- Background, constraints, references → `## Context`.
- Decompose the body into 3–7 Task groups by logical phase (read carefully; do not invent steps the source does not imply).
- For each Task, write 3–6 concrete checkboxes that map directly to phrases in the source. Do not embed `[FIXME]` or `???` — if a phrase is too vague, ask the user in Step 3 first.
- Add `write tests`, `run project tests`, and final `Verify acceptance criteria` Task.

### Output Skeleton (all formats)

```markdown
# <Plan Title>

## Overview

<one or two paragraphs describing what is being built and why>

## Context

- <impacted components>
- <relevant constraints>
- <reference to source: e.g., "Adopted from issue #312" or "Adopted from OpenSpec change auth-rework">

## Development Approach

- Testing approach: regular (or TDD if source explicitly calls it out)
- Complete each task fully before moving to the next
- Update this plan when scope changes during implementation

## Testing Strategy

- Unit tests required for every code-changing Task
- Run project tests after each Task before proceeding

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Update plan if implementation deviates from original scope

## Technical Details

<optional: detailed behavior, data shapes, references to spec sections; omit this section if the source had no such content>

## Implementation Steps

### Task 1: <title>

- [ ] <concrete action>
- [ ] <concrete action>
- [ ] write tests for new functionality
- [ ] run project tests - must pass before next task

### Task 2: <title>

- [ ] <concrete action>
- [ ] write tests for new/changed functionality
- [ ] run project tests - must pass before next task

### Task <last>: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] run full project test suite
- [ ] run project linter - all issues must be fixed

## Post-Completion

*Items requiring manual intervention - no checkboxes, informational only*

- <manual verification steps if any>
- <external system updates if any>
```

## Step 5: Review Loop With revdiff

Create a temp file and capture its path. Each pi `bash` call runs in its own subshell, so shell variables (including `$DRAFT`) do not persist between calls. You must capture the literal path printed by `mktemp` and substitute that exact string into every subsequent tool call (`write`, revdiff, `rm`) — do not rely on `$VAR` references across calls.

Use a portable `mktemp` form. The `-t prefix` form differs between macOS BSD and Linux GNU. A template ending in `XXXXXX` is portable, but a suffix after `XXXXXX` (e.g., `XXXXXX.md`) is silently treated as a literal filename by BSD `mktemp` and would cause concurrent runs to collide on the same path. Generate the random path first, then rename to add the `.md` extension:

```bash
TMP=$(mktemp "${TMPDIR:-/tmp}/ralphex-adopt-XXXXXX") && mv "$TMP" "$TMP.md" && printf '%s\n' "$TMP.md"
```

Read the path from stdout (e.g., `/tmp/ralphex-adopt-aB3xY9.md`) and remember it. Refer to that literal string below as `<draft-path>`. Write the draft content to `<draft-path>` via pi's `write` tool.

An `EXIT` trap is not used because each `bash` call is its own subshell — the trap would fire immediately. Cleanup is explicit at the end of Step 6 (success) and on every cancel path (`rm -f <draft-path>` with the literal path substituted).

pi has no Claude marketplace plugin layout, so there is no `launch-revdiff.sh` launcher. Instead, detect the `revdiff` binary on `PATH` and run it directly on the draft, writing any annotations to a temp output file. Substitute the literal `<draft-path>` you captured above, and capture the output path the same way:

```bash
OUT=$(mktemp "${TMPDIR:-/tmp}/ralphex-adopt-rev-XXXXXX") && printf '%s\n' "$OUT"
command -v revdiff >/dev/null 2>&1 && revdiff --wrap --untracked --only=<draft-path> --output=<output-path>
```

If `command -v revdiff` fails (binary not installed), skip directly to the in-chat fallback below.

After revdiff exits, read the captured `<output-path>` file with pi's `read` tool:

- **Empty output file** → user reviewed and approved silently. Proceed to Step 6.
- **Non-empty output file** → user left annotations. Read each annotation, revise the draft accordingly (rewrite the literal `<draft-path>` in place via `write`), then re-run revdiff with a fresh output file. Repeat until the output file is empty. Clean up each `<output-path>` with `rm -f` when done.

If the `revdiff` binary is missing, OR revdiff fails with any revdiff-related error (non-zero exit with "revdiff" in stderr — "not found in PATH", "command not found", etc.), fall back to in-chat review:

- Print the draft content in chat.
- Ask the user inline: "Approve draft?" with options "Accept", "Revise" (capture feedback as next message), "Reject" (cancel the conversion).
- On "Revise", treat the next user message as annotation text and revise; loop until "Accept".

## Step 6: Write Target File

Compute the target filename:

- Date: today's date in `YYYYMMDD` form (no dashes, e.g., `20260430`).
- Slug: derive from the plan title — lowercase, ASCII-only, words joined by `-`, max ~50 characters. Drop articles (a/an/the) and trailing punctuation.

Ask the user inline to confirm or edit the slug before writing:

- header: "Filename"
- question: "Use slug `<computed-slug>` for `docs/plans/<date>-<slug>.md`?"
- options:
  - label: "Yes, use this slug"
  - label: "Edit slug" (capture next user message as the new slug)
  - label: "Cancel"

If the target file already exists:

- Ask the user inline: "`docs/plans/<filename>` already exists. What should we do?"
- options:
  - label: "Bump suffix" — append `-v2`, then `-v3`, ... to the slug; check `docs/plans/` and `docs/plans/completed/` for collisions, increment until both are clear
  - label: "Pick a new slug" (capture next message)
  - label: "Cancel"
- Never silent-overwrite.

Sanity-check the draft before writing:

- The draft must contain at least one `### Task ` line that matches the form `### Task <N>: <title>`.
- The draft must contain at least one `- [ ]` checkbox under a Task section.
- If either check fails, return to Step 4 to revise (do not write the file).

Once the filename is confirmed and sanity checks pass:

```bash
mkdir -p docs/plans
```

Write the draft content to `docs/plans/<final-name>.md` via pi's `write` tool. Then explicitly clean up the temp file by substituting the literal `<draft-path>` captured in Step 5:

```bash
rm -f <draft-path>
```

Also run the same `rm -f <draft-path>` on any cancel path before exiting (Step 1, Step 3, Step 5 reject, Step 6 cancel) — always with the literal path substituted, never as `$DRAFT`.

Report to the user:

```
Adopted plan: docs/plans/<final-name>.md

Source: <source kind and identifier>
Tasks: <N>

Next: run `ralphex docs/plans/<final-name>.md` to execute.
```

## Edge Cases

- **Missing path**: if user passed a path that does not exist, ask the user to correct or cancel.
- **Ambiguous bare name**: more than one match — ask the user to pick.
- **URL fetch failure**: ask the user to paste body as fallback.
- **Directory with no recognizable structure**: list contents, ask the user to point at the file.
- **Format detection conflict**: multiple signals — ask the user to choose format.
- **Zero task-like content**: source has no items the agent can convert — ask the user whether to infer Tasks from prose or cancel.
- **Mixed localization**: source mixes English and another language — confirm whether to keep the original language for prose. Structural `Task` keyword stays English regardless.
- **Huge source (>1000 lines)**: warn before processing and ask the user whether to proceed, summarize, or split into multiple plans.
- **Tiny source (<10 lines)**: warn that the result will be sparse; ask the user whether to proceed or expand interactively.
- **Output collision**: target file already exists — never silent overwrite (see Step 6).
- **Idempotency**: re-running on the same source uses today's date. Old converted plans in `docs/plans/completed/` are never modified.

## Tool Fallbacks

- **revdiff missing**: fall back to the in-chat Accept/Revise/Reject loop (see Step 5).
- **gh missing** (when source is a GitHub issue/URL): ask the user to paste the issue body manually.
- **glab missing** (when source is a GitLab issue/URL): ask the user to paste the issue body manually.
- **Both gh and glab missing for a `#N` argument**: ask the user to paste the issue body or provide a different reference.

## Constraints

- Never modify the source plan or directory.
- Never write to `docs/plans/` without an explicit user-confirmed slug.
- Never silently overwrite an existing target file.
- Never embed placeholder markers (`???`, `TBD`, `[FIXME]`) in the output — ask the user before drafting instead.
- Never assume the target project is a specific language. Test/run-test checkboxes must use generic phrasing such as "write tests" and "run project tests".
- Never cite ralphex internal source files (e.g., `pkg/...`) in the converted plan content.
- Do not run tests, do not run linters, do not commit, do not push. The skill only produces a plan file.
