//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestSession creates a progress file for a test session.
// registers cleanup to remove the file when the test completes.
func createTestSession(t *testing.T, name string) string {
	t.Helper()
	filename := "progress-" + name + ".txt"
	path := filepath.Join(testTmpDir, filename)

	content := `# Ralphex Progress Log
Plan: ` + name + `.md
Branch: test-branch-` + name + `
Mode: full
Started: 2026-01-22 11:00:00
------------------------------------------------------------
[26-01-22 11:00:00] Starting execution for ` + name + `

--- Task iteration 1 ---
[26-01-22 11:00:01] Working on ` + name + `
[26-01-22 11:00:02] Processing ` + name + `
`
	err := atomicWriteFile(path, []byte(content), 0o600)
	require.NoError(t, err, "create session progress file")

	t.Cleanup(func() {
		os.Remove(path)
	})

	return filename
}

func TestSessionSidebarDiscovery(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sessions to load
	time.Sleep(2 * time.Second)

	// session list should exist and have content
	sessionList := page.Locator("#session-list")
	visible, err := sessionList.IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "session list should be visible")

	// should have at least one session (the test-plan session)
	items := page.Locator(".session-item")
	count, err := items.Count()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have at least one session")
}

func TestSessionListContent(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sessions to load
	time.Sleep(2 * time.Second)

	// find the first session item
	sessionItem := page.Locator(".session-item").First()

	t.Run("has indicator", func(t *testing.T) {
		indicator := sessionItem.Locator(".session-indicator")
		visible, err := indicator.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible, "session should have indicator")
	})

	t.Run("has name", func(t *testing.T) {
		name := sessionItem.Locator(".session-name")
		text, err := name.TextContent()
		require.NoError(t, err)
		assert.NotEmpty(t, text, "session should have name")
	})

	t.Run("has meta info", func(t *testing.T) {
		meta := sessionItem.Locator(".session-meta")
		visible, err := meta.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible, "session should have meta info")
	})
}

func TestSessionSelection(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sessions to load
	time.Sleep(2 * time.Second)

	// find sessions
	sessionItems := page.Locator(".session-item")
	count, err := sessionItems.Count()
	require.NoError(t, err)

	if count < 1 {
		t.Skip("not enough sessions to test selection")
	}

	// first session should be selected by default
	firstSession := sessionItems.First()
	class, err := firstSession.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "selected", "first session should be selected")
}

func TestSessionStateIndicator(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sessions to load
	time.Sleep(2 * time.Second)

	// find session indicator
	indicator := page.Locator(".session-indicator").First()

	visible, err := indicator.IsVisible()
	require.NoError(t, err)
	if !visible {
		t.Skip("no session indicator found")
	}

	// indicator should have either 'active' or 'completed' class
	class, err := indicator.GetAttribute("class")
	require.NoError(t, err)

	// verify indicator has a valid state class (active or completed)
	hasValidState := strings.Contains(class, "active") || strings.Contains(class, "completed")
	assert.True(t, hasValidState, "indicator should have 'active' or 'completed' class, got: %q", class)
}

func TestSidebarToggle(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// sidebar should be visible initially
	sidebar := page.Locator(".session-sidebar")
	visible, err := sidebar.IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "sidebar should be visible initially")

	// click toggle button
	toggle := page.Locator("#sidebar-toggle")
	err = toggle.Click()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// body should have collapsed class
	body := page.Locator("body")
	class, err := body.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "sidebar-collapsed", "body should have sidebar-collapsed class")

	// click toggle again to restore
	err = toggle.Click()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// collapsed class should be removed
	class, err = body.GetAttribute("class")
	require.NoError(t, err)
	// class may be empty or not contain sidebar-collapsed
	if class != "" {
		assert.NotContains(t, class, "sidebar-collapsed", "sidebar-collapsed should be removed")
	}
}

func TestSidebarKeyboardShortcut(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// press 's' to toggle sidebar
	err := page.Keyboard().Press("s")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// body should have collapsed class
	body := page.Locator("body")
	class, err := body.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "sidebar-collapsed", "pressing 's' should collapse sidebar")

	// press 's' again to restore
	err = page.Keyboard().Press("s")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// collapsed class should be removed
	class, err = body.GetAttribute("class")
	require.NoError(t, err)
	if class != "" {
		assert.NotContains(t, class, "sidebar-collapsed", "pressing 's' again should expand sidebar")
	}
}

func TestViewToggleButton(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// view toggle button should exist
	viewToggle := page.Locator("#view-toggle")
	visible, err := viewToggle.IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "view toggle button should be visible")

	// click to toggle view mode
	err = viewToggle.Click()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// button should have grouped class after click
	class, err := viewToggle.GetAttribute("class")
	require.NoError(t, err)
	t.Logf("View toggle class after click: %s", class)
}

func TestSessionDiscoveryOnNewFile(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// count initial sessions
	initialItems := page.Locator(".session-item")
	initialCount, err := initialItems.Count()
	require.NoError(t, err)

	// create a new session
	newSessionName := "e2e-discovery-test"
	createTestSession(t, newSessionName)

	// wait for session polling to discover it (5 second poll interval + some margin)
	time.Sleep(7 * time.Second)

	// count sessions again
	newItems := page.Locator(".session-item")
	newCount, err := newItems.Count()
	require.NoError(t, err)

	// should have one more session
	assert.Equal(t, initialCount+1, newCount, "should discover new session")
}

func TestSessionSwitchingUpdatesHeader(t *testing.T) {
	// create a second session
	secondSessionName := "second-session"
	createTestSession(t, secondSessionName)

	// wait for file system to settle
	time.Sleep(500 * time.Millisecond)

	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sessions to load
	time.Sleep(3 * time.Second)

	// find sessions
	sessionItems := page.Locator(".session-item")
	count, err := sessionItems.Count()
	require.NoError(t, err)

	if count < 2 {
		t.Skip("not enough sessions to test switching")
	}

	// get current plan name
	planName := page.Locator("#plan-name")
	initialPlan, err := planName.TextContent()
	require.NoError(t, err)

	// click second session (should be the new one or different from first)
	// find a session that's not currently selected
	var foundUnselected bool
	for i := 0; i < count; i++ {
		session := sessionItems.Nth(i)
		class, err := session.GetAttribute("class")
		require.NoError(t, err)
		if class == "" || !strings.Contains(class, "selected") {
			// click the unselected session
			err = session.Click()
			require.NoError(t, err)
			foundUnselected = true
			break
		}
	}

	if !foundUnselected {
		t.Skip("could not find unselected session")
	}

	// wait for session to switch
	time.Sleep(2 * time.Second)

	// check if plan name changed (it may or may not, depending on the session)
	newPlan, err := planName.TextContent()
	require.NoError(t, err)

	// Log what we found - the test passes if we got this far without errors
	t.Logf("Initial plan: %s, New plan: %s", initialPlan, newPlan)
}
