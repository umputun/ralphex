package web

import (
	"bufio"
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
	completed, qaCount, err := scanProgressFile(path)
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
	}, true, nil
}

// scanProgressFile scans a progress file to determine if it's completed
// and count Q&A pairs. A session is completed if it has:
// - A "Completed:" footer line, OR
// - A PLAN_READY signal
func scanProgressFile(path string) (completed bool, qaCount int, err error) {
	f, err := os.Open(path) //nolint:gosec // path from Glob result
	if err != nil {
		return false, 0, fmt.Errorf("open progress file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// use larger buffer for progress files (can have long lines)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// check for completion markers
		if strings.HasPrefix(line, "Completed:") {
			completed = true
		}
		if strings.Contains(line, "PLAN_READY") {
			completed = true
		}

		// count Q&A pairs (look for ANSWER: lines in timestamped format)
		// format: [timestamp] ANSWER: ...
		if strings.Contains(line, "] ANSWER:") {
			qaCount++
		}
	}

	if err := scanner.Err(); err != nil {
		return false, 0, fmt.Errorf("scan progress file: %w", err)
	}

	return completed, qaCount, nil
}
