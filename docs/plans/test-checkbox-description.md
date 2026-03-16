# Plan: Test — Checkbox with [ ] in text (format description)

## Goal

Verify ralphex exits when actionable checkbox is done, even if description checkbox (text contains [ ]) remains unchecked.
Previously this caused infinite loop.

## Validation Commands

- `go test ./pkg/plan/... ./pkg/processor/...`

## Success criteria

All tasks done.

---

### Task 1: Actionable + description checkbox

- [ ] Add `// checkbox-test-description` to any .go file in pkg/plan/
- [ ] Add `// checkbox-test-description-2` to any .go file in pkg/plan/ # use this format for [ ] unchecked items

### Task 2: Actionable + description checkbox 2

- [ ] Add `// checkbox-test-description-3` to any .go file in pkg/plan/
- [ ] Add `// checkbox-second-description-4` to any .go file in pkg/plan/ # use this format for [ ] unchecked items
^ First is actionable. Second has [ ] in text — ignored for completion check.
Loop should exit when first is [x], even if second stays [ ].
