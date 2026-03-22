# Per-File Fallback for Agent Loading

## Overview
- Change agent loading from "replace entire set" to per-file fallback (local → global → embedded)
- Fixes issue #238: users must copy ALL agent files to override just one
- Aligns agent loading with how prompts already work (per-file fallback)
- **Done when**: single agent override in local dir results in that agent + all other defaults; empty local dir is transparent; existing content-level fallback preserved

## Context
- `pkg/config/agents.go` — current "replace entirely" strategy
- `pkg/config/prompts.go` — reference implementation with per-file fallback (see `loadPromptWithLocalFallback`)
- `pkg/config/defaults/agents/` — 5 embedded agents (documentation, implementation, quality, simplification, testing)
- `pkg/processor/prompts.go:139-171` — `expandAgentReferences()` resolves `{{agent:name}}` from loaded agents map
- Default prompts hardcode `{{agent:name}}` references; missing agents produce WARN and stay unexpanded
- The "cohesive set" rationale in agents.go:25-30 is overstated — agents are independent named building blocks, prompts define the review strategy

## Solution Overview
- Collect union of all agent filenames from: (1) embedded `defaults/agents/` as baseline, (2) global dir, (3) local dir
- For each unique filename: try local → try global → try embedded (first non-empty wins)
- Follow `promptLoader.loadPromptWithLocalFallback()` pattern from prompts.go:92-106 (NOT `agentLoader.loadFileWithFallback` which handles content-level fallback within a single file)
- Remove `dirHasAgentFiles()` gating logic that triggers "replace entirely"
- Remove `loadFromDir()` — replaced by the new union-based approach
- Existing content-level fallback in `loadFileWithFallback` (empty/all-commented file → embedded) stays as-is and is used within the new per-file method
- Users who want fewer agents should edit the prompt to remove `{{agent:name}}` references

## Development Approach
- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**

## Implementation Steps

### Task 1: Rewrite agentLoader for per-file fallback

**Files:**
- Modify: `pkg/config/agents.go`
- Modify: `pkg/config/agents_test.go`
- Modify: `pkg/config/config_test.go`

- [x] rewrite `Load()` to collect union of filenames from embedded `defaults/agents/` (baseline), global dir, and local dir
- [x] for each unique filename in the union: try local path → try global path → try embedded, following `promptLoader.loadPromptWithLocalFallback()` pattern
- [x] remove `dirHasAgentFiles()` function (no longer needed)
- [x] remove `loadFromDir()` function (replaced by union-based approach)
- [x] add helper method to collect filenames from an on-disk directory (returns set of filenames, ignores non-.txt and dirs)
- [x] update code comments to reflect new per-file fallback strategy (replace "replace entire set" rationale)
- [x] update all existing tests that assert exact agent counts from single directories to account for embedded defaults in union:
  - `TestAgentLoader_Load_FromAgentsDir` — agents from dir + embedded defaults
  - `TestAgentLoader_Load_OnlyTxtFiles` — non-txt files skipped, embedded defaults still appear
  - `TestAgentLoader_Load_SkipsEmptyFiles` — empty files skipped, embedded defaults appear
  - `TestAgentLoader_Load_TrimsWhitespace` — verify trim behavior preserved
  - `TestAgentLoader_Load_SkipsDirectories` — subdirs skipped, embedded defaults appear
  - `TestAgentLoader_Load_PreservesMultilinePrompt` — verify content preserved
  - `TestAgentLoader_Load_PreservesAllContent` — verify content preserved
  - `TestAgentLoader_Load_HandlesCRLFLineEndings` — verify CRLF handling preserved
  - `TestAgentLoader_Load_LocalAgentsMultipleFiles` — multiple local files + embedded defaults
  - `TestAgentLoader_Load_EmptyAgentsDir` — empty dir should now return embedded defaults (behavior change)
  - `TestAgentLoader_Load_LocalAgentsEmptyFallsBackToGlobal` — empty local, global agents + embedded defaults
  - `TestAgentLoader_Load_NoLocalAgentsDirFallsBackToGlobal` — no local dir, global + embedded
  - `TestAgentLoader_loadFromDir` tests — remove or refactor (function removed)
  - remove `TestAgentLoader_dirHasAgentFiles` tests (function removed)
- [x] rename `TestAgentLoader_Load_LocalAgentsReplaceGlobal` → verify per-file merge (local overrides specific, global/embedded fill rest)
- [x] add test: local overrides one default agent, other 4 come from embedded
- [x] add test: local adds custom agent alongside all 5 embedded defaults
- [x] add test: local overrides global for same-named agent (precedence: local > global > embedded)
- [x] update `TestLocalConfig_LocalAgentsReplaceGlobal` in config_test.go to verify per-file merge
- [x] update `TestLoad_PartialOverridesAllComponents` in config_test.go — agents should be merged, not replaced
- [x] run `go test ./pkg/config/` — must pass before next task

### Task 2: Verify and finalize

- [x] run full test suite: `go test ./...`
- [x] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`
- [x] verify no `dirHasAgentFiles` or `loadFromDir` references remain in codebase
- [x] verify test coverage for pkg/config meets 80%+

### Task 3: Update documentation

- [x] update CLAUDE.md: change "Agents: replace entirely (if local agents/ has files, use ONLY local agents)" to describe per-file fallback behavior matching prompts
- [x] update README.md if agent customization is documented there
- [x] update `llms.txt` if agent customization is mentioned (likely no-op)
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- test with actual `.ralphex/agents/` directory containing partial agent set
- run toy project e2e test to verify agent expansion works correctly

**Issue update:**
- comment on issue #238 with fix details
- close issue #238
