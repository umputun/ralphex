package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		plan, err := ParsePlan(content)
		require.NoError(t, err)

		assert.Equal(t, "My Test Plan", plan.Title)
		require.Len(t, plan.Tasks, 2)

		// task 1
		assert.Equal(t, 1, plan.Tasks[0].Number)
		assert.Equal(t, "First Task", plan.Tasks[0].Title)
		assert.Equal(t, TaskStatusActive, plan.Tasks[0].Status) // has mix of checked/unchecked
		require.Len(t, plan.Tasks[0].Checkboxes, 3)
		assert.False(t, plan.Tasks[0].Checkboxes[0].Checked)
		assert.True(t, plan.Tasks[0].Checkboxes[1].Checked)
		assert.False(t, plan.Tasks[0].Checkboxes[2].Checked)

		// task 2
		assert.Equal(t, 2, plan.Tasks[1].Number)
		assert.Equal(t, "Second Task", plan.Tasks[1].Title)
		assert.Equal(t, TaskStatusPending, plan.Tasks[1].Status) // all unchecked
	})

	t.Run("parses iteration headers as tasks", func(t *testing.T) {
		content := `# Plan

### Iteration 1: First Iteration

- [ ] Item 1

### Iteration 2: Second Iteration

- [x] Item 2
`
		plan, err := ParsePlan(content)
		require.NoError(t, err)

		require.Len(t, plan.Tasks, 2)
		assert.Equal(t, 1, plan.Tasks[0].Number)
		assert.Equal(t, "First Iteration", plan.Tasks[0].Title)
		assert.Equal(t, TaskStatusPending, plan.Tasks[0].Status)

		assert.Equal(t, 2, plan.Tasks[1].Number)
		assert.Equal(t, "Second Iteration", plan.Tasks[1].Title)
		assert.Equal(t, TaskStatusDone, plan.Tasks[1].Status)
	})

	t.Run("parses completed tasks", func(t *testing.T) {
		content := `# Plan

### Task 1: Complete Task

- [x] Item 1
- [x] Item 2
`
		plan, err := ParsePlan(content)
		require.NoError(t, err)

		require.Len(t, plan.Tasks, 1)
		assert.Equal(t, TaskStatusDone, plan.Tasks[0].Status)
	})

	t.Run("parses task with no checkboxes", func(t *testing.T) {
		content := `# Plan

### Task 1: Empty Task

Just some text, no checkboxes.

### Task 2: Has Items

- [ ] One item
`
		plan, err := ParsePlan(content)
		require.NoError(t, err)

		require.Len(t, plan.Tasks, 2)
		assert.Equal(t, TaskStatusPending, plan.Tasks[0].Status)
		assert.Empty(t, plan.Tasks[0].Checkboxes)
	})

	t.Run("handles uppercase X in checkbox", func(t *testing.T) {
		content := `# Plan

### Task 1: Test

- [X] Uppercase checked
- [x] Lowercase checked
`
		plan, err := ParsePlan(content)
		require.NoError(t, err)

		require.Len(t, plan.Tasks[0].Checkboxes, 2)
		assert.True(t, plan.Tasks[0].Checkboxes[0].Checked)
		assert.True(t, plan.Tasks[0].Checkboxes[1].Checked)
	})

	t.Run("handles plan without title", func(t *testing.T) {
		content := `### Task 1: No Title Plan

- [ ] Item
`
		plan, err := ParsePlan(content)
		require.NoError(t, err)

		assert.Empty(t, plan.Title)
		require.Len(t, plan.Tasks, 1)
	})

	t.Run("handles empty content", func(t *testing.T) {
		plan, err := ParsePlan("")
		require.NoError(t, err)

		assert.Empty(t, plan.Title)
		assert.Empty(t, plan.Tasks)
	})

	t.Run("ignores checkboxes outside tasks", func(t *testing.T) {
		content := `# Plan

- [ ] This is outside any task

### Task 1: First

- [ ] Inside task
`
		plan, err := ParsePlan(content)
		require.NoError(t, err)

		require.Len(t, plan.Tasks, 1)
		require.Len(t, plan.Tasks[0].Checkboxes, 1)
		assert.Equal(t, "Inside task", plan.Tasks[0].Checkboxes[0].Text)
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

		plan, err := ParsePlanFile(path)
		require.NoError(t, err)

		assert.Equal(t, "File Plan", plan.Title)
		require.Len(t, plan.Tasks, 1)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := ParsePlanFile("/nonexistent/file.md")
		assert.Error(t, err)
	})
}

func TestPlan_JSON(t *testing.T) {
	plan := &Plan{
		Title: "Test Plan",
		Tasks: []Task{
			{
				Number: 1,
				Title:  "First Task",
				Status: TaskStatusPending,
				Checkboxes: []Checkbox{
					{Text: "Item 1", Checked: false},
					{Text: "Item 2", Checked: true},
				},
			},
		},
	}

	data, err := plan.JSON()
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
		checkboxes []Checkbox
		want       TaskStatus
	}{
		{"empty", nil, TaskStatusPending},
		{"all unchecked", []Checkbox{{Checked: false}, {Checked: false}}, TaskStatusPending},
		{"all checked", []Checkbox{{Checked: true}, {Checked: true}}, TaskStatusDone},
		{"mixed", []Checkbox{{Checked: true}, {Checked: false}}, TaskStatusActive},
		{"single checked", []Checkbox{{Checked: true}}, TaskStatusDone},
		{"single unchecked", []Checkbox{{Checked: false}}, TaskStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineTaskStatus(tt.checkboxes)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTaskStatus_Constants(t *testing.T) {
	// verify status values for API stability
	assert.Equal(t, TaskStatusPending, TaskStatus("pending"))
	assert.Equal(t, TaskStatusActive, TaskStatus("active"))
	assert.Equal(t, TaskStatusDone, TaskStatus("done"))
	assert.Equal(t, TaskStatusFailed, TaskStatus("failed"))
}
