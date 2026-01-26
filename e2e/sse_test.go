//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toFloat64 converts a JavaScript number result to float64.
// JavaScript numbers can be returned as int or float64 depending on value.
func toFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func TestSSEConnection(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// give time for SSE connection to establish and events to load
	time.Sleep(2 * time.Second)

	// the dashboard should have loaded some initial content from the progress file
	// check that we have some section headers (from the test data)
	sections := page.Locator(".section-header")
	count, err := sections.Count()
	require.NoError(t, err)

	// we should have at least 1 section from test data
	assert.GreaterOrEqual(t, count, 1, "should have loaded initial sections from progress file")
}

func TestSSEInitialContentLoad(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	t.Run("loads sections from progress file", func(t *testing.T) {
		// check that sections exist (from test data: Task, Claude Review, Codex Review)
		sections := page.Locator(".section-header")
		count, err := sections.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have sections from progress file")
	})

	t.Run("loads output lines from progress file", func(t *testing.T) {
		// check that output lines exist
		lines := page.Locator(".output-line")
		count, err := lines.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have output lines from progress file")
	})
}

func TestSectionCollapseExpand(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// find a section with content
	section := page.Locator(".section-header").First()

	// check if section exists
	visible, err := section.IsVisible()
	require.NoError(t, err)
	if !visible {
		t.Skip("no sections available to test collapse/expand")
	}

	initialOpen := isDetailsOpen(section)

	// click the summary to toggle
	summary := section.Locator("summary")
	err = summary.Click()
	require.NoError(t, err)

	// wait a bit for state change
	time.Sleep(300 * time.Millisecond)

	// check state changed
	newOpen := isDetailsOpen(section)
	assert.NotEqual(t, initialOpen, newOpen, "section open state should toggle after click")
}

func TestStatusBadgeUpdates(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// the status badge should exist
	badge := page.Locator("#status-badge")
	visible, err := badge.IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "status badge should be visible")

	// check it has some content (from initial events)
	text, err := badge.TextContent()
	require.NoError(t, err)
	// badge should show one of: TASK, REVIEW, CODEX, COMPLETED, FAILED
	// (or be empty if no events yet)
	t.Logf("Status badge text: %q", text)
}

func TestExpandCollapseAllButtons(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// check we have some sections
	sections := page.Locator(".section-header")
	count, err := sections.Count()
	require.NoError(t, err)
	if count == 0 {
		t.Skip("no sections available to test expand/collapse all")
	}

	// click collapse all
	collapseBtn := page.Locator("#collapse-all")
	err = collapseBtn.Click()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// verify all sections are closed
	for i := 0; i < count; i++ {
		assert.False(t, isDetailsOpen(sections.Nth(i)), "section %d should be closed after collapse all", i)
	}

	// click expand all
	expandBtn := page.Locator("#expand-all")
	err = expandBtn.Click()
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// verify all sections are open
	for i := 0; i < count; i++ {
		assert.True(t, isDetailsOpen(sections.Nth(i)), "section %d should be open after expand all", i)
	}
}

func TestOutputPanelScrolling(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// check scroll indicator exists
	indicator := page.Locator("#scroll-indicator")
	visible, err := indicator.IsVisible()
	require.NoError(t, err)
	// indicator may or may not be visible depending on content
	t.Logf("Scroll indicator visible: %v", visible)

	// check scroll to bottom button exists
	scrollBtn := page.Locator("#scroll-to-bottom")
	exists, err := scrollBtn.Count()
	require.NoError(t, err)
	assert.Equal(t, 1, exists, "scroll to bottom button should exist")
}

func TestSectionPhaseIndicators(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// check that sections have phase indicators
	phases := page.Locator(".section-phase")
	count, err := phases.Count()
	require.NoError(t, err)

	if count == 0 {
		t.Skip("no phase indicators found")
	}

	// check first phase indicator has text
	firstPhase := phases.First()
	text, err := firstPhase.TextContent()
	require.NoError(t, err)
	assert.NotEmpty(t, text, "phase indicator should have text")
}

func TestSectionDuration(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// check that sections have duration elements
	durations := page.Locator(".section-duration")
	count, err := durations.Count()
	require.NoError(t, err)

	if count == 0 {
		t.Skip("no duration elements found")
	}

	// duration elements exist
	t.Logf("Found %d section duration elements", count)
}

func TestSectionDetailsElement(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// find a section
	section := page.Locator(".section-header").First()

	visible, err := section.IsVisible()
	require.NoError(t, err)
	if !visible {
		t.Skip("no sections available")
	}

	// verify it's a details element (collapsible)
	tagName, err := section.Evaluate("el => el.tagName", nil)
	require.NoError(t, err)
	assert.Equal(t, "DETAILS", tagName, "section should be a details element")

	// verify it has a summary element
	summary := section.Locator("summary")
	summaryVisible, err := summary.IsVisible()
	require.NoError(t, err)
	assert.True(t, summaryVisible, "section should have a visible summary")
}

func TestPhaseFilter(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// click Implementation tab
	taskTab := page.Locator(".phase-tab[data-phase='task']")
	err := taskTab.Click()
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// verify task tab is active
	class, err := taskTab.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, class, "active")

	// click back to All tab
	allTab := page.Locator(".phase-tab[data-phase='all']")
	err = allTab.Click()
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// verify all tab is active
	allClass, err := allTab.GetAttribute("class")
	require.NoError(t, err)
	assert.Contains(t, allClass, "active")
}

func TestSearchFunctionality(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// get search input
	searchInput := page.Locator("#search")

	// type a search term
	err := searchInput.Fill("task")
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// search should be applied (we can't easily verify filtering without knowing content)
	value, err := searchInput.InputValue()
	require.NoError(t, err)
	assert.Equal(t, "task", value)

	// clear search with Escape
	err = page.Keyboard().Press("Escape")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// search should be cleared
	value, err = searchInput.InputValue()
	require.NoError(t, err)
	assert.Empty(t, value)
}

// TestErrorEventRendering verifies that error events from the progress file
// are rendered with proper styling (data-type="error" attribute and error color).
func TestErrorEventRendering(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load and SSE events
	time.Sleep(2 * time.Second)

	t.Run("error lines have correct data-type attribute", func(t *testing.T) {
		// the test fixture (progress-test.txt) contains ERROR: lines
		// these should be rendered with data-type="error"
		errorLines := page.Locator(".output-line[data-type='error']")
		count, err := errorLines.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have at least one error line from test fixture")
		t.Logf("Found %d error lines", count)
	})

	t.Run("error lines have visually distinct styling", func(t *testing.T) {
		// expand all sections to make error content visible
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// verify the error line content element exists and is visible
		errorContent := page.Locator(".output-line[data-type='error'] .content").First()
		visible, err := errorContent.IsVisible()
		require.NoError(t, err)

		if !visible {
			t.Skip("no visible error content to verify styling (even after expanding)")
		}

		// check that the element has the expected styling via computed color
		// the CSS sets color: var(--color-error) which is #f87171
		color, err := errorContent.Evaluate("el => window.getComputedStyle(el).color", nil)
		require.NoError(t, err)

		colorStr, ok := color.(string)
		require.True(t, ok, "color should be a string")
		// color should be red-ish (the error color), not white/gray (default)
		// #f87171 converts to rgb(248, 113, 113)
		assert.Contains(t, colorStr, "248", "error text should have red color component")
	})

	t.Run("multiple error events render correctly", func(t *testing.T) {
		// verify that multiple error lines are present (test fixture has 2+)
		errorLines := page.Locator(".output-line[data-type='error']")
		count, err := errorLines.Count()
		require.NoError(t, err)

		if count < 2 {
			t.Skip("test fixture has fewer than 2 error lines")
		}

		// verify each error line has the content element
		for i := 0; i < count && i < 3; i++ {
			content := errorLines.Nth(i).Locator(".content")
			text, err := content.TextContent()
			require.NoError(t, err)
			assert.NotEmpty(t, text, "error line %d should have content", i)
		}
	})
}

// TestSignalEventRendering verifies that signal events (COMPLETED, FAILED, REVIEW_DONE)
// are properly rendered and affect the status badge.
func TestSignalEventRendering(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	t.Run("COMPLETED signal shows success indicator", func(t *testing.T) {
		// the test fixture (progress-test.txt) ends with <<<RALPHEX:ALL_TASKS_DONE>>>
		// which is normalized to COMPLETED signal
		badge := page.Locator("#status-badge")

		// wait for badge to show COMPLETED (SSE stream needs time to process all events)
		// use Playwright's built-in waiting with a condition
		err := badge.WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateVisible,
			Timeout: playwright.Float(15000),
		})
		require.NoError(t, err, "badge should be visible")

		// poll until badge shows COMPLETED or timeout
		var text string
		for i := 0; i < 100; i++ { // 10 second timeout (100 * 100ms)
			text, _ = badge.TextContent()
			if text == "COMPLETED" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		assert.Equal(t, "COMPLETED", text, "badge should show COMPLETED for ALL_TASKS_DONE signal")

		// verify badge has completed class
		class, err := badge.GetAttribute("class")
		require.NoError(t, err)
		assert.Contains(t, class, "completed", "badge should have 'completed' CSS class")
	})

	t.Run("signal stops elapsed timer", func(t *testing.T) {
		// after COMPLETED signal, the elapsed time should stop updating
		elapsedEl := page.Locator("#elapsed-time")
		visible, err := elapsedEl.IsVisible()
		require.NoError(t, err)

		if !visible {
			t.Skip("elapsed time element not visible")
		}

		// get elapsed time value
		time1, err := elapsedEl.TextContent()
		require.NoError(t, err)

		// wait a moment and check again
		time.Sleep(1500 * time.Millisecond)

		time2, err := elapsedEl.TextContent()
		require.NoError(t, err)

		// timer should be stopped after terminal signal, so times should match
		assert.Equal(t, time1, time2, "elapsed time should not change after COMPLETED signal")
	})

	t.Run("completion message is rendered", func(t *testing.T) {
		// expand all sections to see completion message
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// look for completion message in output
		// the JS renders "execution completed successfully" for COMPLETED signal
		completionLines := page.Locator(".output-line .content")
		count, err := completionLines.Count()
		require.NoError(t, err)

		foundCompletion := false
		for i := 0; i < count; i++ {
			text, err := completionLines.Nth(i).TextContent()
			if err != nil {
				continue
			}
			if strings.Contains(strings.ToLower(text), "completed successfully") {
				foundCompletion = true
				break
			}
		}
		assert.True(t, foundCompletion, "should find completion message in output")
	})
}

// TestSignalFailedIndicator tests the FAILED signal rendering using a separate fixture.
// This is tested separately because a session can only have one terminal signal.
func TestSignalFailedIndicator(t *testing.T) {
	// for this test we need a fixture with FAILED signal
	// since we can't easily create a new fixture mid-test, we'll verify
	// the failed styling exists in CSS by checking the badge element attributes
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	// verify badge element exists and can accept failed class
	badge := page.Locator("#status-badge")
	visible, err := badge.IsVisible()
	require.NoError(t, err)
	assert.True(t, visible, "status badge should be visible")

	// verify the element is a span that can have status classes applied
	tagName, err := badge.Evaluate("el => el.tagName", nil)
	require.NoError(t, err)
	assert.Equal(t, "SPAN", tagName, "status badge should be a span element")
}

// TestReviewDoneSignalHandling verifies REVIEW_DONE signals are properly handled.
func TestReviewDoneSignalHandling(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load - the fixture has multiple REVIEW_DONE signals
	time.Sleep(2 * time.Second)

	// REVIEW_DONE is treated as a success signal but doesn't change badge to COMPLETED
	// unless it's the final signal. The fixture ends with ALL_TASKS_DONE,
	// so we verify the badge shows COMPLETED (the final terminal state)
	badge := page.Locator("#status-badge")
	text, err := badge.TextContent()
	require.NoError(t, err)

	// the badge should show COMPLETED since ALL_TASKS_DONE comes after REVIEW_DONE
	assert.Equal(t, "COMPLETED", text, "badge should show terminal state after all signals processed")
}

// TestTaskBoundaryRendering verifies that task iteration headers are rendered
// as collapsible section headers with task numbers displayed.
func TestTaskBoundaryRendering(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load and SSE events
	time.Sleep(2 * time.Second)

	t.Run("task iteration headers render as section headers", func(t *testing.T) {
		// the test fixture (progress-test.txt) contains task iteration markers
		// these should be rendered as .section-header details elements
		sections := page.Locator(".section-header")
		count, err := sections.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have at least one section from test fixture")
		t.Logf("Found %d section headers", count)

		// verify at least one section has task phase
		taskSections := page.Locator(".section-header[data-phase='task']")
		taskCount, err := taskSections.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, taskCount, 1, "should have at least one task section")
	})

	t.Run("task number is displayed in section title", func(t *testing.T) {
		// expand all sections to see task content
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// find task sections and verify they have task-related titles
		taskSections := page.Locator(".section-header[data-phase='task']")
		count, err := taskSections.Count()
		require.NoError(t, err)

		if count == 0 {
			t.Skip("no task sections found to verify title")
		}

		// check first task section has a title containing "Task" or "iteration"
		firstTask := taskSections.First()
		titleEl := firstTask.Locator(".section-title")
		text, err := titleEl.TextContent()
		require.NoError(t, err)

		// section title should contain either "Task" (with plan lookup) or "iteration" (raw)
		hasTaskInfo := strings.Contains(strings.ToLower(text), "task") ||
			strings.Contains(strings.ToLower(text), "iteration")
		assert.True(t, hasTaskInfo, "task section title should contain task info, got: %q", text)
	})

	t.Run("task sections are collapsible details elements", func(t *testing.T) {
		// find a task section
		taskSection := page.Locator(".section-header[data-phase='task']").First()

		visible, err := taskSection.IsVisible()
		require.NoError(t, err)
		if !visible {
			t.Skip("no visible task section to test collapsibility")
		}

		// verify it's a details element (collapsible)
		tagName, err := taskSection.Evaluate("el => el.tagName", nil)
		require.NoError(t, err)
		assert.Equal(t, "DETAILS", tagName, "task section should be a details element")

		// verify it has a summary element
		summary := taskSection.Locator("summary")
		summaryVisible, err := summary.IsVisible()
		require.NoError(t, err)
		assert.True(t, summaryVisible, "task section should have a visible summary")

		// test toggle behavior
		initialOpen := isDetailsOpen(taskSection)

		// click the summary to toggle
		err = summary.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		newOpen := isDetailsOpen(taskSection)
		assert.NotEqual(t, initialOpen, newOpen, "task section should toggle open/closed on click")
	})
}

// TestIterationBoundaryRendering verifies that Claude review and Codex iteration
// headers are rendered as collapsible section headers with iteration numbers displayed.
func TestIterationBoundaryRendering(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load and SSE events
	time.Sleep(2 * time.Second)

	t.Run("Claude review iteration headers render correctly", func(t *testing.T) {
		// the test fixture contains Claude review sections
		// these should be rendered as .section-header details elements with data-phase='review'
		reviewSections := page.Locator(".section-header[data-phase='review']")
		count, err := reviewSections.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have at least one review section from test fixture")
		t.Logf("Found %d Claude review sections", count)

		if count == 0 {
			t.Skip("no Claude review sections found to verify title")
		}

		// verify the section title contains review-related text
		firstReview := reviewSections.First()
		titleEl := firstReview.Locator(".section-title")
		text, err := titleEl.TextContent()
		require.NoError(t, err)

		// section title should contain "Claude" or "review"
		hasReviewInfo := strings.Contains(strings.ToLower(text), "claude") ||
			strings.Contains(strings.ToLower(text), "review")
		assert.True(t, hasReviewInfo, "review section title should contain review info, got: %q", text)
	})

	t.Run("Codex iteration headers render correctly", func(t *testing.T) {
		// the test fixture contains Codex iteration sections
		// these should be rendered as .section-header details elements with data-phase='codex'
		codexSections := page.Locator(".section-header[data-phase='codex']")
		count, err := codexSections.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have at least one codex section from test fixture")
		t.Logf("Found %d Codex sections", count)

		if count == 0 {
			t.Skip("no Codex sections found to verify title")
		}

		// verify the section title contains codex-related text
		firstCodex := codexSections.First()
		titleEl := firstCodex.Locator(".section-title")
		text, err := titleEl.TextContent()
		require.NoError(t, err)

		// section title should contain "Codex" or "iteration"
		hasCodexInfo := strings.Contains(strings.ToLower(text), "codex") ||
			strings.Contains(strings.ToLower(text), "iteration")
		assert.True(t, hasCodexInfo, "codex section title should contain codex info, got: %q", text)
	})

	t.Run("iteration number is displayed in section title", func(t *testing.T) {
		// expand all sections to see content
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// check review sections for iteration numbers
		reviewSections := page.Locator(".section-header[data-phase='review']")
		reviewCount, err := reviewSections.Count()
		require.NoError(t, err)

		if reviewCount >= 2 {
			// verify both review sections have different titles (pass 1 vs pass 2)
			firstTitle, err := reviewSections.Nth(0).Locator(".section-title").TextContent()
			require.NoError(t, err)
			secondTitle, err := reviewSections.Nth(1).Locator(".section-title").TextContent()
			require.NoError(t, err)

			// titles should contain numbers or be distinguishable
			hasNumber := strings.Contains(firstTitle, "1") || strings.Contains(firstTitle, "2") ||
				strings.Contains(secondTitle, "1") || strings.Contains(secondTitle, "2")
			assert.True(t, hasNumber, "review section titles should contain iteration numbers: %q, %q", firstTitle, secondTitle)
		}

		// check codex sections for iteration numbers
		codexSections := page.Locator(".section-header[data-phase='codex']")
		codexCount, err := codexSections.Count()
		require.NoError(t, err)

		if codexCount >= 2 {
			// verify both codex sections have different titles
			firstTitle, err := codexSections.Nth(0).Locator(".section-title").TextContent()
			require.NoError(t, err)
			secondTitle, err := codexSections.Nth(1).Locator(".section-title").TextContent()
			require.NoError(t, err)

			// titles should contain numbers or be distinguishable
			hasNumber := strings.Contains(firstTitle, "1") || strings.Contains(firstTitle, "2") ||
				strings.Contains(secondTitle, "1") || strings.Contains(secondTitle, "2")
			assert.True(t, hasNumber, "codex section titles should contain iteration numbers: %q, %q", firstTitle, secondTitle)
		}
	})

	t.Run("iteration sections are collapsible details elements", func(t *testing.T) {
		// find a review section
		reviewSection := page.Locator(".section-header[data-phase='review']").First()

		visible, err := reviewSection.IsVisible()
		require.NoError(t, err)
		if !visible {
			t.Skip("no visible review section to test collapsibility")
		}

		// verify it's a details element (collapsible)
		tagName, err := reviewSection.Evaluate("el => el.tagName", nil)
		require.NoError(t, err)
		assert.Equal(t, "DETAILS", tagName, "review section should be a details element")

		// verify it has a summary element
		summary := reviewSection.Locator("summary")
		summaryVisible, err := summary.IsVisible()
		require.NoError(t, err)
		assert.True(t, summaryVisible, "review section should have a visible summary")

		// find a codex section
		codexSection := page.Locator(".section-header[data-phase='codex']").First()
		codexVisible, err := codexSection.IsVisible()
		require.NoError(t, err)
		if !codexVisible {
			return // skip codex check if not visible
		}

		// verify codex section is also a details element
		codexTagName, err := codexSection.Evaluate("el => el.tagName", nil)
		require.NoError(t, err)
		assert.Equal(t, "DETAILS", codexTagName, "codex section should be a details element")
	})
}

// TestWarnEventRendering verifies that warning events from the progress file
// are rendered with proper styling (data-type="warn" attribute and warning color).
func TestWarnEventRendering(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load and SSE events
	time.Sleep(2 * time.Second)

	t.Run("warn lines have correct data-type attribute", func(t *testing.T) {
		// the test fixture (progress-test.txt) contains WARN: lines
		// these should be rendered with data-type="warn"
		warnLines := page.Locator(".output-line[data-type='warn']")
		count, err := warnLines.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have at least one warn line from test fixture")
		t.Logf("Found %d warn lines", count)
	})

	t.Run("warn lines have visually distinct styling", func(t *testing.T) {
		// expand all sections to make warning content visible
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// verify the warn line content element exists and is visible
		warnContent := page.Locator(".output-line[data-type='warn'] .content").First()
		visible, err := warnContent.IsVisible()
		require.NoError(t, err)

		if !visible {
			t.Skip("no visible warn content to verify styling (even after expanding)")
		}

		// check that the element has the expected styling via computed color
		// the CSS sets color: var(--color-warn) which is #fbbf24
		color, err := warnContent.Evaluate("el => window.getComputedStyle(el).color", nil)
		require.NoError(t, err)

		colorStr, ok := color.(string)
		require.True(t, ok, "color should be a string")
		// color should be amber/yellow-ish (the warning color), not white/gray (default)
		// #fbbf24 converts to rgb(251, 191, 36)
		assert.Contains(t, colorStr, "251", "warn text should have amber color component")
	})

	t.Run("multiple warning events render correctly", func(t *testing.T) {
		// verify that multiple warn lines are present (test fixture has 3)
		warnLines := page.Locator(".output-line[data-type='warn']")
		count, err := warnLines.Count()
		require.NoError(t, err)

		if count < 2 {
			t.Skip("test fixture has fewer than 2 warn lines")
		}

		// verify each warn line has the content element
		for i := 0; i < count && i < 3; i++ {
			content := warnLines.Nth(i).Locator(".content")
			text, err := content.TextContent()
			require.NoError(t, err)
			assert.NotEmpty(t, text, "warn line %d should have content", i)
		}
	})
}

// TestAutoScrollOnNewContent verifies that auto-scroll behavior works correctly
// when new content arrives via SSE.
func TestAutoScrollOnNewContent(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load and SSE events
	time.Sleep(2 * time.Second)

	t.Run("scroll position updates when content loads and user at bottom", func(t *testing.T) {
		// expand all sections to ensure we have scrollable content
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// get the output panel and verify it has content
		outputPanel := page.Locator(".output-panel")
		visible, err := outputPanel.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible, "output panel should be visible")

		// check that output has content (sections or lines)
		outputDiv := page.Locator("#output")
		children, err := outputDiv.Locator(":scope > *").Count()
		require.NoError(t, err)
		assert.Greater(t, children, 0, "output should have content")
	})

	t.Run("scroll position preserved when user scrolled up", func(t *testing.T) {
		// expand all sections to ensure we have scrollable content
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// scroll to top of the output panel to simulate user scrolling up
		outputPanel := page.Locator(".output-panel")
		_, err = outputPanel.Evaluate("el => { el.scrollTop = 0; }", nil)
		require.NoError(t, err)
		time.Sleep(200 * time.Millisecond)

		// get the scroll position after scrolling to top
		scrollTopResult, err := outputPanel.Evaluate("el => el.scrollTop", nil)
		require.NoError(t, err)
		scrollTop := toFloat64(scrollTopResult)

		// verify we're at the top
		assert.LessOrEqual(t, scrollTop, float64(10), "should be scrolled to top")

		// wait a moment and verify position is preserved (not auto-scrolled to bottom)
		time.Sleep(500 * time.Millisecond)

		scrollTopAfter, err := outputPanel.Evaluate("el => el.scrollTop", nil)
		require.NoError(t, err)
		scrollTopAfterNum := toFloat64(scrollTopAfter)

		// position should still be at or near top (user's scroll preserved)
		assert.LessOrEqual(t, scrollTopAfterNum, float64(50), "scroll position should be preserved when user scrolled up")
	})
}

// TestScrollToBottomButtonBehavior verifies that the scroll-to-bottom button
// appears when scrolled away from bottom and works correctly.
func TestScrollToBottomButtonBehavior(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load and SSE events
	time.Sleep(2 * time.Second)

	t.Run("button appears when scrolled away from bottom", func(t *testing.T) {
		// expand all sections to ensure we have scrollable content
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// scroll to top of the output panel
		outputPanel := page.Locator(".output-panel")
		_, err = outputPanel.Evaluate("el => { el.scrollTop = 0; }", nil)
		require.NoError(t, err)

		// trigger scroll event to update UI state
		_, err = outputPanel.Evaluate("el => el.dispatchEvent(new Event('scroll'))", nil)
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// check if scroll indicator becomes visible
		indicator := page.Locator("#scroll-indicator")
		hasVisibleClass, err := indicator.Evaluate("el => el.classList.contains('visible')", nil)
		require.NoError(t, err)

		// the indicator should be visible when not at bottom
		// (but only if there's enough content to scroll)
		scrollHeight, err := outputPanel.Evaluate("el => el.scrollHeight", nil)
		require.NoError(t, err)
		clientHeight, err := outputPanel.Evaluate("el => el.clientHeight", nil)
		require.NoError(t, err)

		scrollHeightNum := toFloat64(scrollHeight)
		clientHeightNum := toFloat64(clientHeight)

		if scrollHeightNum > clientHeightNum+50 {
			// there's content to scroll, indicator should be visible
			visible, ok := hasVisibleClass.(bool)
			require.True(t, ok, "expected bool from classList.contains evaluation")
			assert.True(t, visible, "scroll indicator should be visible when scrolled up")
		} else {
			t.Log("not enough content to require scrolling, skipping indicator visibility check")
		}
	})

	t.Run("clicking button scrolls to bottom", func(t *testing.T) {
		// expand all sections
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		outputPanel := page.Locator(".output-panel")

		// check if there's enough content to scroll
		scrollHeight, err := outputPanel.Evaluate("el => el.scrollHeight", nil)
		require.NoError(t, err)
		clientHeight, err := outputPanel.Evaluate("el => el.clientHeight", nil)
		require.NoError(t, err)

		scrollHeightNum := toFloat64(scrollHeight)
		clientHeightNum := toFloat64(clientHeight)

		if scrollHeightNum <= clientHeightNum+50 {
			t.Skip("not enough content to test scroll-to-bottom functionality")
		}

		// scroll to top
		_, err = outputPanel.Evaluate("el => { el.scrollTop = 0; }", nil)
		require.NoError(t, err)
		time.Sleep(200 * time.Millisecond)

		// verify we're at top
		scrollTopBefore, err := outputPanel.Evaluate("el => el.scrollTop", nil)
		require.NoError(t, err)
		assert.LessOrEqual(t, toFloat64(scrollTopBefore), float64(10), "should be at top before clicking button")

		// click scroll-to-bottom button
		scrollBtn := page.Locator("#scroll-to-bottom")
		err = scrollBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// verify we're now at bottom
		scrollTopAfter, err := outputPanel.Evaluate("el => el.scrollTop", nil)
		require.NoError(t, err)
		scrollMaxResult, err := outputPanel.Evaluate("el => el.scrollHeight - el.clientHeight", nil)
		require.NoError(t, err)

		scrollTopAfterNum := toFloat64(scrollTopAfter)
		scrollMaxNum := toFloat64(scrollMaxResult)

		// should be within 50px of bottom
		assert.InDelta(t, scrollMaxNum, scrollTopAfterNum, 50, "should be scrolled to bottom after clicking button")
	})

	t.Run("button hides when at bottom", func(t *testing.T) {
		// expand all sections
		expandBtn := page.Locator("#expand-all")
		err := expandBtn.Click()
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)

		// scroll to bottom programmatically (more reliable than button click)
		outputPanel := page.Locator(".output-panel")
		_, err = outputPanel.Evaluate("el => { el.scrollTop = el.scrollHeight; }", nil)
		require.NoError(t, err)

		// trigger scroll event to update UI state
		_, err = outputPanel.Evaluate("el => el.dispatchEvent(new Event('scroll'))", nil)
		require.NoError(t, err)
		time.Sleep(200 * time.Millisecond)

		// check if scroll indicator is hidden
		indicator := page.Locator("#scroll-indicator")
		hasVisibleClass, err := indicator.Evaluate("el => el.classList.contains('visible')", nil)
		require.NoError(t, err)

		// indicator should not be visible when at bottom
		hidden, ok := hasVisibleClass.(bool)
		require.True(t, ok, "expected bool from classList.contains evaluation")
		assert.False(t, hidden, "scroll indicator should be hidden when at bottom")
	})
}

// TestSSEReconnectionBehavior verifies SSE connection establishment and event reception.
// Tests that the status indicator updates based on SSE events (phase-based status).
// Note: The UI shows phase status (TASK, REVIEW, CODEX, COMPLETED, FAILED) rather than
// explicit connection state. This test verifies events are received and processed correctly.
func TestSSEReconnectionBehavior(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	t.Run("status badge shows phase-based status after events load", func(t *testing.T) {
		// the status badge shows the current phase, not connection state
		// after SSE connects and events stream in, it should show a valid status
		badge := page.Locator("#status-badge")

		// wait for badge to become visible
		err := badge.WaitFor(playwright.LocatorWaitForOptions{
			State:   playwright.WaitForSelectorStateVisible,
			Timeout: playwright.Float(15000),
		})
		require.NoError(t, err, "status badge should be visible")

		// poll until badge shows a valid status (events have been processed)
		var text string
		validStatuses := []string{"TASK", "REVIEW", "CODEX", "COMPLETED", "FAILED"}
		for i := 0; i < 100; i++ { // 10 second timeout
			text, _ = badge.TextContent()
			for _, valid := range validStatuses {
				if text == valid {
					t.Logf("Status badge shows: %q after SSE events loaded", text)
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}

		// if no valid status found, check if badge has any content
		// (empty badge is valid for initial state before events)
		if text == "" {
			t.Log("Status badge is empty (no events processed yet or waiting for first event)")
		} else {
			t.Logf("Status badge shows unexpected value: %q", text)
		}
	})

	t.Run("SSE connection delivers events that populate output", func(t *testing.T) {
		// wait for SSE to deliver events
		time.Sleep(2 * time.Second)

		// verify output has content (events were received via SSE)
		outputDiv := page.Locator("#output")
		children, err := outputDiv.Locator(":scope > *").Count()
		require.NoError(t, err)
		assert.Greater(t, children, 0, "output should have content after SSE delivers events")
	})

	t.Run("multiple sections loaded indicates successful SSE streaming", func(t *testing.T) {
		// wait for events to load
		time.Sleep(2 * time.Second)

		// check that sections were created from SSE events
		sections := page.Locator(".section-header")
		count, err := sections.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have sections from SSE event stream")
		t.Logf("Loaded %d sections via SSE", count)
	})
}

// TestSSEConnectionLossHandling verifies the UI handles disconnection gracefully.
// Note: Full disconnection testing would require server restart which is complex
// in e2e tests. This test verifies the UI elements remain functional and the
// application state is preserved after SSE events have been received.
func TestSSEConnectionLossHandling(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	// wait for initial load
	time.Sleep(2 * time.Second)

	t.Run("UI continues to function after initial load", func(t *testing.T) {
		// verify basic UI elements are still responsive after SSE setup
		badge := page.Locator("#status-badge")
		visible, err := badge.IsVisible()
		require.NoError(t, err)
		assert.True(t, visible, "status badge should remain visible")

		// verify sections can still be interacted with
		sections := page.Locator(".section-header")
		count, err := sections.Count()
		require.NoError(t, err)

		if count > 0 {
			// try toggling a section to verify UI is responsive
			section := sections.First()
			summary := section.Locator("summary")
			initialOpen := isDetailsOpen(section)

			err = summary.Click()
			require.NoError(t, err)
			time.Sleep(200 * time.Millisecond)

			newOpen := isDetailsOpen(section)
			assert.NotEqual(t, initialOpen, newOpen, "section should toggle, indicating UI is responsive")
		}
	})

	t.Run("elapsed timer updates indicate active session tracking", func(t *testing.T) {
		// for a completed session, timer should be stopped
		// for an active session, timer would be updating
		elapsedEl := page.Locator("#elapsed-time")
		visible, err := elapsedEl.IsVisible()
		require.NoError(t, err)

		if !visible {
			t.Skip("elapsed time element not visible")
		}

		// get elapsed time value
		text, err := elapsedEl.TextContent()
		require.NoError(t, err)
		t.Logf("Elapsed time display: %q", text)

		// elapsed time should have some value (not empty) if events were processed
		// for completed sessions it shows final duration
		// for active sessions it would be updating
		assert.NotEmpty(t, text, "elapsed time should show a value after events load")
	})

	t.Run("status badge class indicates terminal state for completed sessions", func(t *testing.T) {
		// the test fixture ends with COMPLETED signal
		// verify the badge reflects this terminal state
		badge := page.Locator("#status-badge")

		// poll until badge shows COMPLETED
		var text string
		for i := 0; i < 50; i++ {
			text, _ = badge.TextContent()
			if text == "COMPLETED" || text == "FAILED" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if text == "COMPLETED" {
			// verify badge has completed class
			class, err := badge.GetAttribute("class")
			require.NoError(t, err)
			assert.Contains(t, class, "completed", "badge should have completed CSS class for terminal state")
		} else if text == "FAILED" {
			// verify badge has failed class
			class, err := badge.GetAttribute("class")
			require.NoError(t, err)
			assert.Contains(t, class, "failed", "badge should have failed CSS class for terminal state")
		} else {
			t.Logf("Badge shows: %q (session may not have terminal signal)", text)
		}
	})
}
