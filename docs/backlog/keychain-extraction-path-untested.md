---
worth: maybe
where: scripts/ralphex-dk.sh:extract_macos_credentials
added: 2026-10-24
---
# the macOS keychain extraction path has no test

`scripts/ralphex-dk/ralphex_dk_test.py` covers `extract_macos_credentials` only where it returns None:
on non-Darwin, and when `.credentials.json` already exists. Every other test patches the function out.
The Darwin branch itself has no test: the `_security_find_credentials` call, the
`security unlock-keychain` retry, and writing the temp file. CI runs on `ubuntu-latest` only, so
nothing exercises it in practice either. A regression there (wrong service name, a changed `security`
output, a broken temp-file write) reaches macOS Docker users first, and the fail-fast error they get
blames a missing login.

Fix: unit tests that patch `platform.system` and `subprocess.run` to cover found, locked-then-found,
not-found and OSError, and assert the temp file's content and mode. No macOS runner needed. Surfaced
reviewing #411.
