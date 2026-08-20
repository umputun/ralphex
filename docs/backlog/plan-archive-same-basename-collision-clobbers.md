---
worth: soon
where: pkg/git/service.go:519
added: 2026-08-20
---
# archiving a plan overwrites an existing completed/ copy of the same name, and reports success

`resolvePlanMoveTargets` returns `done=false` as soon as the source exists (`pkg/git/service.go:519-521`)
without ever checking whether `completed/<same-basename>` is already there. `git mv` then refuses with
`destination exists`, the `os.Rename` fallback overwrites the archived copy, `add(destPath)` stages the
overwrite, and the commit goes through. `MovePlanToCompleted` returns nil, so the caller logs
`moved plan to ...` with `planMoved=true`.

Reproduced on `f5a4557` with a tracked `docs/plans/20260512-foo.md` and an **uncommitted**
`docs/plans/completed/20260512-foo.md` left by an earlier run of the same slug:

```
archived content after move: "# active run 2"        <- run 1's content destroyed
history for that path:       one commit, the clobber
git status:                   D docs/plans/20260512-foo.md
HEAD tree:                   docs/plans/20260512-foo.md
                             docs/plans/completed/20260512-foo.md
```

Three things go wrong at once. The previously completed plan is replaced — recoverable from history if it
had been committed, unrecoverable if the user put it there by hand and never committed it, which is the
realistic case for a plan dropped in manually. HEAD ends up carrying the plan at both paths. And the source
deletion is never staged, so the checkout is left with a dangling ` D docs/plans/<plan>.md` the user did
not make. Against the project's "completed plans are immutable" rule, and reported as a clean success.

Nothing upstream prevents it: `Selector.Select` globs `docs/plans/*.md` and does not consult `completed/`,
so a second run of the same slug on the same day reaches this path normally.

The fix shape is already in the file. The *alternate*-basename form of exactly this collision is guarded at
`pkg/git/service.go:538-543` — it probes the destination, logs, and returns `done=true` to preserve both
files for manual resolution — with `collision between in-place rename and stale completed/<altBase> is left
untouched` (`pkg/git/service_test.go:779`) pinning that contract including `assert.False(dirty)`. The direct
same-basename case needs the same guard and the same test.

Surfaced reviewing the fix for issue #435, and confirmed independently by codex. Separate from that fix:
the collision predates it and the narrowed archive commit neither causes nor worsens it.
