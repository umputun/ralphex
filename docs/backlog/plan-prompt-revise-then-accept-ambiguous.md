---
worth: later
where: pkg/config/defaults/prompts/make_plan.txt
added: 2026-11-26
---
# plan prompt matches both revise and accept after revise then accept

`make_plan.txt` picks the draft decision with independent "contains" checks: accept when the progress file
contains `DRAFT REVIEW: accept`, revise when it contains `DRAFT REVIEW: revise` and `FEEDBACK:`.
`Logger.LogDraftReview` appends every decision, so after a revise round followed by accept the file holds
both, and both branches match. The model can then present another draft after the user clicked Accept.

No report of it happening; recovery is accepting again. Fix is one clause in the template: the last
`DRAFT REVIEW` line is the current decision. Surfaced reviewing PR #488.
