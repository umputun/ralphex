---
worth: yes
where: docs/custom-providers.md
added: 2026-10-20
---
# custom providers doc uses signal markers that do not exist

`docs/custom-providers.md` gives `<<<RALPHEX:COMPLETED>>>` and `<<<RALPHEX:FAILED>>>` as the signals a
wrapper's model must pass through, in the stream-format section and again in the requirements list.
Neither exists: the real markers are in `pkg/status/status.go` (`ALL_TASKS_DONE`, `TASK_FAILED`,
`REVIEW_DONE`, `CODEX_REVIEW_DONE`, ...). Anyone testing a wrapper by grepping its output for the
documented markers finds nothing, even though the wrapper works. Surfaced as a pre-existing finding in the
revmux review of the issue #466 prompt fix.
