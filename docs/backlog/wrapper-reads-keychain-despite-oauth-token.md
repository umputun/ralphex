---
worth: maybe
where: scripts/ralphex-dk.sh:main
added: 2026-10-24
---
# docker wrapper reads the macOS keychain even when CLAUDE_CODE_OAUTH_TOKEN is passed

The README's workaround for keychain-only Claude logins (#411) is `claude setup-token` plus
`-E CLAUDE_CODE_OAUTH_TOKEN`. Claude Code prefers that token over stored login credentials, so the
copied keychain entry goes unused. `main` still calls `extract_macos_credentials` before it merges the
`-E` flags with `merge_env_flags`, so the user can still get the keychain unlock prompt, a temp file
holding the extracted credentials, and a mount the run never reads. The macOS credential gate right
after it also refuses to start without a keychain or on-disk login, which is why the README says the
token adds to a host login and does not replace it.

Fix: check the merged `-E`/`RALPHEX_EXTRA_ENV` entries for `CLAUDE_CODE_OAUTH_TOKEN` before extraction,
and skip extraction and the macOS gate when it is present. About ten lines plus tests. Related to
`wrapper-blocks-users-without-local-claude-creds.md`, which wants a provider-neutral "no local Claude
credentials" mode; the two may share one switch. Surfaced reviewing #411.
