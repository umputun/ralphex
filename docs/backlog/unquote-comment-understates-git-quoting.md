---
worth: yes
where: pkg/git/external.go:extractPathFromPorcelain
added: 2026-11-22
---
# unquote comment says only non-ASCII paths are quoted

The comment on the unquote step (#485) ties git's C-quoting to non-ASCII and control bytes under the default
`core.quotePath`. git 2.56 also quotes any path containing a space, `"` or `\`, and does so even with
`-c core.quotePath=false` (`?? "my plan.md"`). A reader trusting the comment could "simplify" to
`-c core.quotePath=false` and drop the unquote, which would bring back the refusal for plan names with spaces;
the `unquotes renamed path with spaces` test would catch it, so the cost is a wasted edit, not a silent break.

Fix: reword, e.g. "git C-quotes a path with spaces, quotes, backslashes or control bytes, and with the default
core.quotePath also non-ASCII bytes; unquote it so it compares equal to the caller's plain path." In the same
edit, the regression comment in `TestExternalBackend_HasChangesOtherThan/excludes plan file with non-ASCII
name` is two lines and temporal ("used to"); make it one line naming the defect, or drop it.
