//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readFixture reads a fixture file from the testdata directory.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	testDataPath, err := resolveTestDataDir()
	require.NoError(t, err, "resolve test data dir")
	content, err := os.ReadFile(filepath.Join(testDataPath, name))
	require.NoError(t, err, "read fixture %s", name)
	return string(content)
}

// TestNewPlanModal tests the new plan modal open/close interactions.
func TestNewPlanModal(t *testing.T) {
	t.Run("opens on button click", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)

		waitVisible(t, page, "#new-plan-overlay.visible")
	})

	t.Run("opens with keyboard shortcut", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Keyboard().Press("n")
		require.NoError(t, err)

		waitVisible(t, page, "#new-plan-overlay.visible")
	})

	t.Run("closes on X button", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		err = page.Locator("#new-plan-close").Click()
		require.NoError(t, err)
		waitHidden(t, page, "#new-plan-overlay.visible")
	})

	t.Run("closes on cancel button", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		err = page.Locator("#new-plan-cancel").Click()
		require.NoError(t, err)
		waitHidden(t, page, "#new-plan-overlay.visible")
	})

	t.Run("closes on overlay click", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		// click the overlay background (not the modal itself)
		err = page.Locator("#new-plan-overlay").Click(
			playwright.LocatorClickOptions{Position: &playwright.Position{X: 5, Y: 5}})
		require.NoError(t, err)
		waitHidden(t, page, "#new-plan-overlay.visible")
	})

	t.Run("closes on Escape key", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		err = page.Keyboard().Press("Escape")
		require.NoError(t, err)
		waitHidden(t, page, "#new-plan-overlay.visible")
	})

	t.Run("has required elements", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		// verify all required form elements exist
		for _, selector := range []string{"#plan-dir", "#plan-description", "#new-plan-start"} {
			visible, err := page.Locator(selector).IsVisible()
			require.NoError(t, err, "check visibility of %s", selector)
			assert.True(t, visible, "%s should be visible", selector)
		}
	})
}

// TestNewPlanModalValidation tests form validation in the new plan modal.
func TestNewPlanModalValidation(t *testing.T) {
	t.Run("error when dir empty", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		// fill description but leave dir empty
		err = page.Locator("#plan-description").Fill("some plan description")
		require.NoError(t, err)

		err = page.Locator("#new-plan-start").Click()
		require.NoError(t, err)

		// error should be visible
		waitVisible(t, page, "#new-plan-error.visible")
	})

	t.Run("error when description empty", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		// fill dir but leave description empty
		err = page.Locator("#plan-dir").Fill("/tmp/some-project")
		require.NoError(t, err)

		err = page.Locator("#new-plan-start").Click()
		require.NoError(t, err)

		// error should be visible
		waitVisible(t, page, "#new-plan-error.visible")
	})

	t.Run("error clears on reopen", func(t *testing.T) {
		page := newPage(t)
		navigateToDashboard(t, page)

		// open modal and trigger an error
		err := page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		err = page.Locator("#new-plan-start").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-error.visible")

		// close and reopen
		err = page.Keyboard().Press("Escape")
		require.NoError(t, err)
		waitHidden(t, page, "#new-plan-overlay.visible")

		err = page.Locator("#new-plan-btn").Click()
		require.NoError(t, err)
		waitVisible(t, page, "#new-plan-overlay.visible")

		// error should be hidden and fields cleared
		waitHidden(t, page, "#new-plan-error.visible")

		dirVal, err := page.Locator("#plan-dir").InputValue()
		require.NoError(t, err)
		assert.Empty(t, dirVal, "dir input should be cleared on reopen")

		descVal, err := page.Locator("#plan-description").InputValue()
		require.NoError(t, err)
		assert.Empty(t, descVal, "description input should be cleared on reopen")
	})
}

// TestPlanSessionQAOutput tests that plan QA lines from progress files are consumed
// by the output parser and non-QA content lines are rendered normally.
// note: QA cards (.qa-card) require real SSE question/answer events from a running
// plan session, which isn't available in --serve --watch mode. Instead we verify
// the output parsing: QA lines (QUESTION:, OPTIONS:, ANSWER:, <<<RALPHEX:QUESTION>>>)
// are intercepted by tryHandlePlanQuestionFromOutput and hidden, while regular output
// lines like timestamps and status messages are rendered.
func TestPlanSessionQAOutput(t *testing.T) {
	// read the fixture content
	qaFixture := readFixture(t, "progress-plan-qa.txt")

	sessionName := "e2e-qa-output"
	createPlanSession(t, sessionName, qaFixture)

	page := newPage(t)
	navigateToDashboard(t, page)

	// navigate to the plan session
	clickSessionByName(t, page, "add authentication")

	// wait for plan mode to be applied
	waitForClass(t, page.Locator("body"), "plan-session")

	t.Run("renders non-QA output lines", func(t *testing.T) {
		// non-QA lines like "Starting plan creation" should be rendered
		outputLines := page.Locator(".output-line")
		waitForMinCount(t, outputLines, 1)

		count, err := outputLines.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should render at least one output line")
	})

	t.Run("hides raw QA lines from output", func(t *testing.T) {
		// after output loads, raw QUESTION:/OPTIONS:/ANSWER: lines should be consumed
		// by tryHandlePlanQuestionFromOutput and not visible as regular output
		outputLines := page.Locator(".output-line")
		waitForMinCount(t, outputLines, 1)

		count, err := outputLines.Count()
		require.NoError(t, err)

		for i := 0; i < count; i++ {
			text, err := outputLines.Nth(i).TextContent()
			if err != nil {
				continue
			}
			assert.NotRegexp(t, `(?i)^QUESTION:\s+`, text,
				"raw QUESTION: lines should be hidden in plan mode")
			assert.NotRegexp(t, `(?i)^OPTIONS:\s+`, text,
				"raw OPTIONS: lines should be hidden in plan mode")
			assert.NotRegexp(t, `(?i)^ANSWER:\s+`, text,
				"raw ANSWER: lines should be hidden in plan mode")
			assert.NotContains(t, text, "<<<RALPHEX:QUESTION>>>",
				"question block markers should be hidden in plan mode")
		}
	})

	t.Run("no docked question when all answered", func(t *testing.T) {
		// since all questions in the fixture have ANSWER: lines, no docked question
		// should remain visible after processing completes
		dock := page.Locator("#question-dock.visible")
		count, err := dock.Count()
		require.NoError(t, err)
		assert.Equal(t, 0, count, "docked question should not be visible when all questions are answered")
	})
}

// TestPlanSessionDockedQuestion tests the docked question placeholder for unanswered questions.
func TestPlanSessionDockedQuestion(t *testing.T) {
	// read the fixture content
	pendingFixture := readFixture(t, "progress-plan-pending.txt")

	sessionName := "e2e-docked-q"
	createPlanSession(t, sessionName, pendingFixture)

	page := newPage(t)
	navigateToDashboard(t, page)

	// navigate to the plan session
	clickSessionByName(t, page, "add health endpoint")

	// wait for plan mode to be applied
	waitForClass(t, page.Locator("body"), "plan-session")

	t.Run("shows docked question", func(t *testing.T) {
		waitVisible(t, page, "#question-dock.visible")
	})

	t.Run("shows question text", func(t *testing.T) {
		questionText := page.Locator(".question-dock-text")
		waitForMinCount(t, questionText, 1)

		text, err := questionText.First().TextContent()
		require.NoError(t, err)
		assert.Contains(t, text, "health endpoint include dependency checks",
			"docked question should show the question text")
	})

	t.Run("shows option buttons", func(t *testing.T) {
		options := page.Locator(".question-option")
		waitForMinCount(t, options, 3)

		count, err := options.Count()
		require.NoError(t, err)
		assert.Equal(t, 3, count, "should render 3 option buttons")

		// verify option text content
		for i, expected := range []string{"Yes, check all deps", "No, simple liveness only", "Configurable"} {
			text, err := options.Nth(i).TextContent()
			require.NoError(t, err)
			assert.Contains(t, text, expected, "option %d should contain expected text", i+1)
		}
	})

	t.Run("shows status message", func(t *testing.T) {
		statusEl := page.Locator(".question-dock-status")
		waitForMinCount(t, statusEl, 1)

		text, err := statusEl.First().TextContent()
		require.NoError(t, err)
		assert.NotEmpty(t, text, "status message should have text")
	})
}

// TestPlanSessionModeUI tests plan mode body class and UI adjustments.
func TestPlanSessionModeUI(t *testing.T) {
	qaFixture := readFixture(t, "progress-plan-qa.txt")

	sessionName := "e2e-plan-mode-ui"
	createPlanSession(t, sessionName, qaFixture)

	page := newPage(t)
	navigateToDashboard(t, page)

	clickSessionByName(t, page, "add authentication")
	waitForClass(t, page.Locator("body"), "plan-session")

	t.Run("plan session has body class", func(t *testing.T) {
		waitForClass(t, page.Locator("body"), "plan-session")
	})

	t.Run("plan panel collapsed in plan mode", func(t *testing.T) {
		waitForClass(t, page.Locator(".main-container"), "plan-collapsed")
	})

	t.Run("plan panel hidden in plan mode", func(t *testing.T) {
		visible, err := page.Locator(".plan-panel").IsVisible()
		require.NoError(t, err)
		assert.False(t, visible, "plan panel should be hidden in plan mode")
	})

	t.Run("phase nav hidden in plan mode", func(t *testing.T) {
		visible, err := page.Locator(".phase-nav").IsVisible()
		require.NoError(t, err)
		assert.False(t, visible, "phase nav should be hidden in plan mode")
	})

	t.Run("resume button element exists", func(t *testing.T) {
		btn := page.Locator("#plan-resume-btn")
		count, err := btn.Count()
		require.NoError(t, err)
		assert.Equal(t, 1, count, "resume button element should exist in DOM")
	})

	t.Run("plan iteration sections suppressed", func(t *testing.T) {
		// plan mode should suppress iteration section headers ("--- Plan iteration N ---")
		// the fixture has "--- Plan iteration 1 ---" which should not create a section
		planSections := page.Locator(".section-header[data-phase='plan']")
		count, err := planSections.Count()
		require.NoError(t, err)
		assert.Equal(t, 0, count, "plan iteration sections should be suppressed in plan mode")
	})

	t.Run("mode clears when switching to non-plan session", func(t *testing.T) {
		// click a non-plan session (the default test session derived from progress-test.txt)
		clickSessionByName(t, page, "test-plan")
		waitForClassGone(t, page.Locator("body"), "plan-session")
	})
}

// TestNewPlanModalElements tests additional form elements in the new plan modal.
func TestNewPlanModalElements(t *testing.T) {
	page := newPage(t)
	navigateToDashboard(t, page)

	err := page.Locator("#new-plan-btn").Click()
	require.NoError(t, err)
	waitVisible(t, page, "#new-plan-overlay.visible")

	t.Run("has directory dropdown", func(t *testing.T) {
		dropdown := page.Locator("#plan-dir-select")
		count, err := dropdown.Count()
		require.NoError(t, err)
		assert.Equal(t, 1, count, "directory dropdown should exist")
	})

	t.Run("has resumable sessions container", func(t *testing.T) {
		container := page.Locator("#resumable-sessions")
		count, err := container.Count()
		require.NoError(t, err)
		assert.Equal(t, 1, count, "resumable sessions container should exist")
	})

	t.Run("start button enabled by default", func(t *testing.T) {
		disabled, err := page.Locator("#new-plan-start").IsDisabled()
		require.NoError(t, err)
		assert.False(t, disabled, "start button should be enabled by default")
	})

	t.Run("description textarea accepts input", func(t *testing.T) {
		err := page.Locator("#plan-description").Fill("test plan description")
		require.NoError(t, err)
		waitInputValue(t, page.Locator("#plan-description"), "test plan description")
	})

	t.Run("directory input accepts input", func(t *testing.T) {
		err := page.Locator("#plan-dir").Fill("/tmp/test-project")
		require.NoError(t, err)
		waitInputValue(t, page.Locator("#plan-dir"), "/tmp/test-project")
	})
}

// TestDockedQuestionInteraction tests clicking and keyboard navigation on the docked question.
func TestDockedQuestionInteraction(t *testing.T) {
	pendingFixture := readFixture(t, "progress-plan-pending.txt")

	sessionName := "e2e-dock-interact"
	createPlanSession(t, sessionName, pendingFixture)

	page := newPage(t)
	navigateToDashboard(t, page)

	clickSessionByName(t, page, "add health endpoint")
	waitForClass(t, page.Locator("body"), "plan-session")
	waitVisible(t, page, "#question-dock.visible")

	t.Run("title shows Claude needs input", func(t *testing.T) {
		title := page.Locator(".question-dock-title")
		text, err := title.TextContent()
		require.NoError(t, err)
		assert.Contains(t, text, "Claude needs your input")
	})

	t.Run("option buttons have numbered keys", func(t *testing.T) {
		keys := page.Locator(".question-option .option-key")
		waitForMinCount(t, keys, 3)

		for i, expected := range []string{"1", "2", "3"} {
			text, err := keys.Nth(i).TextContent()
			require.NoError(t, err)
			assert.Equal(t, expected, text, "option key %d should show correct number", i+1)
		}
	})

	t.Run("arrow down navigates to next option", func(t *testing.T) {
		firstOption := page.Locator(".question-option").First()

		// focus first option
		err := firstOption.Focus()
		require.NoError(t, err)
		waitFocused(t, firstOption)

		// press arrow down to move to second option
		err = page.Keyboard().Press("ArrowDown")
		require.NoError(t, err)

		secondOption := page.Locator(".question-option").Nth(1)
		waitFocused(t, secondOption)
	})

	t.Run("arrow up navigates to previous option", func(t *testing.T) {
		secondOption := page.Locator(".question-option").Nth(1)

		err := secondOption.Focus()
		require.NoError(t, err)
		waitFocused(t, secondOption)

		err = page.Keyboard().Press("ArrowUp")
		require.NoError(t, err)

		firstOption := page.Locator(".question-option").First()
		waitFocused(t, firstOption)
	})

	t.Run("clicking option shows selected state and status update", func(t *testing.T) {
		// click the first option
		firstOption := page.Locator(".question-option").First()
		err := firstOption.Click()
		require.NoError(t, err)

		// submitAnswer re-renders dock with selectedAnswer and status text;
		// wait for the status to update (it becomes "Resuming session..." or an error)
		require.Eventually(t, func() bool {
			text, err := page.Locator(".question-dock-status").First().TextContent()
			return err == nil && text != "" && text != "Resume the session to answer this question."
		}, pollTimeout, pollInterval, "status should update after clicking option")

		// the re-rendered dock should show a selected option
		selectedOptions := page.Locator(".question-option.selected")
		count, err := selectedOptions.Count()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "should have a selected option after click")
	})
}

// TestDockedQuestionKeyboardSelection tests number key selection on the docked question.
func TestDockedQuestionKeyboardSelection(t *testing.T) {
	pendingFixture := readFixture(t, "progress-plan-pending.txt")

	sessionName := "e2e-dock-numkey"
	createPlanSession(t, sessionName, pendingFixture)

	page := newPage(t)
	navigateToDashboard(t, page)

	clickSessionByName(t, page, "add health endpoint")
	waitForClass(t, page.Locator("body"), "plan-session")
	waitVisible(t, page, "#question-dock.visible")

	t.Run("number key selects option", func(t *testing.T) {
		// press "2" to select second option ("No, simple liveness only")
		err := page.Keyboard().Press("2")
		require.NoError(t, err)

		// submitAnswer re-renders with selectedAnswer, check for selected option
		require.Eventually(t, func() bool {
			selected := page.Locator(".question-option.selected")
			count, err := selected.Count()
			return err == nil && count >= 1
		}, pollTimeout, pollInterval, "number key should select an option")
	})
}

// TestPlanReadySignal tests that PLAN_READY signal renders correctly.
func TestPlanReadySignal(t *testing.T) {
	content := `# Ralphex Progress Log
Plan: add feature
Branch: plan-feature
Mode: plan
Started: 2026-01-22 16:00:00
------------------------------------------------------------
[26-01-22 16:00:00] Starting plan creation

--- Plan iteration 1 ---
[26-01-22 16:00:01] Analyzing project
[26-01-22 16:00:10] Plan drafted
[26-01-22 16:00:20] Plan finalized <<<RALPHEX:PLAN_READY>>>
`
	sessionName := "e2e-plan-ready"
	createPlanSession(t, sessionName, content)

	page := newPage(t)
	navigateToDashboard(t, page)

	clickSessionByName(t, page, "add feature")

	t.Run("badge shows PLAN READY", func(t *testing.T) {
		badge := page.Locator("#status-badge")
		waitForTextContains(t, badge, "PLAN READY")
	})

	t.Run("badge has completed class", func(t *testing.T) {
		badge := page.Locator("#status-badge")
		waitForClass(t, badge, "completed")
	})

	t.Run("completion message rendered in output", func(t *testing.T) {
		outputLines := page.Locator(".output-line")
		waitForMinCount(t, outputLines, 1)

		count, err := outputLines.Count()
		require.NoError(t, err)

		var found bool
		for i := 0; i < count; i++ {
			text, err := outputLines.Nth(i).TextContent()
			if err != nil {
				continue
			}
			if assert.ObjectsAreEqual("plan ready", text) || contains(text, "plan ready") {
				found = true
				break
			}
		}
		assert.True(t, found, "output should contain 'plan ready' completion message")
	})
}

// contains checks if s contains substr (simple helper to avoid importing strings).
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
