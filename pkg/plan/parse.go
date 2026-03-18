package plan

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
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
	taskHeaderPattern = regexp.MustCompile(`^###\s+(?:Task|Iteration)\s+([^:]+?):\s*(.*)$`)
	// allow leading whitespace for indented sub-items (e.g. "  - [ ] Unit tests")
	checkboxPattern = regexp.MustCompile(`^\s*-\s+\[([ xX])\]\s*(.*)$`)
	titlePattern    = regexp.MustCompile(`^#\s+(.*)$`)
	// formatInText matches [ ] or [x] in checkbox text — description/example, not actionable for completion check.
	formatInText = regexp.MustCompile(`\[\s*[ xX]?\s*\]`)
)

// ParsePlan parses plan markdown content into a structured Plan.
func ParsePlan(content string) (*Plan, error) {
	p := &Plan{
		Tasks: make([]Task, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentTask *Task

	for scanner.Scan() {
		line := scanner.Text()

		// check for plan title (first h1)
		if p.Title == "" {
			if matches := titlePattern.FindStringSubmatch(line); matches != nil {
				p.Title = strings.TrimSpace(matches[1])
				continue
			}
		}

		// check for task header
		if matches := taskHeaderPattern.FindStringSubmatch(line); matches != nil {
			// save previous task if exists
			if currentTask != nil {
				currentTask.Status = DetermineTaskStatus(currentTask.Checkboxes)
				p.Tasks = append(p.Tasks, *currentTask)
			}

			taskNum := parseTaskNum(matches[1])

			currentTask = &Task{
				Number:     taskNum,
				Title:      strings.TrimSpace(matches[2]),
				Status:     TaskStatusPending,
				Checkboxes: make([]Checkbox, 0),
			}
			continue
		}

		// non-Task section header (e.g. ## Success criteria, ## Overview, ## Context):
		// close current task so checkboxes below are not attached to it.
		// only ## (h2) closes; ### and #### are subsections and must not orphan checkboxes.
		// also close on # (h1) when title already set, e.g. # Overview in plans using single hash for sections.
		isH2 := strings.HasPrefix(line, "##") && !strings.HasPrefix(line, "###")
		isH1AfterTitle := strings.HasPrefix(line, "#") && p.Title != "" && !strings.HasPrefix(line, "##")
		if currentTask != nil && (isH2 || isH1AfterTitle) && !taskHeaderPattern.MatchString(line) {
			currentTask.Status = DetermineTaskStatus(currentTask.Checkboxes)
			p.Tasks = append(p.Tasks, *currentTask)
			currentTask = nil
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
		currentTask.Status = DetermineTaskStatus(currentTask.Checkboxes)
		p.Tasks = append(p.Tasks, *currentTask)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan plan: %w", err)
	}

	return p, nil
}

// ParsePlanFile reads and parses a plan file from disk.
func ParsePlanFile(path string) (*Plan, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path is internally resolved, not from user input
	if err != nil {
		return nil, fmt.Errorf("read plan file: %w", err)
	}
	return ParsePlan(string(content))
}

// FileHasUncompletedCheckbox returns true if the file contains any uncompleted actionable checkbox (- [ ]).
// used for malformed plans (no task headers) to avoid treating them as complete.
// ignores format-description checkboxes (text containing [ ] or [x]) to match HasUncompletedActionableWork behavior.
func FileHasUncompletedCheckbox(path string) (bool, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path is internally resolved
	if err != nil {
		return false, fmt.Errorf("read plan file: %w", err)
	}
	// scan lines for uncompleted checkboxes; only count actionable ones (text without [ ] or [x])
	for line := range strings.SplitSeq(string(content), "\n") {
		matches := checkboxPattern.FindStringSubmatch(line)
		if len(matches) < 3 || matches[1] == "x" || matches[1] == "X" {
			continue
		}
		text := strings.TrimSpace(matches[2])
		if formatInText.MatchString(text) {
			continue // format description, not actionable
		}
		return true, nil
	}
	return false, nil
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
// returns 0 for non-integer values (e.g. "2.5", "2a").
func parseTaskNum(s string) int {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// IsActionable returns false if checkbox text contains format pattern [ ] or [x] —
// such checkboxes are description/examples, ignored for completion check.
func (cb Checkbox) IsActionable() bool {
	return !formatInText.MatchString(cb.Text)
}

// HasUncompletedActionableWork returns true if the task has any unchecked actionable checkbox.
// checkboxes whose text contains [ ] or [x] (format description) are ignored.
func (t *Task) HasUncompletedActionableWork() bool {
	for _, cb := range t.Checkboxes {
		if !cb.Checked && cb.IsActionable() {
			return true
		}
	}
	return false
}

// DetermineTaskStatus calculates task status based on checkbox states.
func DetermineTaskStatus(checkboxes []Checkbox) TaskStatus {
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
