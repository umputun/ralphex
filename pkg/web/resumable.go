package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ResumableSession represents a plan creation session that can be resumed.
type ResumableSession struct {
	ProgressPath    string    `json:"progress_path"`    // full path to progress file
	PlanDescription string    `json:"plan_description"` // from Plan: header (in plan mode)
	Branch          string    `json:"branch"`           // from Branch: header
	StartTime       time.Time `json:"start_time"`       // from Started: header
	Dir             string    `json:"dir"`              // directory containing progress file
	QACount         int       `json:"qa_count"`         // number of Q&A pairs found
	PendingQuestion string    `json:"pending_question,omitempty"`
	PendingOptions  []string  `json:"pending_options,omitempty"`
}

// FindResumableSessions scans directories for resumable plan creation sessions.
// A session is resumable if:
// 1. Mode is "plan" (from header)
// 2. No "Completed:" footer line
// 3. No PLAN_READY signal in content
// 4. File is not locked (no active session)
func FindResumableSessions(dirs []string) ([]ResumableSession, error) {
	var sessions []ResumableSession

	for _, dir := range dirs {
		// find progress-plan-*.txt files
		pattern := filepath.Join(dir, "progress-plan-*.txt")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue // skip invalid patterns
		}

		for _, path := range matches {
			session, ok, err := checkResumable(path)
			if err != nil {
				continue // skip files with errors
			}
			if ok {
				sessions = append(sessions, session)
			}
		}
	}

	return sessions, nil
}

// checkResumable checks if a progress file represents a resumable session.
// Returns the session info and true if resumable, or false if not resumable.
func checkResumable(path string) (ResumableSession, bool, error) {
	// check if file is locked (active session)
	isActive, err := IsActive(path)
	if err != nil {
		return ResumableSession{}, false, err
	}
	if isActive {
		return ResumableSession{}, false, nil // skip active sessions
	}

	// parse header
	meta, err := ParseProgressHeader(path)
	if err != nil {
		return ResumableSession{}, false, err
	}

	// only plan mode sessions can be resumed
	if meta.Mode != "plan" {
		return ResumableSession{}, false, nil
	}

	// scan file for completion markers and Q&A count
	completed, qaCount, pendingQuestion, pendingOptions, err := scanProgressFile(path)
	if err != nil {
		return ResumableSession{}, false, err
	}

	// skip completed sessions
	if completed {
		return ResumableSession{}, false, nil
	}

	return ResumableSession{
		ProgressPath:    path,
		PlanDescription: meta.PlanPath, // in plan mode, Plan: contains the description
		Branch:          meta.Branch,
		StartTime:       meta.StartTime,
		Dir:             filepath.Dir(path),
		QACount:         qaCount,
		PendingQuestion: pendingQuestion,
		PendingOptions:  pendingOptions,
	}, true, nil
}

// scanProgressFile scans a progress file to determine if it's completed
// and count Q&A pairs. A session is completed if it has:
// - A "Completed:" footer line, OR
// - A PLAN_READY signal
func scanProgressFile(path string) (completed bool, qaCount int, pendingQuestion string, pendingOptions []string, err error) {
	f, err := os.Open(path) //nolint:gosec // path from Glob result
	if err != nil {
		return false, 0, "", nil, fmt.Errorf("open progress file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// use larger buffer for progress files (can have long lines)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var currentQuestion string
	var currentOptions []string
	var inQuestionBlock bool
	var questionBuf strings.Builder
	const questionStart = "<<<RALPHEX:QUESTION>>>"
	const questionEnd = "<<<RALPHEX:END>>>"

	for scanner.Scan() {
		line := scanner.Text()
		raw := stripTimestampPrefix(line)

		// check for completion markers
		if strings.HasPrefix(raw, "Completed:") {
			completed = true
		}
		if strings.Contains(raw, "PLAN_READY") {
			completed = true
		}

		if inQuestionBlock {
			consumeQuestionBlockLine(raw, questionEnd, &questionBuf, &inQuestionBlock, &currentQuestion, &currentOptions, &pendingQuestion, &pendingOptions)
			continue
		}

		if _, afterStart, found := strings.Cut(raw, questionStart); found {
			if payload, _, foundEnd := strings.Cut(afterStart, questionEnd); foundEnd {
				payload = strings.TrimSpace(payload)
				q, opts := parseQuestionBlock(payload)
				if q != "" {
					currentQuestion = q
					currentOptions = opts
					pendingQuestion = q
					pendingOptions = opts
				}
				continue
			}
			inQuestionBlock = true
			questionBuf.Reset()
			if trimmed := strings.TrimSpace(afterStart); trimmed != "" {
				questionBuf.WriteString(trimmed)
				questionBuf.WriteByte('\n')
			}
			continue
		}

		// count Q&A pairs (look for ANSWER: lines in timestamped format)
		// format: [timestamp] ANSWER: ...
		if strings.HasPrefix(raw, "ANSWER:") {
			qaCount++
			currentQuestion = ""
			currentOptions = nil
			pendingQuestion = ""
			pendingOptions = nil
			continue
		}

		if questionLine, ok := strings.CutPrefix(raw, "QUESTION:"); ok {
			currentQuestion = strings.TrimSpace(questionLine)
			currentOptions = nil
			pendingQuestion = currentQuestion
			pendingOptions = nil
			continue
		}

		if strings.HasPrefix(raw, "OPTIONS:") && currentQuestion != "" {
			optionsText := strings.TrimSpace(strings.TrimPrefix(raw, "OPTIONS:"))
			currentOptions = splitOptions(optionsText)
			pendingOptions = currentOptions
		}
	}

	if err := scanner.Err(); err != nil {
		return false, 0, "", nil, fmt.Errorf("scan progress file: %w", err)
	}

	if pendingQuestion == "" && currentQuestion != "" {
		pendingQuestion = currentQuestion
		pendingOptions = currentOptions
	}

	return completed, qaCount, pendingQuestion, pendingOptions, nil
}

func consumeQuestionBlockLine(raw, questionEnd string, questionBuf *strings.Builder, inQuestionBlock *bool, currentQuestion *string, currentOptions *[]string, pendingQuestion *string, pendingOptions *[]string) {
	endIdx := strings.Index(raw, questionEnd)
	if endIdx == -1 {
		questionBuf.WriteString(strings.TrimSpace(raw))
		questionBuf.WriteByte('\n')
		return
	}

	if endIdx > 0 {
		questionBuf.WriteString(strings.TrimSpace(raw[:endIdx]))
		questionBuf.WriteByte('\n')
	}

	*inQuestionBlock = false
	q, opts := parseQuestionBlock(questionBuf.String())
	if q == "" {
		return
	}

	*currentQuestion = q
	*currentOptions = opts
	*pendingQuestion = q
	*pendingOptions = opts
}

func stripTimestampPrefix(line string) string {
	if strings.HasPrefix(line, "[") {
		if _, after, found := strings.Cut(line, "] "); found {
			return after
		}
	}
	return line
}

func parseQuestionBlock(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	type payload struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	var p payload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "", nil
	}
	return strings.TrimSpace(p.Question), p.Options
}

func splitOptions(optionsText string) []string {
	if optionsText == "" {
		return nil
	}
	parts := strings.Split(optionsText, ", ")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
