---
worth: yes
where: Makefile:e2e-setup
added: 2026-09-14
---
# playwright driver install uses @latest and drifts from the vendored client

`make e2e-setup` (Makefile:37), the manual e2e workflow (.github/workflows/e2e.yml:26) and the CLAUDE.md
example (CLAUDE.md:389) all install the Playwright driver with `go run .../cmd/playwright@latest`. The
vendored client pins its driver in `playwrightCliVersion` (vendor/github.com/mxschmitt/playwright-go/run.go:22),
and go.mod pins the client. Once upstream publishes a newer release, `@latest` installs a driver and
browsers for a different revision than the one the tests use. The tests still pass: `setupPlaywright`
(e2e/e2e_test.go) calls `playwright.Install()` before `playwright.Run()`, and each driver version lives in
its own cache directory (run.go, `ms-playwright-go/<version>`), so the suite downloads its matching driver
and browsers itself. The cost is a redundant download, and `--with-deps` system packages chosen for a
different Chromium revision. The Makefile additionally still uses the old module path
`github.com/playwright-community/playwright-go`, which PR #434 (f5a4557) replaced in the workflow and in
CLAUDE.md but not here; that path resolves to no release newer than v0.6000.0.

Fix in all three places:

```
go run -mod=mod github.com/mxschmitt/playwright-go/cmd/playwright install --with-deps chromium
```

Dropping `@latest` makes `go run` resolve the version from go.mod, so the driver follows the client
automatically. `-mod=mod` is required because `cmd/playwright` is not in the vendor tree and the default
vendor mode cannot resolve it. Checked on 2026-09-14 in a scratch copy of go.mod/go.sum, when the client
was v0.6201.1: resolved to v0.6201.1 and did not modify go.sum. Surfaced reviewing PR #458.
