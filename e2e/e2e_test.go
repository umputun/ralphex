//go:build e2e

// Package e2e provides end-to-end tests for the ralphex web dashboard.
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
)

const (
	testPort    = 18080
	baseURL     = "http://127.0.0.1:18080"
	binaryPath  = "/tmp/ralphex-e2e"
	testDataDir = "testdata"

	// polling intervals for condition-based waits (replaces time.Sleep).
	pollTimeout      = 5 * time.Second
	pollInterval     = 100 * time.Millisecond
	longPollTimeout  = 15 * time.Second
	longPollInterval = 500 * time.Millisecond

	// server startup timeout
	serverStartTimeout = 30 * time.Second

	// negative-assertion waits: verify something does NOT change over a time window.
	// these are intentional sleeps — there is no condition to poll for "no change".
	noChangeWait      = 1500 * time.Millisecond
	noChangeWaitShort = 500 * time.Millisecond
)

var (
	pw         *playwright.Playwright
	browser    playwright.Browser
	serverCmd  *exec.Cmd
	testTmpDir string
)

func TestMain(m *testing.M) {
	code := 1
	defer func() {
		os.Exit(code)
	}()

	// build the binary
	if err := buildBinary(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build binary: %v\n", err)
		return
	}
	defer os.Remove(binaryPath)

	// create temp directory for test data
	var err error
	testTmpDir, err = os.MkdirTemp("", "ralphex-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		return
	}
	defer os.RemoveAll(testTmpDir)

	// copy test data to temp directory
	if err := copyTestData(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to copy test data: %v\n", err)
		return
	}

	// start the server
	if err := startServer(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start server: %v\n", err)
		return
	}
	defer stopServer()

	// wait for server to be ready
	if err := waitForServer(serverStartTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "server not ready: %v\n", err)
		return
	}

	// setup playwright
	if err := setupPlaywright(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup playwright: %v\n", err)
		return
	}
	defer teardownPlaywright()

	code = m.Run()
}

func buildBinary() error {
	// get the project root (parent of e2e directory)
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get cwd: %w", err)
	}
	projectRoot := filepath.Dir(cwd)

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/ralphex")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	return nil
}

func copyTestData() error {
	testDataPath, err := resolveTestDataDir()
	if err != nil {
		return fmt.Errorf("resolve test data dir: %w", err)
	}

	// copy progress file
	progressSrc := filepath.Join(testDataPath, "progress-test.txt")
	progressDst := filepath.Join(testTmpDir, "progress-test.txt")
	if err := copyFile(progressSrc, progressDst); err != nil {
		return fmt.Errorf("copy progress file: %w", err)
	}

	// copy plan file
	planSrc := filepath.Join(testDataPath, "test-plan.md")
	planDst := filepath.Join(testTmpDir, "test-plan.md")
	if err := copyFile(planSrc, planDst); err != nil {
		return fmt.Errorf("copy plan file: %w", err)
	}

	// copy malformed plan file for edge case tests
	malformedSrc := filepath.Join(testDataPath, "test-plan-malformed.md")
	malformedDst := filepath.Join(testTmpDir, "test-plan-malformed.md")
	if err := copyFile(malformedSrc, malformedDst); err != nil {
		return fmt.Errorf("copy malformed plan file: %w", err)
	}

	// copy full events progress file for iteration/boundary tests
	fullEventsSrc := filepath.Join(testDataPath, "progress-full-events.txt")
	fullEventsDst := filepath.Join(testTmpDir, "progress-full-events.txt")
	if err := copyFile(fullEventsSrc, fullEventsDst); err != nil {
		return fmt.Errorf("copy full events progress file: %w", err)
	}

	return nil
}

func resolveTestDataDir() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate test file")
	}

	return filepath.Join(filepath.Dir(filename), testDataDir), nil
}

func copyFile(src, dst string) error {
	content, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// atomicWriteFile writes content to a file atomically using a temp file and rename.
// this prevents fsnotify from seeing partial writes when the server is watching the directory.
func atomicWriteFile(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	// ensure cleanup on error
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// atomic rename
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp to target: %w", err)
	}
	tmpPath = "" // prevent deferred cleanup
	return nil
}

func startServer() error {
	serverCmd = exec.Command(binaryPath,
		"--serve",
		"--port", fmt.Sprintf("%d", testPort),
		"--watch", testTmpDir,
	)
	serverCmd.Stdout = os.Stdout
	serverCmd.Stderr = os.Stderr

	if err := serverCmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	return nil
}

func stopServer() {
	if serverCmd != nil && serverCmd.Process != nil {
		_ = serverCmd.Process.Kill()
		_ = serverCmd.Wait()
	}
}

func waitForServer(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for server after %v", timeout)
		case <-ticker.C:
			resp, err := client.Get(baseURL + "/")
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

func setupPlaywright() error {
	// install playwright browsers
	if err := playwright.Install(); err != nil {
		return fmt.Errorf("install playwright: %w", err)
	}

	var err error
	pw, err = playwright.Run()
	if err != nil {
		return fmt.Errorf("run playwright: %w", err)
	}

	// check for headless mode (default: headless)
	headless := os.Getenv("E2E_HEADLESS") != "false"

	opts := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
	}

	// add slow motion when not headless (for visual observation)
	if !headless {
		opts.SlowMo = playwright.Float(50)
	}

	browser, err = pw.Chromium.Launch(opts)
	if err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}

	return nil
}

func teardownPlaywright() {
	if browser != nil {
		_ = browser.Close()
	}
	if pw != nil {
		_ = pw.Stop()
	}
}

// newPage creates an isolated browser context and page for a test.
// each test gets its own context to ensure isolation (separate cookies, storage).
func newPage(t *testing.T) playwright.Page {
	t.Helper()

	ctx, err := browser.NewContext()
	require.NoError(t, err, "create browser context")

	page, err := ctx.NewPage()
	require.NoError(t, err, "create page")

	t.Cleanup(func() {
		_ = page.Close()
		_ = ctx.Close()
	})

	return page
}

// navigateToDashboard loads the dashboard and waits for it to be ready.
func navigateToDashboard(t *testing.T, page playwright.Page) {
	t.Helper()

	_, err := page.Goto(baseURL)
	require.NoError(t, err, "navigate to dashboard")

	// wait for the main header h1 to be visible (indicates page loaded)
	err = page.Locator("header h1").First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(float64(longPollTimeout / time.Millisecond)),
	})
	require.NoError(t, err, "wait for header")
}

// waitVisible waits for a selector to become visible.
func waitVisible(t *testing.T, page playwright.Page, selector string, timeout ...float64) {
	t.Helper()

	timeoutMs := float64(longPollTimeout / time.Millisecond)
	if len(timeout) > 0 {
		timeoutMs = timeout[0]
	}

	err := page.Locator(selector).First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(timeoutMs),
	})
	require.NoError(t, err, "wait for %s to be visible", selector)
}

// waitHidden waits for a selector to become hidden.
func waitHidden(t *testing.T, page playwright.Page, selector string, timeout ...float64) {
	t.Helper()

	timeoutMs := float64(longPollTimeout / time.Millisecond)
	if len(timeout) > 0 {
		timeoutMs = timeout[0]
	}

	err := page.Locator(selector).First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateHidden,
		Timeout: playwright.Float(timeoutMs),
	})
	require.NoError(t, err, "wait for %s to be hidden", selector)
}

// isDetailsOpen checks if a details element is open using JavaScript evaluation.
// returns false if evaluation fails or element is not a details element.
func isDetailsOpen(locator playwright.Locator) bool {
	result, err := locator.Evaluate("el => el.open", nil)
	if err != nil {
		return false
	}
	open, ok := result.(bool)
	if !ok {
		return false
	}
	return open
}

// hasClass checks if classAttr contains the exact CSS class token.
// uses strings.Fields for exact token matching to avoid false positives
// (e.g. "sidebar-collapsed" matching "collapsed").
func hasClass(classAttr, class string) bool {
	for _, c := range strings.Fields(classAttr) {
		if c == class {
			return true
		}
	}
	return false
}

// waitForClass polls until the locator's class attribute contains the exact token.
func waitForClass(t *testing.T, loc playwright.Locator, class string) {
	t.Helper()
	require.Eventually(t, func() bool {
		c, err := loc.GetAttribute("class")
		return err == nil && hasClass(c, class)
	}, pollTimeout, pollInterval, "element should have class %q", class)
}

// waitForClassGone polls until the locator's class attribute no longer contains the exact token.
func waitForClassGone(t *testing.T, loc playwright.Locator, class string) {
	t.Helper()
	require.Eventually(t, func() bool {
		c, err := loc.GetAttribute("class")
		return err == nil && !hasClass(c, class)
	}, pollTimeout, pollInterval, "element should not have class %q", class)
}

// waitAllDetailsState polls until all sections have the specified open state.
func waitAllDetailsState(t *testing.T, sections playwright.Locator, count int, open bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		for i := 0; i < count; i++ {
			if isDetailsOpen(sections.Nth(i)) != open {
				return false
			}
		}
		return true
	}, pollTimeout, pollInterval, "all sections should have open=%v", open)
}

// waitInputValue polls until the locator's input value matches expected.
func waitInputValue(t *testing.T, loc playwright.Locator, expected string) {
	t.Helper()
	require.Eventually(t, func() bool {
		v, err := loc.InputValue()
		return err == nil && v == expected
	}, pollTimeout, pollInterval, "input should have value %q", expected)
}

// waitFocused polls until the locator's element is the active (focused) element.
func waitFocused(t *testing.T, loc playwright.Locator) {
	t.Helper()
	var lastErr error
	require.Eventually(t, func() bool {
		focused, err := loc.Evaluate("el => document.activeElement === el", nil)
		if err != nil {
			lastErr = err
			return false
		}
		b, ok := focused.(bool)
		if !ok {
			lastErr = fmt.Errorf("expected bool, got %T", focused)
			return false
		}
		return b
	}, pollTimeout, pollInterval, "element should be focused, last error: %v", lastErr)
}

// waitDetailsToggle polls until a details element's open state differs from initialOpen.
func waitDetailsToggle(t *testing.T, section playwright.Locator, initialOpen bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		return isDetailsOpen(section) != initialOpen
	}, pollTimeout, pollInterval, "section open state should toggle")
}

// waitForCount polls until the locator count equals expected (uses long timeout for polling-based discovery).
func waitForCount(t *testing.T, loc playwright.Locator, expected int) {
	t.Helper()
	require.Eventually(t, func() bool {
		count, err := loc.Count()
		return err == nil && count == expected
	}, longPollTimeout, longPollInterval, "expected %d elements", expected)
}

// waitForMinCount polls until the locator count is at least min.
func waitForMinCount(t *testing.T, loc playwright.Locator, min int) {
	t.Helper()
	require.Eventually(t, func() bool {
		count, err := loc.Count()
		return err == nil && count >= min
	}, longPollTimeout, longPollInterval, "expected at least %d elements", min)
}

// waitForText polls until the locator's text content equals expected.
func waitForText(t *testing.T, loc playwright.Locator, expected string) {
	t.Helper()
	require.Eventually(t, func() bool {
		text, err := loc.TextContent()
		return err == nil && text == expected
	}, longPollTimeout, pollInterval, "element should have text %q", expected)
}

// waitForTextContains polls until the locator's text content contains substr.
func waitForTextContains(t *testing.T, loc playwright.Locator, substr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		text, err := loc.TextContent()
		return err == nil && strings.Contains(text, substr)
	}, longPollTimeout, pollInterval, "element text should contain %q", substr)
}

// waitForScrollIndicator polls until the scroll indicator has the expected visibility state.
func waitForScrollIndicator(t *testing.T, indicator playwright.Locator, visible bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		result, err := indicator.Evaluate("el => el.classList.contains('visible')", nil)
		if err != nil {
			return false
		}
		v, ok := result.(bool)
		return ok && v == visible
	}, pollTimeout, pollInterval, "scroll indicator visible should be %v", visible)
}

// expandAllSections clicks the expand-all button and waits for all sections to open.
func expandAllSections(t *testing.T, page playwright.Page) {
	t.Helper()
	err := page.Locator("#expand-all").Click()
	require.NoError(t, err, "click expand all")
	sections := page.Locator(".section-header")
	count, err := sections.Count()
	require.NoError(t, err, "count sections")
	if count > 0 {
		waitAllDetailsState(t, sections, count, true)
	}
}

// clickSessionByName polls the session sidebar until a session with the given name
// appears, then clicks it. Returns true if the session was found and clicked.
func clickSessionByName(t *testing.T, page playwright.Page, name string) bool {
	t.Helper()
	var clicked bool
	require.Eventually(t, func() bool {
		items := page.Locator(".session-item")
		count, err := items.Count()
		if err != nil || count == 0 {
			return false
		}
		for i := 0; i < count; i++ {
			nameEl := items.Nth(i).Locator(".session-name")
			text, err := nameEl.TextContent()
			if err != nil {
				continue
			}
			if text == name {
				if err := items.Nth(i).Click(); err != nil {
					return false
				}
				clicked = true
				return true
			}
		}
		return false
	}, longPollTimeout, longPollInterval, "session %q should appear in sidebar", name)
	return clicked
}

// assertDurationsClose asserts two duration strings (e.g. "7m 0s", "6m 59s") are within tolerance.
func assertDurationsClose(t *testing.T, s1, s2 string, tolerance time.Duration, msgAndArgs ...any) {
	t.Helper()
	d1, err1 := time.ParseDuration(strings.ReplaceAll(s1, " ", ""))
	d2, err2 := time.ParseDuration(strings.ReplaceAll(s2, " ", ""))
	require.NoError(t, err1, "failed to parse duration %q", s1)
	require.NoError(t, err2, "failed to parse duration %q", s2)
	diff := d1 - d2
	if diff < 0 {
		diff = -diff
	}
	require.LessOrEqual(t, diff, tolerance, msgAndArgs...)
}

// TestDashboardSmoke verifies the server is running and page loads.
func TestDashboardSmoke(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify page title
	title, err := page.Title()
	require.NoError(t, err)
	require.Contains(t, title, "Ralphex")
}
