---
worth: maybe
where: pkg/git/service.go:186
added: 2026-08-31
---
# the worktree guard still refuses a mid-operation source checkout after its reason was removed

`preparePlanBranch` refuses to start a worktree run when the source checkout has an unfinished git
operation - merge, cherry-pick, revert, rebase, am or bisect. #445 added it, and the reason it gave, in
both the comment and the user-facing error, was that completion archives the plan in that checkout.
#450 moved the archive into the worktree, so that reason no longer holds: a rebase in progress in the
main checkout cannot affect an archive that commits on the feature branch.

The guard was left firing exactly as before and its rationale rewritten to a conservative one - a
worktree forks committed HEAD and carries none of the unfinished operation's index or staged state, so
the source checkout is an unsound base to fork from. That is defensible, but it is a reason written after
the fact to keep a guard whose original justification had gone, which is worth recording as such.

What narrowing would have to establish, since the cases differ:

- **a new feature branch** forks the source checkout's current HEAD, so a detached or mid-rebase HEAD is
  a genuinely poor base and the refusal earns its place
- **an existing feature branch** keeps its own tip and does not read the source checkout's HEAD at all,
  so the operation in progress is arguably irrelevant to it
- `CreateWorktreeForPlan` still copies an uncommitted plan out of the source checkout, so a run whose
  plan is dirty reads that tree whether it creates a new feature branch or reuses an existing one

Deferred because it is a behavior change to someone else's recent deliberate guard, not a defect: the
current behavior is over-strict rather than wrong, and the cost is a clear refusal a user can act on.
Surfaced merging #450 into master, and argued through with codex.
