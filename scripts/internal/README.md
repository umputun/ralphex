# Internal Scripts

Development and build utility scripts. Not intended for end users.

- **init-docker.sh** - Docker container init script. Copies Claude and Codex credentials from mounted volumes into the app user's home directory. Run automatically by the base image on container start.
- **prep-toy-test.sh** - Creates a toy Go project at `/tmp/ralphex-test` with buggy code and a plan file for end-to-end testing of ralphex's full execution mode.
- **prep-review-test.sh** - Creates a toy Go project at `/tmp/ralphex-review-test` with subtle code issues on a feature branch for testing review-only mode.
- **update-plugin-version.sh** - Updates four version fields from one release tag: both Claude manifests plus the Codex and portable plugin manifests. Called by goreleaser as a pre-release hook.
- **update-plugin-version_test.sh** - Uses `python3` and temporary fixtures to prove the jq and sed-fallback version-update paths preserve unrelated metadata.
