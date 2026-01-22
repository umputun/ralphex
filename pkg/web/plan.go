package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// TaskStatus represents the execution status of a task.
type TaskStatus string

// task status constants.
const (
	TaskStatusPending TaskStatus = "pending"
	TaskStatusActive  TaskStatus = "active"
	TaskStatusDone    TaskStatus = "done"
	TaskStatusFailed  TaskStatus = "failed"
)

// Checkbox represents a single checkbox item in a task.
type Checkbox struct {
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

// Task represents a task section in a plan.
type Task struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Status     TaskStatus `json:"status"`
	Checkboxes []Checkbox `json:"checkboxes"`
}

// Plan represents a parsed plan file.
type Plan struct {
	Title string `json:"title"`
	Tasks []Task `json:"tasks"`
}

// patterns for parsing plan markdown.
var (
	taskHeaderPattern = regexp.MustCompile(`^###\s+(?:Task|Iteration)\s+(\d+):\s*(.*)$`)
	checkboxPattern   = regexp.MustCompile(`^-\s+\[([ xX])\]\s*(.*)$`)
	titlePattern      = regexp.MustCompile(`^#\s+(.*)$`)
)

// ParsePlan parses a plan markdown file into a structured Plan.
func ParsePlan(content string) (*Plan, error) {
	plan := &Plan{
		Tasks: make([]Task, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentTask *Task

	for scanner.Scan() {
		line := scanner.Text()

		// check for plan title (first h1)
		if plan.Title == "" {
			if matches := titlePattern.FindStringSubmatch(line); matches != nil {
				plan.Title = strings.TrimSpace(matches[1])
				continue
			}
		}

		// check for task header
		if matches := taskHeaderPattern.FindStringSubmatch(line); matches != nil {
			// save previous task if exists
			if currentTask != nil {
				currentTask.Status = determineTaskStatus(currentTask.Checkboxes)
				plan.Tasks = append(plan.Tasks, *currentTask)
			}

			taskNum := 0
			if _, err := parseTaskNum(matches[1]); err == nil {
				taskNum, _ = parseTaskNum(matches[1])
			}

			currentTask = &Task{
				Number:     taskNum,
				Title:      strings.TrimSpace(matches[2]),
				Status:     TaskStatusPending,
				Checkboxes: make([]Checkbox, 0),
			}
			continue
		}

		// check for checkbox (only if inside a task)
		if currentTask != nil {
			if matches := checkboxPattern.FindStringSubmatch(line); matches != nil {
				checked := matches[1] == "x" || matches[1] == "X"
				currentTask.Checkboxes = append(currentTask.Checkboxes, Checkbox{
					Text:    strings.TrimSpace(matches[2]),
					Checked: checked,
				})
			}
		}
	}

	// save last task
	if currentTask != nil {
		currentTask.Status = determineTaskStatus(currentTask.Checkboxes)
		plan.Tasks = append(plan.Tasks, *currentTask)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan plan: %w", err)
	}

	return plan, nil
}

// ParsePlanFile reads and parses a plan file from disk.
func ParsePlanFile(path string) (*Plan, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path comes from server config
	if err != nil {
		return nil, fmt.Errorf("read plan file: %w", err)
	}
	return ParsePlan(string(content))
}

// JSON returns the plan as JSON bytes.
func (p *Plan) JSON() ([]byte, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal plan: %w", err)
	}
	return data, nil
}

// parseTaskNum extracts task number from string.
func parseTaskNum(s string) (int, error) {
	var num int
	if err := json.Unmarshal([]byte(s), &num); err != nil {
		return 0, fmt.Errorf("parse task number: %w", err)
	}
	return num, nil
}

// determineTaskStatus calculates task status based on checkbox states.
func determineTaskStatus(checkboxes []Checkbox) TaskStatus {
	if len(checkboxes) == 0 {
		return TaskStatusPending
	}

	checkedCount := 0
	for _, cb := range checkboxes {
		if cb.Checked {
			checkedCount++
		}
	}

	switch {
	case checkedCount == len(checkboxes):
		return TaskStatusDone
	case checkedCount > 0:
		return TaskStatusActive
	default:
		return TaskStatusPending
	}
}
