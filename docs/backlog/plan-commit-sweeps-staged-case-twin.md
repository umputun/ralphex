---
worth: later
where: pkg/git/service.go:CreateBranchForPlan
added: 2026-11-22
---
# plan commit sweeps in a staged case-twin of the plan

`hasChangesOtherThan` excludes the plan with `strings.EqualFold` (deliberate, 03c0866, for macOS APFS), so on
a case-sensitive filesystem a staged `docs/plans/plan.md` is treated as the plan `docs/plans/Plan.md` and not
reported. `CreateBranchForPlan` then runs `add(planFile)` and a bare `commit`, which commits the whole index,
so the unrelated staged file lands in the `add plan: ...` commit. True for ASCII names since 03c0866; #485
(decoding quoted porcelain paths) extended it to non-ASCII names such as `Résumé.md` / `résumé.md`.

Fix: commit with `s.repo.commitFiles(msg, planFile)`, as `MovePlanToCompleted` already does (#435), plus a
case-sensitive test that stages the twin. Keep the case fold; an exact-path exclusion would undo 03c0866.
Surfaced by codex in the #485 review.
