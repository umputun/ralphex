---
worth: later
where: pkg/git/external.go:hasChangesOtherThan
added: 2026-11-22
---
# decomposed (NFD) plan names are still refused on macOS

With `core.precomposeunicode=true` (macOS default) git prints a path in composed form (NFC), while the plan
path reaches `hasChangesOtherThan` with the bytes stored on disk: `filepath.Glob` in the fzf picker,
`resolveFilesystemCase` (`os.ReadDir`) and shell completion all pass NFD through, and `toRelative` does not
normalise. `strings.EqualFold` does not equate NFC and NFD, so a plan named with an accented Latin or Hangul
character saved decomposed (Cocoa apps, Finder renames) is listed as somebody else's dirty file and branch
creation fails with "worktree has uncommitted changes". CJK names have no decomposed form, which is why #485's
tests do not hit it; the base code failed the same way.

Options: look the plan up with a path-scoped `git status --porcelain -- <plan>` (git precomposes argv on
macOS) and compare against that spelling, which fits the `status --porcelain -z` change #485 deferred; or
normalise both sides with `golang.org/x/text/unicode/norm` (already an indirect dependency, would become
direct). Reproduced by the #485 review with `cafe\xcc\x81-nfd.md`.
