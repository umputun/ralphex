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
	// fenceOpenPattern matches a code-fence opener and captures its indent and backtick or tilde run.
	// indent is uncapped: a fence under "10." or a nested bullet sits 4+ spaces deep.
	fenceOpenPattern = regexp.MustCompile("^([ \t]*)(`{3,}|~{3,})")
	// fenceClosePattern matches a code-fence closer: same as opener but with no info string
	// permitted — only optional trailing whitespace (including a trailing CR for CRLF inputs that
	// reach scanners which do not strip it). used to avoid treating an inner
	// opener-with-info-string (e.g. ```go) as closing an outer fence.
	fenceClosePattern = regexp.MustCompile(`^([ \t]*)(` + "`" + `{3,}|~{3,})[ \t]*\r?$`)
)

// ParsePlan parses plan markdown content into a structured Plan.
func ParsePlan(content string) (*Plan, error) {
	p := &Plan{
		Tasks: make([]Task, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentTask *Task
	var ft fenceTracker

	for scanner.Scan() {
		line := scanner.Text()

		// skip lines inside fenced code blocks so example checkboxes are not parsed as tasks
		if ft.skip(line) {
			continue
		}

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

// FileHasUncompletedCheckbox returns true if the file contains any uncompleted checkbox (- [ ]).
// used for malformed plans (no task headers) to avoid treating them as complete.
func FileHasUncompletedCheckbox(path string) (bool, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path is internally resolved
	if err != nil {
		return false, fmt.Errorf("read plan file: %w", err)
	}
	var ft fenceTracker
	for line := range strings.SplitSeq(string(content), "\n") {
		if ft.skip(line) {
			continue
		}
		matches := checkboxPattern.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}
		if matches[1] == " " {
			return true, nil
		}
	}
	return false, nil
}

// fenceTracker tracks markdown fenced code block state across a line-by-line scan.
// the zero value is ready to use.
type fenceTracker struct {
	open   string // current fence marker (e.g. "```" or "~~~~"); empty when not in fenced block
	indent int    // leading whitespace of the opener
}

// skip reports whether line is inside (or opens/closes) a fenced code block and should be skipped
// by the caller. when the call returns true, the caller should advance to the next line. a fence
// is any indentation, then 3+ backticks or 3+ tildes. an opener may carry an info string
// (e.g. ```go); a closer may not — only trailing whitespace is allowed — and must use the same
// character as the opener with length at least equal to the opener. a closer indented more than
// 3 beyond the opener is content, so a deeper fence quoted inside an example cannot close it.
func (f *fenceTracker) skip(line string) bool {
	if f.open == "" {
		marker, indent := f.openerMarker(line)
		if marker == "" {
			return false
		}
		f.open, f.indent = marker, indent
		return true
	}
	marker, indent := f.closerMarker(line)
	if marker != "" && len(marker) >= len(f.open) && marker[0] == f.open[0] && indent <= f.indent+3 {
		f.open = ""
	}
	return true
}

// openerMarker returns the fence sequence (backticks or tildes) that opens a fenced code block
// and its indent width, or an empty marker if line is not an opener. info strings are allowed.
func (f *fenceTracker) openerMarker(line string) (marker string, indent int) {
	m := fenceOpenPattern.FindStringSubmatch(line)
	if len(m) < 3 {
		return "", 0
	}
	return m[2], len(m[1])
}

// closerMarker returns the fence sequence that closes a fenced code block and its indent width,
// or an empty marker if line is not a valid closer. closers must have no info string.
func (f *fenceTracker) closerMarker(line string) (marker string, indent int) {
	m := fenceClosePattern.FindStringSubmatch(line)
	if len(m) < 3 {
		return "", 0
	}
	return m[2], len(m[1])
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

// HasUncompletedCheckbox returns true if the task has any unchecked checkbox.
func (t *Task) HasUncompletedCheckbox() bool {
	for _, cb := range t.Checkboxes {
		if !cb.Checked {
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
