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
	if err := waitForServer(30 * time.Second); err != nil {
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
	err = page.Locator("header h1").WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(10000),
	})
	require.NoError(t, err, "wait for header")
}

// waitVisible waits for a selector to become visible.
func waitVisible(t *testing.T, page playwright.Page, selector string, timeout ...float64) {
	t.Helper()

	timeoutMs := float64(15000) // default 15s for CI
	if len(timeout) > 0 {
		timeoutMs = timeout[0]
	}

	err := page.Locator(selector).WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(timeoutMs),
	})
	require.NoError(t, err, "wait for %s to be visible", selector)
}

// waitHidden waits for a selector to become hidden.
func waitHidden(t *testing.T, page playwright.Page, selector string, timeout ...float64) {
	t.Helper()

	timeoutMs := float64(15000)
	if len(timeout) > 0 {
		timeoutMs = timeout[0]
	}

	err := page.Locator(selector).WaitFor(playwright.LocatorWaitForOptions{
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

// TestDashboardSmoke verifies the server is running and page loads.
func TestDashboardSmoke(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// verify page title
	title, err := page.Title()
	require.NoError(t, err)
	require.Contains(t, title, "Ralphex")
}
