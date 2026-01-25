//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
