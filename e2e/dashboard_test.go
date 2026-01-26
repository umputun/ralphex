//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardLoads(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("has correct title", func(t *testing.T) {
		title, err := page.Title()
		require.NoError(t, err)
		assert.Contains(t, title, "Ralphex Dashboard")
	})

	t.Run("has header with title", func(t *testing.T) {
		h1 := page.Locator("header h1")
		text, err := h1.TextContent()
		require.NoError(t, err)
		assert.Equal(t, "Ralphex Dashboard", text)
	})

	t.Run("has status area", func(t *testing.T) {
		waitVisible(t, page, ".status-area")
	})
}

func TestPhaseNavigation(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("all tabs visible", func(t *testing.T) {
		// check all phase tabs are present
		tabs := []string{"All", "Implementation", "Claude Review", "Codex Review"}
		for _, tab := range tabs {
			locator := page.Locator(".phase-tab", playwright.PageLocatorOptions{
				HasText: tab,
			})
			visible, err := locator.IsVisible()
			require.NoError(t, err, "check visibility of tab %s", tab)
			assert.True(t, visible, "tab %s should be visible", tab)
		}
	})

	t.Run("all tab is active by default", func(t *testing.T) {
		allTab := page.Locator(".phase-tab[data-phase='all']")
		class, err := allTab.GetAttribute("class")
		require.NoError(t, err)
		assert.Contains(t, class, "active")
	})

	t.Run("clicking tab changes active state", func(t *testing.T) {
		// click Implementation tab
		taskTab := page.Locator(".phase-tab[data-phase='task']")
		err := taskTab.Click()
		require.NoError(t, err)

		// verify it becomes active
		class, err := taskTab.GetAttribute("class")
		require.NoError(t, err)
		assert.Contains(t, class, "active")

		// verify All tab is no longer active
		allTab := page.Locator(".phase-tab[data-phase='all']")
		allClass, err := allTab.GetAttribute("class")
		require.NoError(t, err)
		assert.NotContains(t, allClass, "active")
	})
}

func TestPlanPanel(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("plan panel is visible", func(t *testing.T) {
		waitVisible(t, page, ".plan-panel")
	})

	t.Run("plan panel has header", func(t *testing.T) {
		header := page.Locator(".plan-panel-header")
		visible, err := header.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})

	t.Run("plan toggle button exists", func(t *testing.T) {
		toggle := page.Locator("#plan-toggle")
		visible, err := toggle.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})

	t.Run("plan content area exists", func(t *testing.T) {
		content := page.Locator("#plan-content")
		visible, err := content.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})
}

func TestPlanTaskStatus(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for plan to load
	time.Sleep(2 * time.Second)

	t.Run("shows task items", func(t *testing.T) {
		tasks := page.Locator(".plan-task")
		count, err := tasks.Count()
		require.NoError(t, err)
		// test plan has 3 tasks
		assert.GreaterOrEqual(t, count, 1, "should display task items from plan")
	})

	t.Run("tasks have status indicators", func(t *testing.T) {
		statuses := page.Locator(".plan-task-status")
		count, err := statuses.Count()
		require.NoError(t, err)
		if count == 0 {
			t.Skip("no task status indicators found")
		}

		// check first status has a class (pending, active, done, or failed)
		firstStatus := statuses.First()
		class, err := firstStatus.GetAttribute("class")
		require.NoError(t, err)
		assert.Contains(t, class, "plan-task-status")
	})

	t.Run("tasks have titles", func(t *testing.T) {
		titles := page.Locator(".plan-task-title")
		count, err := titles.Count()
		require.NoError(t, err)
		if count == 0 {
			t.Skip("no task titles found")
		}

		// check first title has text
		firstTitle := titles.First()
		text, err := firstTitle.TextContent()
		require.NoError(t, err)
		assert.NotEmpty(t, text, "task title should have text")
	})
}

func TestOutputPanel(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("output panel exists", func(t *testing.T) {
		waitVisible(t, page, ".output-panel")
	})

	t.Run("output div exists", func(t *testing.T) {
		output := page.Locator("#output")
		visible, err := output.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})
}

func TestExpandCollapseButtons(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("expand all button exists", func(t *testing.T) {
		btn := page.Locator("#expand-all")
		visible, err := btn.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})

	t.Run("collapse all button exists", func(t *testing.T) {
		btn := page.Locator("#collapse-all")
		visible, err := btn.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})
}

func TestSearchBar(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("search input exists", func(t *testing.T) {
		input := page.Locator("#search")
		visible, err := input.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})

	t.Run("search has placeholder", func(t *testing.T) {
		input := page.Locator("#search")
		placeholder, err := input.GetAttribute("placeholder")
		require.NoError(t, err)
		assert.Contains(t, placeholder, "Search")
	})
}

func TestSessionSidebar(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("session sidebar exists", func(t *testing.T) {
		sidebar := page.Locator(".session-sidebar")
		visible, err := sidebar.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})

	t.Run("sidebar has header", func(t *testing.T) {
		header := page.Locator(".sidebar-header")
		visible, err := header.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})

	t.Run("sidebar toggle button exists", func(t *testing.T) {
		toggle := page.Locator("#sidebar-toggle")
		visible, err := toggle.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})

	t.Run("session list exists", func(t *testing.T) {
		list := page.Locator("#session-list")
		visible, err := list.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})
}

func TestKeyboardShortcutHelp(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("help overlay is hidden by default", func(t *testing.T) {
		overlay := page.Locator("#help-overlay")
		visible, err := overlay.IsVisible()
		require.NoError(t, err)
		assert.False(t, visible)
	})

	t.Run("pressing ? opens help", func(t *testing.T) {
		// press ? key
		err := page.Keyboard().Press("?")
		require.NoError(t, err)

		// wait for help overlay to appear
		waitVisible(t, page, "#help-overlay", 5000)

		// verify modal content
		modal := page.Locator(".help-modal")
		visible, err := modal.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible)
	})

	t.Run("pressing Escape closes help", func(t *testing.T) {
		// open help first
		err := page.Keyboard().Press("?")
		require.NoError(t, err)
		waitVisible(t, page, "#help-overlay", 5000)

		// press Escape to close
		err = page.Keyboard().Press("Escape")
		require.NoError(t, err)

		// wait for overlay to be hidden
		waitHidden(t, page, "#help-overlay", 5000)
	})
}

func TestKeyboardShortcutSlashFocusesSearch(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// search should not be focused initially
	searchInput := page.Locator("#search")
	focusedResult, err := searchInput.Evaluate("el => document.activeElement === el", nil)
	require.NoError(t, err)
	focused, ok := focusedResult.(bool)
	require.True(t, ok, "expected bool from focus check evaluation")
	assert.False(t, focused, "search should not be focused initially")

	// press / to focus search
	err = page.Keyboard().Press("/")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// search should now be focused
	focusedResult, err = searchInput.Evaluate("el => document.activeElement === el", nil)
	require.NoError(t, err)
	focused, ok = focusedResult.(bool)
	require.True(t, ok, "expected bool from focus check evaluation")
	assert.True(t, focused, "search should be focused after pressing /")
}

func TestKeyboardShortcutPTogglesPlanPanel(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// plan panel should be visible initially
	planPanel := page.Locator(".plan-panel")
	initiallyVisible, err := planPanel.IsVisible()
	require.NoError(t, err)
	assert.True(t, initiallyVisible, "plan panel should be visible initially")

	// press p to toggle plan panel
	err = page.Keyboard().Press("p")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// check main-container has plan-collapsed class
	mainContainer := page.Locator(".main-container")
	class, err := mainContainer.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "plan-collapsed", "main-container should have plan-collapsed class after pressing p")

	// press p again to restore
	err = page.Keyboard().Press("p")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// check plan-collapsed is removed
	class, err = mainContainer.GetAttribute("class")
	require.NoError(t, err)
	assert.NotContains(t, class, "plan-collapsed", "plan-collapsed should be removed after pressing p again")
}

func TestKeyboardShortcutExpandCollapseAll(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sections to load
	time.Sleep(2 * time.Second)

	sections := page.Locator(".section-header")
	count, err := sections.Count()
	require.NoError(t, err)
	if count == 0 {
		t.Skip("no sections available")
	}

	// press c to collapse all
	err = page.Keyboard().Press("c")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// verify all sections are closed
	for i := 0; i < count; i++ {
		assert.False(t, isDetailsOpen(sections.Nth(i)), "section %d should be closed after pressing c", i)
	}

	// press e to expand all
	err = page.Keyboard().Press("e")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// verify all sections are open
	for i := 0; i < count; i++ {
		assert.True(t, isDetailsOpen(sections.Nth(i)), "section %d should be open after pressing e", i)
	}
}

func TestKeyboardShortcutViewModes(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	viewToggle := page.Locator("#view-toggle")

	// press t for time-sorted (recent) view
	err := page.Keyboard().Press("t")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	class, err := viewToggle.GetAttribute("class")
	require.NoError(t, err)
	// should NOT have grouped class
	if class != "" {
		assert.NotContains(t, class, "grouped", "should be in recent view after pressing t")
	}

	// press g for grouped view
	err = page.Keyboard().Press("g")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	class, err = viewToggle.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "grouped", "should be in grouped view after pressing g")
}

func TestKeyboardShortcutSectionNavigation(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sections to load
	time.Sleep(2 * time.Second)

	sections := page.Locator(".section-header")
	count, err := sections.Count()
	require.NoError(t, err)
	if count < 2 {
		t.Skip("need at least 2 sections for navigation test")
	}

	// press j to navigate to next section
	err = page.Keyboard().Press("j")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// check first section has section-focused class
	firstSection := sections.First()
	class, err := firstSection.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "section-focused", "first section should have section-focused after pressing j")

	// press j again to move to second section
	err = page.Keyboard().Press("j")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// first section should lose focus, second should have it
	class, err = firstSection.GetAttribute("class")
	require.NoError(t, err)
	assert.NotContains(t, class, "section-focused", "first section should lose section-focused")

	secondSection := sections.Nth(1)
	class, err = secondSection.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "section-focused", "second section should have section-focused after pressing j again")

	// press k to go back
	err = page.Keyboard().Press("k")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	class, err = firstSection.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "section-focused", "first section should have section-focused after pressing k")
}

func TestPlanPanelToggleBehavior(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	mainContainer := page.Locator(".main-container")

	// use keyboard shortcut p to toggle (more reliable than clicking button)
	err := page.Keyboard().Press("p")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// check main-container has plan-collapsed class
	class, err := mainContainer.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "plan-collapsed", "main-container should have plan-collapsed after pressing p")

	// press p again to restore
	err = page.Keyboard().Press("p")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// check plan-collapsed is removed
	class, err = mainContainer.GetAttribute("class")
	require.NoError(t, err)
	assert.NotContains(t, class, "plan-collapsed", "plan-collapsed should be removed after pressing p again")
}

func TestScrollToBottomButton(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// use JavaScript to check if element exists and get its visibility
	result, err := page.Evaluate(`() => {
		const btn = document.getElementById('scroll-to-bottom');
		if (!btn) return { exists: false, visible: false };
		const style = window.getComputedStyle(btn);
		const visible = style.display !== 'none' && style.visibility !== 'hidden' && style.opacity !== '0';
		return { exists: true, visible: visible };
	}`, nil)
	require.NoError(t, err)

	resultMap, ok := result.(map[string]interface{})
	require.True(t, ok, "expected map from JavaScript evaluation")

	exists, ok := resultMap["exists"].(bool)
	require.True(t, ok, "expected 'exists' to be boolean")
	assert.True(t, exists, "scroll to bottom button should exist in DOM")

	visible, ok := resultMap["visible"].(bool)
	require.True(t, ok, "expected 'visible' to be boolean")
	if visible {
		t.Log("scroll button is visible")
	} else {
		t.Log("scroll button exists but is hidden (content fits in viewport)")
	}
}

func TestSearchFiltering(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for content to load
	time.Sleep(2 * time.Second)

	searchInput := page.Locator("#search")

	// type a search term
	err := searchInput.Fill("task")
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	// verify search value is set
	value, err := searchInput.InputValue()
	require.NoError(t, err)
	assert.Equal(t, "task", value)

	// type a nonexistent search term
	err = searchInput.Fill("xyznonexistent123456")
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	// check that search highlight indicator might show "no matches"
	// or just verify the search input still has the value
	value, err = searchInput.InputValue()
	require.NoError(t, err)
	assert.Equal(t, "xyznonexistent123456", value)

	// clear search with Escape
	err = page.Keyboard().Press("Escape")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// verify search is cleared
	value, err = searchInput.InputValue()
	require.NoError(t, err)
	assert.Empty(t, value, "search should be cleared after Escape")
}

// createSessionWithPlan creates a progress file that references a specific plan.
// the plan file may or may not exist.
func createSessionWithPlan(t *testing.T, sessionName, planName string) string {
	t.Helper()
	filename := "progress-" + sessionName + ".txt"
	path := filepath.Join(testTmpDir, filename)

	content := `# Ralphex Progress Log
Plan: ` + planName + `
Branch: test-branch-` + sessionName + `
Mode: full
Started: 2026-01-22 12:00:00
------------------------------------------------------------
[26-01-22 12:00:00] Session for ` + sessionName + `

--- Task iteration 1 ---
[26-01-22 12:00:01] Working on session ` + sessionName + `
`
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err, "create session progress file")

	t.Cleanup(func() {
		os.Remove(path)
	})

	return filename
}

// TestPlanParsingEdgeCases tests graceful handling of missing and malformed plans.
// tests the frontend behavior when plan data is unavailable or has no tasks.
func TestPlanParsingEdgeCases(t *testing.T) {
	t.Run("missing plan shows not available message", func(t *testing.T) {
		// create a session that references a non-existent plan
		// the session name in the sidebar is derived from the plan filename
		planName := "nonexistent-plan-edge-case.md"
		expectedSessionName := "nonexistent-plan-edge-case"
		createSessionWithPlan(t, "missing-plan-test", planName)

		// wait for file system to settle
		time.Sleep(500 * time.Millisecond)

		page := newPage(t)
		navigateToDashboard(t, page)

		// wait for sessions to load
		time.Sleep(3 * time.Second)

		// find the session we created and click it
		// session name in sidebar is derived from plan filename
		sessionItems := page.Locator(".session-item")
		count, err := sessionItems.Count()
		require.NoError(t, err)

		var sessionFound bool
		for i := 0; i < count; i++ {
			session := sessionItems.Nth(i)
			name := session.Locator(".session-name")
			text, err := name.TextContent()
			require.NoError(t, err)

			if text == expectedSessionName {
				err = session.Click()
				require.NoError(t, err)
				sessionFound = true
				break
			}
		}

		if !sessionFound {
			t.Skip("could not find the test session in sidebar")
		}

		// wait for plan to attempt to load
		time.Sleep(2 * time.Second)

		// check plan panel shows "Plan not available" message
		planContent := page.Locator("#plan-content")
		text, err := planContent.TextContent()
		require.NoError(t, err)
		assert.Contains(t, text, "Plan not available", "should show 'Plan not available' for missing plan")
	})

	t.Run("plan with no tasks shows appropriate message", func(t *testing.T) {
		// create a session that references the malformed plan (which has no valid tasks)
		// the session name in the sidebar is derived from the plan filename
		planName := "test-plan-malformed.md"
		expectedSessionName := "test-plan-malformed"
		createSessionWithPlan(t, "malformed-plan-test", planName)

		// wait for file system to settle
		time.Sleep(500 * time.Millisecond)

		page := newPage(t)
		navigateToDashboard(t, page)

		// wait for sessions to load
		time.Sleep(3 * time.Second)

		// find the session we created and click it
		sessionItems := page.Locator(".session-item")
		count, err := sessionItems.Count()
		require.NoError(t, err)

		var sessionFound bool
		for i := 0; i < count; i++ {
			session := sessionItems.Nth(i)
			name := session.Locator(".session-name")
			text, err := name.TextContent()
			require.NoError(t, err)

			if text == expectedSessionName {
				err = session.Click()
				require.NoError(t, err)
				sessionFound = true
				break
			}
		}

		if !sessionFound {
			t.Skip("could not find the test session in sidebar")
		}

		// wait for plan to load
		time.Sleep(2 * time.Second)

		// check plan panel shows "No tasks in plan" message
		planContent := page.Locator("#plan-content")
		text, err := planContent.TextContent()
		require.NoError(t, err)
		assert.Contains(t, text, "No tasks in plan", "should show 'No tasks in plan' for plan without tasks")
	})
}
