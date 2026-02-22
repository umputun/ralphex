package plan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/plan"
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
		p, err := plan.ParsePlan(content)
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
		p, err := plan.ParsePlan(content)
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
		p, err := plan.ParsePlan(content)
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
		p, err := plan.ParsePlan(content)
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
		p, err := plan.ParsePlan(content)
		require.NoError(t, err)

		require.Len(t, p.Tasks[0].Checkboxes, 2)
		assert.True(t, p.Tasks[0].Checkboxes[0].Checked)
		assert.True(t, p.Tasks[0].Checkboxes[1].Checked)
	})

	t.Run("handles plan without title", func(t *testing.T) {
		content := `### Task 1: No Title Plan

- [ ] Item
`
		p, err := plan.ParsePlan(content)
		require.NoError(t, err)

		assert.Empty(t, p.Title)
		require.Len(t, p.Tasks, 1)
	})

	t.Run("handles empty content", func(t *testing.T) {
		p, err := plan.ParsePlan("")
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
		p, err := plan.ParsePlan(content)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 1)
		require.Len(t, p.Tasks[0].Checkboxes, 1)
		assert.Equal(t, "Inside task", p.Tasks[0].Checkboxes[0].Text)
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
		p, err := plan.ParsePlan(content)
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
		p, err := plan.ParsePlan(content)
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
		p, err := plan.ParsePlan(content)
		require.NoError(t, err)

		require.Len(t, p.Tasks, 3)
		assert.Equal(t, 1, p.Tasks[0].Number)
		assert.Equal(t, 2, p.Tasks[1].Number)
		assert.Equal(t, 3, p.Tasks[2].Number)
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

		p, err := plan.ParsePlanFile(path)
		require.NoError(t, err)

		assert.Equal(t, "File Plan", p.Title)
		require.Len(t, p.Tasks, 1)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := plan.ParsePlanFile("/nonexistent/file.md")
		assert.Error(t, err)
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
