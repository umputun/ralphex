# Plan: Test — Checkboxes outside Task section (Success criteria)

## Goal

Verify ralphex exits when Task sections are done, even if Success criteria has [ ].
Previously this caused infinite loop.

## Validation Commands

- `go test ./pkg/plan/... ./pkg/processor/...`

## Success criteria

All tasks done.

---

### Task 1: Trivial — add a comment

- [ ] Add `// checkbox-test-outside` to any .go file in pkg/plan/

---

## Success criteria (checkboxes here — outside Task, should NOT block exit)

- [ ] Manual: run e2e test
- [ ] Deploy to staging

^ These are in ## Success criteria, not in ### Task. Parser orphans them.
hasUncompletedTasks only checks Task sections. Loop should exit when Task 1 is done.
