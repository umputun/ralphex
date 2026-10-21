# Internal Scripts

Development and build utility scripts. Not intended for end users.

- **init-docker.sh** - Docker container init script. Copies Claude and Codex credentials from mounted volumes into the app user's home directory. Run automatically by the base image on container start.
- **prep-toy-test.sh** - Creates a toy Go project at `/tmp/ralphex-test` with buggy code and a plan file for end-to-end testing of ralphex's full execution mode.
- **prep-review-test.sh** - Creates a toy Go project at `/tmp/ralphex-review-test` with subtle code issues on a feature branch for testing review-only mode.
- **check-plugin-version.py** - Verifies all four manifests agree and, with `--base`, requires a bump when distributed Claude or Codex skill payload differs from that git base.
- **check-plugin-version_test.py** - Covers local consistency, base-aware payload changes, version bumps, and untracked skills.
- **update-plugin-version.sh** - Explicit maintainer helper for the four independently versioned plugin manifests. Pass `--check` to compare them with one expected plugin version.
- **update-plugin-version_test.sh** - Proves the jq and sed-fallback update paths and independently detects corruption in each manifest.

When distributed skill files change, run `make bump-plugin-version VERSION=<plugin-version>` and commit the four manifest changes. The plugin version is independent of the Ralphex CLI version; `make test-plugin` verifies consistency without mutating the checkout.
