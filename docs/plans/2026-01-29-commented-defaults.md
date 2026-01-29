# Commented Defaults Implementation Plan

## Overview

Change config file installation to copy defaults with content commented out. Files with only comments/empty lines fall back to embedded defaults and are safe to overwrite on updates. Users who customize files keep their changes, while non-customizing users automatically get updated defaults.

## Context

- Files involved: `pkg/config/defaults.go`, `pkg/config/values.go`, `pkg/config/agents.go`, `pkg/config/prompts.go`
- Existing patterns: `stripComments()` already used in prompts loading, falls back to embedded if empty
- Scope: config file, prompts, and agents

## Design

**Install phase:**
- Copy embedded defaults with content lines commented out (prefix `# `)
- Skip lines that are already comments or empty

**Load phase:**
- Strip comments from file content
- If result is empty after trimming → fall back to embedded default

**Update phase:**
- Before overwriting, check if file is all-commented/empty
- If yes → safe to overwrite with new commented template
- If no → user customized, preserve their file

**Comment function:**
```go
func commentOutContent(content string) string {
    lines := strings.Split(content, "\n")
    for i, line := range lines {
        trimmed := strings.TrimSpace(line)
        if trimmed == "" || strings.HasPrefix(trimmed, "#") {
            continue
        }
        lines[i] = "# " + line
    }
    return strings.Join(lines, "\n")
}
```

## Tasks

### 1. Add helper functions to defaults.go

**Files:**
- Modify: `pkg/config/defaults.go`
- Modify: `pkg/config/defaults_test.go`

- [ ] Add `commentOutContent(content string) string` function
- [ ] Add `shouldOverwrite(filePath string) bool` function (uses stripComments + TrimSpace)
- [ ] Add tests for `commentOutContent` (regular lines, already-commented, empty lines, mixed)
- [ ] Add tests for `shouldOverwrite` (all-commented, empty, has content, file not exists)
- [ ] Verify tests pass

### 2. Update defaults installer

**Files:**
- Modify: `pkg/config/defaults.go`
- Modify: `pkg/config/defaults_test.go`

- [ ] Modify `copyConfigFile` to use `commentOutContent` before writing
- [ ] Modify `copyConfigFile` to check `shouldOverwrite` before overwriting existing files
- [ ] Modify `copyPromptFiles` to use `commentOutContent` and `shouldOverwrite`
- [ ] Modify `copyAgentFiles` to use `commentOutContent` and `shouldOverwrite`
- [ ] Add integration tests for install with existing files
- [ ] Verify tests pass

### 3. Update config values loading

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/values_test.go`

- [ ] Update config loading to strip comments and fall back to embedded if empty
- [ ] Add tests for fallback behavior (all-commented config falls back to embedded)
- [ ] Verify tests pass

### 4. Update agents loading

**Files:**
- Modify: `pkg/config/agents.go`
- Modify: `pkg/config/agents_test.go`

- [ ] Update agent loading to strip comments and fall back to embedded if empty
- [ ] Add tests for fallback behavior per agent file
- [ ] Verify tests pass

### 5. Verify prompts loading

**Files:**
- Review: `pkg/config/prompts.go`
- Modify: `pkg/config/prompts_test.go`

- [ ] Verify existing `loadPromptWithFallback` already handles this pattern correctly
- [ ] Add explicit tests for all-commented prompt files falling back to embedded
- [ ] Verify tests pass

### 6. Final Validation

- [ ] Run full test suite: `make test`
- [ ] Run linter: `make lint`
- [ ] Test manually: delete config dir, run ralphex, verify files are commented templates
- [ ] Test manually: verify uncommented file is not overwritten on re-run
- [ ] Move plan to `docs/plans/completed/`
