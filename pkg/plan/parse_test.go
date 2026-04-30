package plan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/plan"
)

// mustResolve is a test helper that resolves pattern strings (preset names or raw regexes)
// to compiled regexes, failing the test on error.
func mustResolve(t *testing.T, patterns ...string) []*regexp.Regexp {
	t.Helper()
	res, err := plan.ResolveHeaderPatterns(patterns)
	require.NoError(t, err)
	return res
}

func TestParsePlan(t *testing.T) {
	t.Run("parses plan with title and tasks", func(t *testing.T) {
		content := `# My Test Plan

Some description here.

### Task 1: First Task

- [ ] Do something
- [x] Already done
- [ ] Another item

### Task 2: Second Task

- [ ] Task 2 item 1
- [ ] Task 2 item 2
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		assert.Equal(t, "My Test Plan", p.Title)
		require.Len(t, p.Tasks, 2)

		// task 1
		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, "First Task", p.Tasks[0].Title)
		assert.Equal(t, plan.TaskStatusActive, p.Tasks[0].Status) // has mix of checked/unchecked
		require.Len(t, p.Tasks[0].Checkboxes, 3)
		assert.False(t, p.Tasks[0].Checkboxes[0].Checked)
		assert.True(t, p.Tasks[0].Checkboxes[1].Checked)
		assert.False(t, p.Tasks[0].Checkboxes[2].Checked)

		// task 2
		assert.Equal(t, 2, p.Tasks[1].Number)
		assert.Equal(t, "Second Task", p.Tasks[1].Title)
		assert.Equal(t, plan.TaskStatusPending, p.Tasks[1].Status) // all unchecked
	})

	t.Run("parses iteration headers as tasks", func(t *testing.T) {
		content := `# Plan

### Iteration 1: First Iteration

- [ ] Item 1

### Iteration 2: Second Iteration

- [x] Item 2
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 2)
		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, "First Iteration", p.Tasks[0].Title)
		assert.Equal(t, plan.TaskStatusPending, p.Tasks[0].Status)

		assert.Equal(t, 2, p.Tasks[1].Number)
		assert.Equal(t, "Second Iteration", p.Tasks[1].Title)
		assert.Equal(t, plan.TaskStatusDone, p.Tasks[1].Status)
	})

	t.Run("parses completed tasks", func(t *testing.T) {
		content := `# Plan

### Task 1: Complete Task

- [x] Item 1
- [x] Item 2
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		assert.Equal(t, plan.TaskStatusDone, p.Tasks[0].Status)
	})

	t.Run("parses task with no checkboxes", func(t *testing.T) {
		content := `# Plan

### Task 1: Empty Task

Just some text, no checkboxes.

### Task 2: Has Items

- [ ] One item
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 2)
		assert.Equal(t, plan.TaskStatusPending, p.Tasks[0].Status)
		assert.Empty(t, p.Tasks[0].Checkboxes)
	})

	t.Run("handles uppercase X in checkbox", func(t *testing.T) {
		content := `# Plan

### Task 1: Test

- [X] Uppercase checked
- [x] Lowercase checked
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks[0].Checkboxes, 2)
		assert.True(t, p.Tasks[0].Checkboxes[0].Checked)
		assert.True(t, p.Tasks[0].Checkboxes[1].Checked)
	})

	t.Run("handles plan without title", func(t *testing.T) {
		content := `### Task 1: No Title Plan

- [ ] Item
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		assert.Empty(t, p.Title)
		require.Len(t, p.Tasks, 1)
	})

	t.Run("handles empty content", func(t *testing.T) {
		p, err := plan.ParsePlan("", nil)
		require.NoError(t, err)

		assert.Empty(t, p.Title)
		assert.Empty(t, p.Tasks)
	})

	t.Run("ignores checkboxes outside tasks", func(t *testing.T) {
		content := `# Plan

- [ ] This is outside any task

### Task 1: First

- [ ] Inside task
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
		assert.Equal(t, "Inside task", p.Tasks[0].Checkboxes[0].Text)
	})

	t.Run("ignores checkboxes in Success criteria after Task sections", func(t *testing.T) {
		content := `# Plan

### Task 1: First

- [x] done

## Success criteria

- [ ] Manual: run e2e test
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		assert.Equal(t, plan.TaskStatusDone, p.Tasks[0].Status)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
		assert.Equal(t, "done", p.Tasks[0].Checkboxes[0].Text)
	})

	t.Run("closes task on # Overview when title already set", func(t *testing.T) {
		content := `# Plan

### Task 1: First

- [x] done

# Overview

- [ ] Manual: verify
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		assert.Equal(t, plan.TaskStatusDone, p.Tasks[0].Status)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
		assert.Equal(t, "done", p.Tasks[0].Checkboxes[0].Text)
	})

	t.Run("does not close task on #### subsection under task", func(t *testing.T) {
		content := `# Plan

### Task 1: First

- [ ] main item

#### Subsection

- [ ] sub item
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		require.Len(t, p.Tasks[0].Checkboxes, 2)
		assert.Equal(t, "main item", p.Tasks[0].Checkboxes[0].Text)
		assert.Equal(t, "sub item", p.Tasks[0].Checkboxes[1].Text)
	})

	t.Run("parses non-integer task headers", func(t *testing.T) {
		content := `# Plan with inserted tasks

### Task 1: First Task

- [x] Done

### Task 2: Second Task

- [x] Done

### Task 2.5: Inserted Task

- [ ] New item

### Task 3: Third Task

- [ ] Item
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 4)

		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, "First Task", p.Tasks[0].Title)

		assert.Equal(t, 2, p.Tasks[1].Number)
		assert.Equal(t, "Second Task", p.Tasks[1].Title)

		assert.Equal(t, 0, p.Tasks[2].Number) // non-integer gets Number=0
		assert.Equal(t, "Inserted Task", p.Tasks[2].Title)
		assert.Equal(t, plan.TaskStatusPending, p.Tasks[2].Status)

		assert.Equal(t, 3, p.Tasks[3].Number)
		assert.Equal(t, "Third Task", p.Tasks[3].Title)
	})

	t.Run("parses alphanumeric task headers", func(t *testing.T) {
		content := `# Plan

### Task 2a: Alpha Task

- [ ] Item
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		assert.Equal(t, 0, p.Tasks[0].Number) // non-integer gets Number=0
		assert.Equal(t, "Alpha Task", p.Tasks[0].Title)
	})

	t.Run("backward compat with integer task headers", func(t *testing.T) {
		content := `# Plan

### Task 1: First
- [ ] A

### Task 2: Second
- [x] B

### Task 3: Third
- [ ] C
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 3)
		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, 2, p.Tasks[1].Number)
		assert.Equal(t, 3, p.Tasks[2].Number)
	})

	t.Run("parses indented checkboxes as sub-items", func(t *testing.T) {
		content := `# Plan

### Task 1: Add tests

- [ ] Add comprehensive tests
  - [ ] Unit tests for handler
  - [ ] Integration tests
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		require.Len(t, p.Tasks[0].Checkboxes, 3)
		assert.Equal(t, "Add comprehensive tests", p.Tasks[0].Checkboxes[0].Text)
		assert.Equal(t, "Unit tests for handler", p.Tasks[0].Checkboxes[1].Text)
		assert.Equal(t, "Integration tests", p.Tasks[0].Checkboxes[2].Text)
	})

	t.Run("HasUncompletedActionableWork ignores description checkboxes", func(t *testing.T) {
		content := `# Plan

### Task 1: Format example

- [x] Faulti format
- [ ] use this format for [ ] unchecked items
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		require.Len(t, p.Tasks[0].Checkboxes, 2)
		// first actionable (done), second is description (text contains [ ]) — no actionable uncompleted
		assert.False(t, p.Tasks[0].HasUncompletedActionableWork())
	})

	t.Run("HasUncompletedActionableWork returns true when actionable unchecked", func(t *testing.T) {
		content := `# Plan

### Task 1: Mixed

- [ ] Create HashPassword
- [ ] use [ ] for format example
`
		p, err := plan.ParsePlan(content, nil)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		assert.True(t, p.Tasks[0].HasUncompletedActionableWork())
	})
}

func TestParsePlan_CustomPatterns(t *testing.T) {
	t.Run("OpenSpec-style ## N. Phase headers", func(t *testing.T) {
		content := `# OpenSpec Plan

## 1. Phase One

- [ ] 1.1 First item
- [x] 1.2 Second item

## 2. Phase Two

- [ ] 2.1 Another item
`
		p, err := plan.ParsePlan(content, mustResolve(t, "openspec"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 2)
		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, "Phase One", p.Tasks[0].Title)
		require.Len(t, p.Tasks[0].Checkboxes, 2)
		assert.Equal(t, "1.1 First item", p.Tasks[0].Checkboxes[0].Text)
		assert.False(t, p.Tasks[0].Checkboxes[0].Checked)
		assert.True(t, p.Tasks[0].Checkboxes[1].Checked)
		assert.Equal(t, plan.TaskStatusActive, p.Tasks[0].Status)

		assert.Equal(t, 2, p.Tasks[1].Number)
		assert.Equal(t, "Phase Two", p.Tasks[1].Title)
		require.Len(t, p.Tasks[1].Checkboxes, 1)
	})

	t.Run("mixed patterns parse in document order", func(t *testing.T) {
		content := `# Mixed

### Task 1: First

- [ ] A

## 2. Phase Two

- [ ] B

### Iteration 3: Third

- [x] C
`
		p, err := plan.ParsePlan(content, mustResolve(t, "default", "openspec"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 3)
		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, "First", p.Tasks[0].Title)
		assert.Equal(t, 2, p.Tasks[1].Number)
		assert.Equal(t, "Phase Two", p.Tasks[1].Title)
		assert.Equal(t, 3, p.Tasks[2].Number)
		assert.Equal(t, "Third", p.Tasks[2].Title)
	})

	t.Run("non-matching h2 closes current task (unchanged)", func(t *testing.T) {
		// with only ### Task patterns configured, a plain ## header
		// that doesn't match closes the current task.
		content := `# Plan

### Task 1: First

- [x] done

## Success criteria

- [ ] outside
`
		p, err := plan.ParsePlan(content, mustResolve(t, "default"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		assert.Equal(t, plan.TaskStatusDone, p.Tasks[0].Status)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
	})

	t.Run("non-matching h1 after title closes current task (unchanged)", func(t *testing.T) {
		content := `# Plan

### Task 1: First

- [x] done

# Overview

- [ ] outside
`
		p, err := plan.ParsePlan(content, mustResolve(t, "default"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		assert.Equal(t, plan.TaskStatusDone, p.Tasks[0].Status)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
	})

	t.Run("non-matching h3 does NOT close current task (unchanged)", func(t *testing.T) {
		// free-form ### sub-note inside a task should not orphan checkboxes.
		content := `# Plan

### Task 1: First

- [ ] main

### A note (not a task)

- [ ] still inside task
`
		p, err := plan.ParsePlan(content, mustResolve(t, "default"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		require.Len(t, p.Tasks[0].Checkboxes, 2)
		assert.Equal(t, "main", p.Tasks[0].Checkboxes[0].Text)
		assert.Equal(t, "still inside task", p.Tasks[0].Checkboxes[1].Text)
	})

	t.Run("matching custom h2 closes preceding h3 task and opens new task", func(t *testing.T) {
		content := `# Plan

### Task 1: First

- [x] done

## 2. Phase Two

- [ ] phase item
`
		p, err := plan.ParsePlan(content, mustResolve(t, "default", "openspec"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 2)
		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, "First", p.Tasks[0].Title)
		assert.Equal(t, plan.TaskStatusDone, p.Tasks[0].Status)

		assert.Equal(t, 2, p.Tasks[1].Number)
		assert.Equal(t, "Phase Two", p.Tasks[1].Title)
		require.Len(t, p.Tasks[1].Checkboxes, 1)
		assert.Equal(t, "phase item", p.Tasks[1].Checkboxes[0].Text)
	})

	t.Run("invalid raw regex surfaces compile error via resolver", func(t *testing.T) {
		_, err := plan.ResolveHeaderPattern(`^(unclosed`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "compile task header pattern")
	})

	t.Run("plan with matching headers but no checkboxes yields zero-checkbox tasks", func(t *testing.T) {
		content := `# Plan

## 1. Phase One

just prose, no checkboxes.

## 2. Phase Two

also no checkboxes.
`
		p, err := plan.ParsePlan(content, mustResolve(t, "openspec"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 2)
		assert.Empty(t, p.Tasks[0].Checkboxes)
		assert.Empty(t, p.Tasks[1].Checkboxes)
		assert.Equal(t, plan.TaskStatusPending, p.Tasks[0].Status)
	})

	t.Run("H1 task template does not lose first task to title capture", func(t *testing.T) {
		// custom template using a single hash header; the first line must be
		// captured as a task, not silently consumed as the plan title.
		content := `# 1. First Phase

- [ ] first item

# 2. Second Phase

- [ ] second item
`
			p, err := plan.ParsePlan(content, mustResolve(t, `^# (\d+)\.\s*(.*)$`))
			require.NoError(t, err)
			assert.Empty(t, p.Title, "first H1 must not be consumed as plan title when it matches a task template")
			require.Len(t, p.Tasks, 2)
			assert.Equal(t, 1, p.Tasks[0].Number)
			assert.Equal(t, "First Phase", p.Tasks[0].Title)
			require.Len(t, p.Tasks[0].Checkboxes, 1)
			assert.Equal(t, 2, p.Tasks[1].Number)
			assert.Equal(t, "Second Phase", p.Tasks[1].Title)
	})

	t.Run("H1 task template closes preceding task on later non-task H1", func(t *testing.T) {
		// with a custom H1 task template and no separate plan title,
		// a later non-task "# Section" must close the current task
		// instead of being swallowed as the plan title while checkboxes
		// below silently attach to the preceding task.
		content := `# 1. First Phase

- [ ] first item

# Overview

- [ ] outside
`
		p, err := plan.ParsePlan(content, mustResolve(t, `^# (\d+)\.\s*(.*)$`))
		require.NoError(t, err)
		require.Len(t, p.Tasks, 1)
		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, "First Phase", p.Tasks[0].Title)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
		assert.Equal(t, "first item", p.Tasks[0].Checkboxes[0].Text)
	})

	t.Run("default patterns still capture H1 plan title", func(t *testing.T) {
		// default templates (### Task/Iteration) do not match "# Title", so the
		// first H1 must still be captured as the plan title.
		content := `# Plan Title

### Task 1: Do Stuff

- [ ] item
`
			p, err := plan.ParsePlan(content, nil)
			require.NoError(t, err)
			assert.Equal(t, "Plan Title", p.Title)
			require.Len(t, p.Tasks, 1)
			assert.Equal(t, "Do Stuff", p.Tasks[0].Title)
	})

	t.Run("H1 task template: later ## subsection does NOT close task", func(t *testing.T) {
		// with H1-level task headers, a ## subsection inside the task must remain
		// attached (it's deeper than the task heading, so it's a sub-note).
		content := `# 1. First Phase

- [ ] main item

## Details

- [ ] sub item

# 2. Second Phase

- [ ] second main
`
		p, err := plan.ParsePlan(content, mustResolve(t, `^# (\d+)\.\s*(.*)$`))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 2)
		assert.Equal(t, 1, p.Tasks[0].Number)
		require.Len(t, p.Tasks[0].Checkboxes, 2)
		assert.Equal(t, "main item", p.Tasks[0].Checkboxes[0].Text)
		assert.Equal(t, "sub item", p.Tasks[0].Checkboxes[1].Text)

		assert.Equal(t, 2, p.Tasks[1].Number)
		require.Len(t, p.Tasks[1].Checkboxes, 1)
	})

	t.Run("H4 task template: higher-level ### section closes task", func(t *testing.T) {
		// with H4-level task headers (deeper than the default), a shallower ###
		// section must still close the task so its checkboxes don't leak.
		content := `# Plan

#### 1. Deep Task

- [ ] item inside task

### Sibling Section

- [ ] outside task
`
		p, err := plan.ParsePlan(content, mustResolve(t, `^#### (\d+)\.\s*(.*)$`))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
		assert.Equal(t, "item inside task", p.Tasks[0].Checkboxes[0].Text)
	})

	t.Run("H2 task template: ### sub-note stays inside task", func(t *testing.T) {
		// for a ## task, a deeper ### heading is a sub-note and must NOT close
		// the task (regression guard: any sibling-same-level logic must not fire here).
		content := `# Plan

## 1. Phase One

- [ ] main item

### Notes

- [ ] still inside task

## 2. Phase Two

- [ ] second
`
		p, err := plan.ParsePlan(content, mustResolve(t, "openspec"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 2)
		require.Len(t, p.Tasks[0].Checkboxes, 2)
		assert.Equal(t, "main item", p.Tasks[0].Checkboxes[0].Text)
		assert.Equal(t, "still inside task", p.Tasks[0].Checkboxes[1].Text)
		require.Len(t, p.Tasks[1].Checkboxes, 1)
	})

	t.Run("ParsePlanFile accepts openspec preset patterns", func(t *testing.T) {
		content := `# Plan

## 1. Phase One

- [ ] 1.1 item
`
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "plan.md")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		p, err := plan.ParsePlanFile(path, mustResolve(t, "openspec"))
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		assert.Equal(t, "Phase One", p.Tasks[0].Title)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
	})
}

func TestParsePlanFile(t *testing.T) {
	t.Run("reads and parses file", func(t *testing.T) {
		content := `# File Plan

### Task 1: File Task

- [ ] File item
`
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "test-plan.md")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		p, err := plan.ParsePlanFile(path, nil)
		require.NoError(t, err)

		assert.Equal(t, "File Plan", p.Title)
		require.Len(t, p.Tasks, 1)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := plan.ParsePlanFile("/nonexistent/file.md", nil)
		assert.Error(t, err)
	})
}

func TestFileHasUncompletedCheckbox(t *testing.T) {
	t.Run("returns true when file has uncompleted checkbox", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "plan.md")
		require.NoError(t, os.WriteFile(path, []byte("# Plan\n- [ ] todo\n- [x] done"), 0o600))

		has, err := plan.FileHasUncompletedCheckbox(path)
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns false when all checkboxes completed", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "plan.md")
		require.NoError(t, os.WriteFile(path, []byte("# Plan\n- [x] done"), 0o600))

		has, err := plan.FileHasUncompletedCheckbox(path)
		require.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := plan.FileHasUncompletedCheckbox("/nonexistent/file.md")
		assert.Error(t, err)
	})

	t.Run("returns false when only format-description checkboxes unchecked", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "plan.md")
		content := "# Plan\n- [ ] use this format for [ ] unchecked items\n- [ ] example with [x] in text"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		has, err := plan.FileHasUncompletedCheckbox(path)
		require.NoError(t, err)
		assert.False(t, has)
	})
}

func TestPlan_JSON(t *testing.T) {
	p := &plan.Plan{
		Title: "Test Plan",
		Tasks: []plan.Task{
			{
				Number: 1,
				Title:  "First Task",
				Status: plan.TaskStatusPending,
				Checkboxes: []plan.Checkbox{
					{Text: "Item 1", Checked: false},
					{Text: "Item 2", Checked: true},
				},
			},
		},
	}

	data, err := p.JSON()
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, "Test Plan", decoded["title"])
	tasks := decoded["tasks"].([]any)
	require.Len(t, tasks, 1)
}

func TestDetermineTaskStatus(t *testing.T) {
	tests := []struct {
		name       string
		checkboxes []plan.Checkbox
		want       plan.TaskStatus
	}{
		{"empty", nil, plan.TaskStatusPending},
		{"all unchecked", []plan.Checkbox{{Checked: false}, {Checked: false}}, plan.TaskStatusPending},
		{"all checked", []plan.Checkbox{{Checked: true}, {Checked: true}}, plan.TaskStatusDone},
		{"mixed", []plan.Checkbox{{Checked: true}, {Checked: false}}, plan.TaskStatusActive},
		{"single checked", []plan.Checkbox{{Checked: true}}, plan.TaskStatusDone},
		{"single unchecked", []plan.Checkbox{{Checked: false}}, plan.TaskStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := plan.DetermineTaskStatus(tt.checkboxes)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTaskStatus_Constants(t *testing.T) {
	// verify status values for API stability
	assert.Equal(t, plan.TaskStatusPending, plan.TaskStatus("pending"))
	assert.Equal(t, plan.TaskStatusActive, plan.TaskStatus("active"))
	assert.Equal(t, plan.TaskStatusDone, plan.TaskStatus("done"))
	assert.Equal(t, plan.TaskStatusFailed, plan.TaskStatus("failed"))
}
