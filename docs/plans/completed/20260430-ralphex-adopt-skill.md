# Add ralphex-adopt skill for converting plans from various formats

## Overview

Add a new Claude Code skill `ralphex-adopt` that converts source plans from various formats into ralphex-format plans in `docs/plans/`. The skill complements the existing `ralphex-plan` (creates plan from scratch) and `ralphex-update` (merges defaults) skills, addressing the gap where users have an existing plan or spec in some other format and want to run it through ralphex without manual rewriting.

The source plan is never modified. The output is always a new dated file at `docs/plans/YYYYMMDD-<slug>.md`. The skill handles five input shapes: OpenSpec change directories, spec-kit specs, GitHub/GitLab issues with checklists, generic task-lists in unknown structured formats, and free-form markdown brain dumps.

The skill is a single markdown file that instructs the agent through the conversion. It uses revdiff for the review loop on the converted draft and never silently overwrites existing files. On any uncertainty during conversion, the skill asks the user via AskUserQuestion before drafting rather than embedding placeholder markers in the output.

## Context (from discovery)

- Project: ralphex (Go CLI for autonomous Claude Code plan execution).
- Existing ralphex skills live under `assets/claude/skills/<name>/SKILL.md` with a flat symlink `assets/claude/<name>.md` pointing to the same file. Plugin reads via `.claude-plugin/plugin.json` `"skills": "./assets/claude/skills/"`.
- Existing skills: `ralphex` (run execution), `ralphex-plan` (create plan), `ralphex-update` (merge updated defaults). All single-file, model-driven, no per-format reference docs.
- Plan parser: `pkg/plan/parse.go` line 46, `taskHeaderPattern = regexp.MustCompile(`^###\s+(?:Task|Iteration)\s+([^:]+?):\s*(.*)$`)`. Task and Iteration keywords MUST stay English even when plan content is localized.
- Plan checkbox rule: checkboxes belong only inside Task sections. Headers like Overview, Context, Success criteria must not contain `- [ ]` (causes extra loop iterations).
- Plan filename convention: `YYYYMMDD-<slug>.md` (no dashes inside the date), per recent files in `docs/plans/completed/`.
- Plugin version is in two places that must stay aligned: `.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json`. Currently both at 0.18.0.
- llms.txt has an install-block convention for each skill (fetch URL, write target path).
- revdiff invoked via `~/.claude/plugins/marketplaces/revdiff/.claude-plugin/skills/revdiff/scripts/launch-revdiff.sh --wrap --only=<file>` for direct review (the wrapper `~/.claude/scripts/draft-review.sh` runs a writing-style lint that misfires on plan content and writes a gh/glab approval marker the skill does not need).
- Today's date for the plan filename: 2026-04-30 → `20260430-ralphex-adopt-skill.md`.

## Development Approach

- Testing approach: regular (the skill is a markdown contract, not Go code, so there are no unit tests to write; correctness is verified by reading the skill end-to-end and by a manual dry-run trigger).
- Complete each task fully before moving to the next.
- No backward-compat concerns: this is a new file, no existing callers.
- Plugin version bump is required because skill files under `assets/claude/` are changing (per CLAUDE.md "Plugin version" rule).

## Testing Strategy

- Unit tests: not applicable. The skill is markdown.
- Verification approach: after each task, re-read modified files and confirm content. After the final task, manually trigger the skill in a fresh Claude session with a sample free-form markdown plan and confirm the conversion produces a valid ralphex-format plan that satisfies `pkg/plan/parse.go`'s `taskHeaderPattern`.
- E2E: not required for v1. The existing `prep-toy-test.sh` workflow could be extended later with a ralphex-adopt round-trip if desired.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with the prefix shown in CLAUDE.md if scope changes during implementation.
- Update plan if implementation deviates from original scope.

## Solution Overview

Single markdown skill file at `assets/claude/skills/ralphex-adopt/SKILL.md` (real file) with a symlink at `assets/claude/ralphex-adopt.md` pointing to it. The skill describes a seven-step flow (Step 0 through Step 6): verify CLI, resolve source from arg shape, detect format, confidence-guard (ask user up front on ambiguity), convert per format, review via revdiff on a temp draft, write to `docs/plans/` with collision guard. Format-specific mapping rules live inline in the SKILL.md, not in separate reference files, matching the convention of the existing three ralphex skills.

The skill never modifies the source. It never overwrites an existing target without the user picking a new slug. revdiff is invoked directly so the writing-style lint gate inside `draft-review.sh` does not misfire on plan-shaped content. If revdiff is not installed, the skill falls back to in-chat AskUser Accept/Revise/Reject.

**Language-agnostic skill output**: this plan references ralphex Go source files (such as `pkg/plan/parse.go`) where useful for the implementer to understand format constraints, but the resulting SKILL.md content must be language-agnostic. The skill is consumed by users whose target projects can be any language; it must not assume Go, must not emit Go-specific commands or paths, and must not cite ralphex internal source files in the user-facing skill text. Test/run-test checkboxes the skill instructs the agent to emit must be phrased generically ("write tests", "run project tests"), matching the convention of `assets/claude/skills/ralphex-plan/SKILL.md`.

## Technical Details

YAML frontmatter for SKILL.md:

```yaml
---
description: Convert plans from various source formats (OpenSpec, spec-kit, GitHub/GitLab issues with checklists, generic task-lists, free-form markdown) into ralphex-format plans in docs/plans/. Triggers on "ralphex-adopt", "adopt plan", "convert plan to ralphex", "import plan as ralphex".
allowed-tools: [Bash, Read, Write, Glob, Grep, AskUserQuestion]
---
```

Step flow inside SKILL.md (Step 0 through Step 6):

- **Step 0**: Verify ralphex CLI installed (`which ralphex`); inform but do not block.
- **Step 1**: Resolve source from arg by shape: URL → fetch via gh/glab; `#N` → current repo via gh/glab; `owner/repo#N` or full issue URL → explicit repo; existing path (file or dir) → read; directory with `proposal.md`+`tasks.md` → OpenSpec; bare name → search filesystem (agent picks tool, AskUser variants on ambiguity); no arg → AskUser for source.
- **Step 2**: Detect format among OpenSpec, spec-kit, issue, generic task-list, free-form. AskUser on ambiguity (e.g., directory with mixed signals).
- **Step 3**: Confidence guard: AskUser BEFORE drafting on any uncertainty (which headings are tasks, how to split, vague items, intent). Do NOT embed sticky markers in the draft.
- **Step 4**: Convert per format (mapping rules inline). All formats add `write tests` and `run tests` checkboxes per Task and end with a `Verify acceptance criteria` Task. The `Task` keyword in the output is always English even when source headings are localized.
- **Step 5**: Write draft to `/tmp/ralphex-adopt-<slug>.md`. Run `launch-revdiff.sh --wrap --only=<file>` directly. Empty stdout means user approved. Non-empty means address annotations, rewrite, re-run revdiff. Fallback to in-chat AskUser Accept/Revise/Reject if revdiff not installed.
- **Step 6**: Compute target filename `docs/plans/YYYYMMDD-<slug>.md`, AskUser to confirm/edit slug. If target exists, AskUser to bump suffix (-2, -3) or rename or cancel; never silent overwrite. Sanity-check the draft before writing: at least one `### Task N:` header and at least one `- [ ]` checkbox under a Task; if it fails, revise and retry. Write file, clean up temp file with a trap so cleanup runs on early exit.

Per-format mapping rules to include inline in SKILL.md:

- **OpenSpec**: `proposal.md` "## Why" + "## What Changes" → Overview/Context. `specs/**/spec.md` delta sections → Technical Details. `tasks.md` numbered list → Implementation Steps grouped into `### Task N:` sections; sub-bullets become checkboxes.
- **spec-kit**: similar shape. Spec → Overview, plan/architecture → Technical Details, tasks → Implementation Steps.
- **GitHub/GitLab issue with checklists**: title → plan title, body prose → Overview, top-level `- [ ]` items → Task items split by H3 headings if present, otherwise grouped 5-7 per Task by length.
- **Generic task-list**: agent infers heading and item-marker patterns from the source, normalizes to `### Task N: <title>` (English keyword) and `- [ ]` checkboxes, preserves checked state. If heading or item pattern is ambiguous, AskUser to confirm before drafting.
- **Free-form prose**: agent infers intent (feature/bugfix/refactor/migration), pulls Overview/Context from opening prose, decomposes into 3-7 Task groups by logical phase.

Edge cases enumerated in SKILL.md:

- Source resolution: missing path, ambiguous bare name, URL fetch failure, directory with no recognizable structure.
- Format detection conflicts (multiple signals).
- Content edge cases: zero task-like content (AskUser to infer or cancel), mixed localization, huge sources >1000 lines (warn before processing), tiny <10 lines (warn that result may be sparse).
- Output collision (never silent overwrite).
- Tool fallbacks: revdiff missing → in-chat AskUser; gh/glab missing → AskUser to paste body.
- Idempotency: re-running on same source produces a fresh dated file (today's date), old converted plans in `docs/plans/completed/` are not touched.

## What Goes Where

- Implementation Steps (`[ ]` checkboxes): write the SKILL.md content, create the symlink, add the llms.txt install block, update CLAUDE.md, bump plugin version files. All achievable inside this repo.
- Post-Completion (no checkboxes): manual smoke test of the skill in a fresh Claude session against a sample source plan, confirming the converted plan parses correctly with ralphex.

## Implementation Steps

### Task 1: Write the ralphex-adopt SKILL.md content

**Files:**
- Create: `assets/claude/skills/ralphex-adopt/SKILL.md`

- [x] create directory `assets/claude/skills/ralphex-adopt/`
- [x] write `SKILL.md` with YAML frontmatter (description with triggers and brief scope, allowed-tools listing Bash, Read, Write, Glob, Grep, AskUserQuestion)
- [x] write Step 0 (verify ralphex CLI) following the same shape as existing `ralphex-plan` and `ralphex-update` skills
- [x] write Step 1 (resolve source from arg shape) covering all six input forms with heuristic order and AskUser fallbacks
- [x] write Step 2 (detect format) with detection signals for OpenSpec, spec-kit, issue, generic task-list, free-form fallback
- [x] write Step 3 (confidence guard) with explicit instruction to AskUser before drafting on ambiguity, not to embed placeholder markers
- [x] write Step 4 (convert per format) with all five mapping rules inline (OpenSpec, spec-kit, issue, generic task-list, free-form), stating ralphex's requirement that the `Task` and `Iteration` keywords stay English in plan headers (e.g., `### Task 1: <title>`) regardless of what natural language the source uses for titles or content. Do NOT cite ralphex source file paths in the user-facing skill text — this is a behavioral rule for the agent, not an implementation detail.
- [x] write Step 5 (revdiff review loop) calling `~/.claude/plugins/marketplaces/revdiff/.claude-plugin/skills/revdiff/scripts/launch-revdiff.sh --wrap --only=<draft>` directly with the draft-review.sh bypass note, and the in-chat AskUser fallback
- [x] write Step 6 (write target file) with collision guard (AskUser to bump or rename or cancel, never silent overwrite), sanity check (at least one `### Task N:` and one `- [ ]` under a Task), and temp cleanup with trap
- [x] write Edge Cases section enumerating the failures listed in Technical Details above
- [x] write Tool Fallbacks section (revdiff missing, gh missing, glab missing)
- [x] verify the SKILL.md content is fully language-agnostic: no references to Go, no Go-specific commands or paths (e.g., `go test`, `go.mod`, `pkg/...`), no language-specific assumptions about the target project. Test/run-test checkboxes the skill instructs the agent to emit must be phrased generically (e.g., "write tests for new functionality", "run project tests"), matching the existing `ralphex-plan` skill convention.
- [x] re-read the file end-to-end to confirm completeness against the brainstorm design
- [x] run sanity check on the SKILL.md content: valid markdown, frontmatter parses, every example `### Task N:` snippet inside the SKILL.md (used to teach the agent the output format) is well-formed (`### Task <N>: <title>` with English keyword)

### Task 2: Wire skill into project (symlink, llms.txt, CLAUDE.md)

**Files:**
- Create: `assets/claude/ralphex-adopt.md` (symlink)
- Modify: `llms.txt`
- Modify: `CLAUDE.md`

- [x] create symlink `assets/claude/ralphex-adopt.md` pointing to `./skills/ralphex-adopt/SKILL.md` using `ln -s` (matches the existing three ralphex skills)
- [x] verify symlink resolves and shows the SKILL.md content via `cat assets/claude/ralphex-adopt.md`
- [x] add an install block to `llms.txt` after the `/ralphex-update Skill` section, mirroring its structure: section header `### /ralphex-adopt Skill`, brief description of what the skill does, and the two install steps (fetch from `https://ralphex.com/assets/claude/ralphex-adopt.md`, write to `~/.claude/commands/ralphex-adopt.md`)
- [x] update the line in `llms.txt` that lists skill names ("With skills: `/ralphex-plan` creates plans, `/ralphex` launches execution...") to mention `/ralphex-adopt` for plan conversion
- [x] check `CLAUDE.md` for the existing skill-mention pattern (`/ralphex-update` is mentioned at line near "Use `/ralphex-update` skill for smart merging..."); if a similar one-line mention fits, add `/ralphex-adopt` for plan conversion in the same vicinity
- [x] verify all three files render correctly (no broken markdown, no malformed YAML)

### Task 3: Bump plugin version

**Files:**
- Modify: `.claude-plugin/plugin.json`
- Modify: `.claude-plugin/marketplace.json`

- [x] bump `.claude-plugin/plugin.json` `version` from `0.18.0` to `0.19.0` (minor: new skill addition, additive change)
- [x] bump `.claude-plugin/marketplace.json` matching plugin entry from `0.18.0` to `0.19.0`
- [x] verify both files have matching version values via `grep -n version .claude-plugin/plugin.json .claude-plugin/marketplace.json`
- [x] verify both files are still valid JSON via `jq . .claude-plugin/plugin.json` and `jq . .claude-plugin/marketplace.json`

### Task 4: Verify acceptance criteria

- [x] verify `assets/claude/skills/ralphex-adopt/SKILL.md` exists, has valid YAML frontmatter, and covers all six step-flow steps end-to-end
- [x] verify the symlink at `assets/claude/ralphex-adopt.md` resolves to the SKILL.md (use `readlink` and `cat`)
- [x] verify `llms.txt` contains the new `/ralphex-adopt Skill` install block and updated skill-list line
- [x] verify `CLAUDE.md` mentions `/ralphex-adopt` if a matching pattern was found in Task 2
- [x] verify both `.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json` have version `0.19.0` and parse as valid JSON
- [x] verify the skill design honors all original requirements: source untouched, output to new dated file, never silent overwrite, AskUser before drafting on uncertainty (not embedded markers), revdiff via launch-revdiff.sh (not draft-review.sh), all five input formats covered, English `Task` keyword normalization referenced

## Post-Completion

*Items requiring manual intervention - no checkboxes, informational only*

**Manual verification**:

- Trigger the skill in a fresh Claude Code session by typing `/ralphex-adopt <some-source>` and confirm the description in the frontmatter activates the skill.
- Run a dry conversion against a sample free-form markdown plan and confirm:
  - Source file is never modified.
  - Output lands at `docs/plans/YYYYMMDD-<slug>.md` with today's date.
  - revdiff opens for review on the temp draft.
  - The resulting plan parses successfully when ralphex is run against it (`### Task N:` headers detected, no checkboxes outside Task sections).
- Run a dry conversion against an OpenSpec-shaped directory if one is available, and confirm `proposal.md` content lands in Overview/Context and `tasks.md` items become Task sections.
- Run a dry conversion against a GitHub issue (`/ralphex-adopt #312` or similar) and confirm `gh issue view` is invoked for fetching.

**External system updates**: none. The plugin version bump means the next plugin marketplace pull will include the new skill automatically.
