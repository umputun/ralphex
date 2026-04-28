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
	// allow leading whitespace for indented sub-items (e.g. "  - [ ] Unit tests")
	checkboxPattern = regexp.MustCompile(`^\s*-\s+\[([ xX])\]\s*(.*)$`)
	titlePattern    = regexp.MustCompile(`^#\s+(.*)$`)
	// formatInText matches [ ] or [x] in checkbox text — description/example, not actionable for completion check.
	formatInText = regexp.MustCompile(`\[\s*[ xX]?\s*\]`)
)

// matchTaskHeader tries each compiled pattern in order and returns the
// (taskNum, title) capture groups for the first match. ok=false if no pattern matched.
func matchTaskHeader(line string, compiled []*regexp.Regexp) (taskID, title string, ok bool) {
	for _, re := range compiled {
		matches := re.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		id := ""
		if len(matches) > 1 {
			id = matches[1]
		}
		t := ""
		if len(matches) > 2 {
			t = matches[2]
		}
		return id, t, true
	}
	return "", "", false
}

// headingLevel returns the number of leading '#' characters on a line, or 0 if
// the line is not a markdown heading. a line starting with '#' is considered a
// heading regardless of whether a space follows (matching the legacy behavior
// that used strings.HasPrefix without whitespace checks).
func headingLevel(line string) int {
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	return i
}

// closesTask reports whether a non-task-matching heading at lineLevel should
// close a task opened at taskLevel. strictly shallower headings always close
// (they start a new top-level section). same-level headings close only when
// the task lives at the top of the document tree (level 1 or 2), because at
// those levels a same-level heading is a sibling section, not a sub-note; at
// deeper levels a same-level non-matching heading is treated as a note inside
// the task so its checkboxes remain attached. see parse_test.go cases
// "non-matching h3 does NOT close current task" and
// "H1 task template closes preceding task on later non-task H1".
func closesTask(lineLevel, taskLevel int) bool {
	if lineLevel <= 0 || taskLevel <= 0 {
		return false
	}
	if lineLevel < taskLevel {
		return true
	}
	return lineLevel == taskLevel && taskLevel <= 2
}

// ParsePlan parses plan markdown content into a structured Plan.
// patterns is an optional variadic list of task-header templates (e.g.
// "### Task {N}: {title}"). If empty, DefaultTaskHeaderPatterns is used.
func ParsePlan(content string, patterns ...string) (*Plan, error) {
	compiled, err := CompileTaskHeaderPatterns(patterns)
	if err != nil {
		return nil, fmt.Errorf("compile task header patterns: %w", err)
	}

	p := &Plan{
		Tasks: make([]Task, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentTask *Task
	currentTaskLevel := 0 // heading level (# count) of the currently open task

	for scanner.Scan() {
		line := scanner.Text()
		level := headingLevel(line)

		// check for task header first (first match wins across configured patterns).
		// runs before the H1 title capture so a custom H1 task template like
		// "# {N}. {title}" is not silently consumed as the plan title.
		if id, title, matched := matchTaskHeader(line, compiled); matched {
			// save previous task if exists
			if currentTask != nil {
				currentTask.Status = DetermineTaskStatus(currentTask.Checkboxes)
				p.Tasks = append(p.Tasks, *currentTask)
			}

			currentTask = &Task{
				Number:     parseTaskNum(id),
				Title:      strings.TrimSpace(title),
				Status:     TaskStatusPending,
				Checkboxes: make([]Checkbox, 0),
			}
			currentTaskLevel = level
			continue
		}

		// non-task heading: close the current task when it starts a new section at
		// a shallower or sibling-top-level position (see closesTask). this lets
		// deeper headings (e.g. #### inside a ### task) stay attached to the task
		// as sub-notes, while a ## Success criteria or # Overview still closes a
		// ### task, and a ## Phase task is not prematurely closed by a ### note.
		if currentTask != nil && closesTask(level, currentTaskLevel) {
			currentTask.Status = DetermineTaskStatus(currentTask.Checkboxes)
			p.Tasks = append(p.Tasks, *currentTask)
			currentTask = nil
			currentTaskLevel = 0
			continue
		}

		// check for plan title (first h1) — only when no task header matched above
		// and no task is open, so H1-style task templates aren't swallowed here and
		// a later # Section doesn't retroactively become the plan title.
		if p.Title == "" && level == 1 {
			if matches := titlePattern.FindStringSubmatch(line); matches != nil {
				p.Title = strings.TrimSpace(matches[1])
				continue
			}
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
// patterns is an optional variadic list of task-header templates (e.g.
// "### Task {N}: {title}"). If empty, DefaultTaskHeaderPatterns is used.
func ParsePlanFile(path string, patterns ...string) (*Plan, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path is internally resolved, not from user input
	if err != nil {
		return nil, fmt.Errorf("read plan file: %w", err)
	}
	return ParsePlan(string(content), patterns...)
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
