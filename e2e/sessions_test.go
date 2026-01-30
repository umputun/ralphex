//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

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

	// wait for at least one session to appear
	items := page.Locator(".session-item")
	waitForMinCount(t, items, 1)

	// session list should exist and have content
	sessionList := page.Locator("#session-list")
	visible, err := sessionList.IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "session list should be visible")
}

func TestSessionListContent(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for at least one session to appear
	waitForMinCount(t, page.Locator(".session-item"), 1)

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
		// .session-info contains session rows (name, time, optional project/branch)
		info := sessionItem.Locator(".session-info")
		visible, err := info.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible, "session should have info container")

		// session time is always present as metadata
		timeEl := sessionItem.Locator(".session-time")
		timeVisible, err := timeEl.IsVisible()
		require.NoError(t, err)
		assert.True(t, timeVisible, "session should have time metadata")
	})
}

func TestSessionSelection(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for at least one session to appear
	sessionItems := page.Locator(".session-item")
	waitForMinCount(t, sessionItems, 1)

	count, err := sessionItems.Count()
	require.NoError(t, err)

	if count < 1 {
		t.Skip("not enough sessions to test selection")
	}

	// first session should be selected by default
	firstSession := sessionItems.First()
	waitForClass(t, firstSession, "selected")
}

func TestSessionStateIndicator(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for at least one session to appear
	waitForMinCount(t, page.Locator(".session-item"), 1)

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
	hasValidState := hasClass(class, "active") || hasClass(class, "completed")
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

	body := page.Locator("body")

	// click toggle button
	toggle := page.Locator("#sidebar-toggle")
	err = toggle.Click()
	require.NoError(t, err)

	// wait for body to have collapsed class
	waitForClass(t, body, "sidebar-collapsed")

	// click toggle again to restore
	err = toggle.Click()
	require.NoError(t, err)

	// wait for collapsed class to be removed
	waitForClassGone(t, body, "sidebar-collapsed")
}

func TestSidebarKeyboardShortcut(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	body := page.Locator("body")

	// press 's' to toggle sidebar
	err := page.Keyboard().Press("s")
	require.NoError(t, err)

	// wait for body to have collapsed class
	waitForClass(t, body, "sidebar-collapsed")

	// press 's' again to restore
	err = page.Keyboard().Press("s")
	require.NoError(t, err)

	// wait for collapsed class to be removed
	waitForClassGone(t, body, "sidebar-collapsed")
}

func TestViewToggleButton(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// view toggle button should exist
	viewToggle := page.Locator("#view-toggle")
	visible, err := viewToggle.IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "view toggle button should be visible")

	// read state before clicking
	initialClass, err := viewToggle.GetAttribute("class")
	require.NoError(t, err)
	wasGrouped := hasClass(initialClass, "grouped")

	// click to toggle view mode
	err = viewToggle.Click()
	require.NoError(t, err)

	// wait for state change after click
	if wasGrouped {
		waitForClassGone(t, viewToggle, "grouped")
	} else {
		waitForClass(t, viewToggle, "grouped")
	}
}

func TestSessionDiscoveryOnNewFile(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial sessions to load
	waitForMinCount(t, page.Locator(".session-item"), 1)

	// count initial sessions
	initialItems := page.Locator(".session-item")
	initialCount, err := initialItems.Count()
	require.NoError(t, err)

	// create a new session
	newSessionName := "e2e-discovery-test"
	createTestSession(t, newSessionName)

	// wait for session polling to discover the new session
	newItems := page.Locator(".session-item")
	waitForCount(t, newItems, initialCount+1)
}

func TestSessionSwitchingUpdatesHeader(t *testing.T) {
	// create a second session
	secondSessionName := "second-session"
	createTestSession(t, secondSessionName)

	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for at least 2 sessions to appear
	sessionItems := page.Locator(".session-item")
	waitForMinCount(t, sessionItems, 2)

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
		if class == "" || !hasClass(class, "selected") {
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

	// wait for the clicked session to become selected
	require.Eventually(t, func() bool {
		for i := 0; i < count; i++ {
			session := sessionItems.Nth(i)
			class, _ := session.GetAttribute("class")
			if hasClass(class, "selected") {
				// check if plan name has been read (session switch happened)
				newPlan, _ := planName.TextContent()
				return newPlan != "" && newPlan != initialPlan
			}
		}
		return false
	}, longPollTimeout, pollInterval, "session switch should update header")

	// check if plan name changed (it may or may not, depending on the session)
	newPlan, err := planName.TextContent()
	require.NoError(t, err)

	// Log what we found - the test passes if we got this far without errors
	t.Logf("Initial plan: %s, New plan: %s", initialPlan, newPlan)
}
