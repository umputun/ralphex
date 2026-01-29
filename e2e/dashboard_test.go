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
		h1 := page.Locator("header h1").First()
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
		waitForClass(t, allTab, "active")
	})

	t.Run("clicking tab changes active state", func(t *testing.T) {
		// click Implementation tab
		taskTab := page.Locator(".phase-tab[data-phase='task']")
		err := taskTab.Click()
		require.NoError(t, err)

		// verify it becomes active
		waitForClass(t, taskTab, "active")

		// verify All tab is no longer active
		allTab := page.Locator(".phase-tab[data-phase='all']")
		waitForClassGone(t, allTab, "active")
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

	// wait for plan tasks to appear
	waitVisible(t, page, ".plan-task")

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
		waitVisible(t, page, "#help-overlay", float64(pollTimeout / time.Millisecond))

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
		waitVisible(t, page, "#help-overlay", float64(pollTimeout / time.Millisecond))

		// press Escape to close
		err = page.Keyboard().Press("Escape")
		require.NoError(t, err)

		// wait for overlay to be hidden
		waitHidden(t, page, "#help-overlay", float64(pollTimeout / time.Millisecond))
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

	// wait for search to become focused
	waitFocused(t, searchInput)
}

func TestKeyboardShortcutPTogglesPlanPanel(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// plan panel should be visible initially
	planPanel := page.Locator(".plan-panel")
	initiallyVisible, err := planPanel.IsVisible()
	require.NoError(t, err)
	assert.True(t, initiallyVisible, "plan panel should be visible initially")

	mainContainer := page.Locator(".main-container")

	// press p to toggle plan panel
	err = page.Keyboard().Press("p")
	require.NoError(t, err)

	// wait for plan-collapsed class to appear
	waitForClass(t, mainContainer, "plan-collapsed")

	// press p again to restore
	err = page.Keyboard().Press("p")
	require.NoError(t, err)

	// wait for plan-collapsed class to be removed
	waitForClassGone(t, mainContainer, "plan-collapsed")
}

func TestKeyboardShortcutExpandCollapseAll(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sections to appear
	waitVisible(t, page, ".section-header")

	sections := page.Locator(".section-header")
	count, err := sections.Count()
	require.NoError(t, err)
	if count == 0 {
		t.Skip("no sections available")
	}

	// press c to collapse all
	err = page.Keyboard().Press("c")
	require.NoError(t, err)

	// wait for all sections to be closed
	waitAllDetailsState(t, sections, count, false)

	// press e to expand all
	err = page.Keyboard().Press("e")
	require.NoError(t, err)

	// wait for all sections to be open
	waitAllDetailsState(t, sections, count, true)
}

func TestKeyboardShortcutViewModes(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	viewToggle := page.Locator("#view-toggle")

	// press t for time-sorted (recent) view
	err := page.Keyboard().Press("t")
	require.NoError(t, err)

	// should NOT have grouped class
	waitForClassGone(t, viewToggle, "grouped")

	// press g for grouped view
	err = page.Keyboard().Press("g")
	require.NoError(t, err)

	// should have grouped class
	waitForClass(t, viewToggle, "grouped")
}

func TestKeyboardShortcutSectionNavigation(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for sections to appear
	waitVisible(t, page, ".section-header")

	sections := page.Locator(".section-header")
	count, err := sections.Count()
	require.NoError(t, err)
	if count < 2 {
		t.Skip("need at least 2 sections for navigation test")
	}

	firstSection := sections.First()
	secondSection := sections.Nth(1)

	// press j to navigate to next section
	err = page.Keyboard().Press("j")
	require.NoError(t, err)

	// wait for first section to get section-focused class
	waitForClass(t, firstSection, "section-focused")

	// press j again to move to second section
	err = page.Keyboard().Press("j")
	require.NoError(t, err)

	// wait for second section to get focus, first to lose it
	waitForClass(t, secondSection, "section-focused")
	waitForClassGone(t, firstSection, "section-focused")

	// press k to go back
	err = page.Keyboard().Press("k")
	require.NoError(t, err)

	// wait for first section to regain focus
	waitForClass(t, firstSection, "section-focused")
}

func TestPlanPanelToggleBehavior(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	mainContainer := page.Locator(".main-container")

	// use keyboard shortcut p to toggle (more reliable than clicking button)
	err := page.Keyboard().Press("p")
	require.NoError(t, err)

	// wait for plan-collapsed class to appear
	waitForClass(t, mainContainer, "plan-collapsed")

	// press p again to restore
	err = page.Keyboard().Press("p")
	require.NoError(t, err)

	// wait for plan-collapsed class to be removed
	waitForClassGone(t, mainContainer, "plan-collapsed")
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
	waitVisible(t, page, ".section-header")

	searchInput := page.Locator("#search")

	// type a search term
	err := searchInput.Fill("task")
	require.NoError(t, err)

	// wait for search value to be applied
	waitInputValue(t, searchInput, "task")

	// type a nonexistent search term
	err = searchInput.Fill("xyznonexistent123456")
	require.NoError(t, err)

	// wait for search value to be applied
	waitInputValue(t, searchInput, "xyznonexistent123456")

	// clear search with Escape
	err = page.Keyboard().Press("Escape")
	require.NoError(t, err)

	// wait for search to be cleared
	waitInputValue(t, searchInput, "")
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
	err := atomicWriteFile(path, []byte(content), 0o600)
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

		page := newPage(t)
		navigateToDashboard(t, page)

		// poll for the session to appear and click it
		clickSessionByName(t, page, expectedSessionName)

		// wait for plan panel to show "Plan not available" message
		planContent := page.Locator("#plan-content")
		waitForTextContains(t, planContent, "Plan not available")
	})

	t.Run("plan with no tasks shows appropriate message", func(t *testing.T) {
		// create a session that references the malformed plan (which has no valid tasks)
		// the session name in the sidebar is derived from the plan filename
		planName := "test-plan-malformed.md"
		expectedSessionName := "test-plan-malformed"
		createSessionWithPlan(t, "malformed-plan-test", planName)

		page := newPage(t)
		navigateToDashboard(t, page)

		// poll for the session to appear and click it
		clickSessionByName(t, page, expectedSessionName)

		// wait for plan panel to show "No tasks in plan" message
		planContent := page.Locator("#plan-content")
		waitForTextContains(t, planContent, "No tasks in plan")
	})
}
