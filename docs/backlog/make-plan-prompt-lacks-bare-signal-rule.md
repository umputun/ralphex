---
worth: maybe
where: pkg/config/defaults/prompts/make_plan.txt
added: 2026-10-20
---
# make_plan prompt lacks the bare-name signal rule

The fix for https://github.com/umputun/ralphex/issues/466 tells agents in `task.txt`, `review_first.txt`,
`review_second.txt`, `codex.txt` and `custom_eval.txt` to refer to a signal by its bare name and never quote
the full marker, because `detectSignal` (`pkg/executor/executor.go`) matches a marker anywhere in the
output. `make_plan.txt` was left out: no plan-mode failure has been reported. The exposure is the same for
`PLAN_READY`, so a sentence like "I will output <<<RALPHEX:PLAN_READY>>> once you accept" would end plan
creation early. `QUESTION` and `PLAN_DRAFT` are parsed with their `END` payload markers, so the wording
there needs its own thought rather than a copy of the one-line rule.
