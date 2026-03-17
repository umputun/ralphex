# Plan: Checkbox Examples — Correct vs Incorrect

## Goal

Demonstrate correct and incorrect checkbox placement for ralphex plans.

## Overview

Ralphex uses checkboxes to track task completion. Only checkboxes in Task sections count as work.

## Success criteria

All tasks completed, tests pass.

---

### Task 1: Correct — actionable checkboxes

- [ ] Create auth/hash.go with HashPassword function
- [ ] Add VerifyPassword function
- [ ] Write unit tests for hash package

---

### Task 2: Correct — simple task

- [ ] Create POST /api/login handler
- [ ] Add tests

---

### Task 3: Actionable + description checkbox

- [ ] Faulti format
- [ ] use this format for [ ] unchecked items

^ First is actionable (fix typo). Second is format description — when checking completion, ralphex ignores it (text contains `[ ]`).

---

### Task 4: Correct — [ ] in prose is not a checkbox

Description: Use - [ ] for unchecked and - [x] for done.

- [ ] Implement the parser

^ Line "Use - [ ] for unchecked" does NOT start with `- ` — parser ignores it. Only "Implement the parser" is a checkbox. Good.

---

### Task 5: Indented sub-items

- [ ] Add comprehensive tests
  - [ ] Unit tests for handler
  - [ ] Integration tests

^ Parser supports leading whitespace; all three checkboxes are parsed.

---

## Success criteria (wrong place)

- [ ] Manual: run e2e test
- [ ] Deploy to staging

^ These are NOT in a Task section. Parser closes task on ## so they're orphaned (not in any Task.Checkboxes). hasUncompletedTasks only checks Tasks — so these are ignored. Loop exits when Task checkboxes are done. Fallback FileHasUncompletedCheckbox (plans with zero Task headers) also ignores format-description checkboxes.

---

## Summary

| Case | Example | Parsed into Task? | Causes loop? |
|------|---------|------------------|--------------|
| Correct | `- [ ] Create HashPassword` | yes | no |
| Format in text | `- [ ] use [ ] for unchecked` | yes | no (ignored as description) |
| Prose | `Use - [ ] for unchecked` | no (not a checkbox line) | no |
| Success criteria | `- [ ] Manual: run e2e` | no (orphaned) | no* |
| Indented | `  - [ ] Unit tests` | yes | no |

*When plan has Task sections, Success criteria checkboxes are orphaned — hasUncompletedTasks ignores them. Loop exits. Fallback FileHasUncompletedCheckbox (plans with zero Task headers) also ignores format-description checkboxes.
