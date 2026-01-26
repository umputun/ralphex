package web

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/umputun/ralphex/pkg/processor"
)

// TailerConfig holds configuration for the Tailer.
type TailerConfig struct {
	PollInterval time.Duration   // how often to check for new content (default: 100ms)
	InitialPhase processor.Phase // phase to use for events (default: PhaseTask)
}

// DefaultTailerConfig returns default configuration.
func DefaultTailerConfig() TailerConfig {
	return TailerConfig{
		PollInterval: 100 * time.Millisecond,
		InitialPhase: processor.PhaseTask,
	}
}

// Tailer watches a progress file and emits events for new lines.
// it parses progress file format (timestamps, sections) into Event structs.
type Tailer struct {
	mu       sync.Mutex
	path     string
	config   TailerConfig
	file     *os.File
	reader   *bufio.Reader
	offset   int64
	running  bool
	stopped  atomic.Bool // guards against double-stop panic
	stopCh   chan struct{}
	doneCh   chan struct{}
	eventCh  chan Event
	phase    processor.Phase
	inHeader bool // true until we pass the header separator
}

// NewTailer creates a new Tailer for the given progress file.
// the tailer starts in stopped state; call Start() to begin tailing.
func NewTailer(path string, config TailerConfig) *Tailer {
	if config.PollInterval <= 0 {
		config.PollInterval = 100 * time.Millisecond
	}
	if config.InitialPhase == "" {
		config.InitialPhase = processor.PhaseTask
	}

	return &Tailer{
		path:     path,
		config:   config,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		eventCh:  make(chan Event, 256),
		phase:    config.InitialPhase,
		inHeader: true,
	}
}

// Events returns the channel that emits parsed events.
// events are emitted in order as lines are read from the file.
func (t *Tailer) Events() <-chan Event {
	return t.eventCh
}

// Start begins tailing the file from the current position.
// if fromStart is true, reads from the beginning; otherwise reads from current end.
// note: Tailer is not reusable after Stop() - create a new instance instead.
func (t *Tailer) Start(fromStart bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return nil
	}

	f, err := os.Open(t.path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}

	if !fromStart {
		// seek to end
		offset, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
			return fmt.Errorf("seek to end: %w", err)
		}
		t.offset = offset
		t.inHeader = false // assume we're past header if starting from end
	}

	t.file = f
	t.reader = bufio.NewReader(f)
	t.running = true
	t.stopCh = make(chan struct{})
	t.doneCh = make(chan struct{})

	go t.tailLoop()

	return nil
}

// Stop stops the tailer and closes resources.
// blocks until the tail loop has fully stopped.
// safe to call multiple times concurrently.
func (t *Tailer) Stop() {
	// use atomic to prevent double-close of stopCh
	if t.stopped.Swap(true) {
		// already stopped or stopping, wait for completion
		t.mu.Lock()
		doneCh := t.doneCh
		t.mu.Unlock()
		if doneCh != nil {
			<-doneCh
		}
		return
	}

	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return
	}
	close(t.stopCh)
	doneCh := t.doneCh
	t.mu.Unlock()

	<-doneCh
}

// IsRunning returns whether the tailer is currently active.
func (t *Tailer) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// tailLoop is the main loop that polls for new content.
func (t *Tailer) tailLoop() {
	defer func() {
		t.mu.Lock()
		if t.file != nil {
			t.file.Close()
			t.file = nil
		}
		t.running = false
		t.mu.Unlock()
		close(t.doneCh)
	}()

	ticker := time.NewTicker(t.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.readNewLines()
		}
	}
}

// readNewLines reads any new lines from the file and emits events.
func (t *Tailer) readNewLines() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.file == nil {
		return
	}

	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// no more data, wait for next poll
				// seek back to where we were (ReadString may have read partial line)
				if line != "" {
					_, _ = t.file.Seek(t.offset, io.SeekStart)
					t.reader.Reset(t.file)
				}
				return
			}
			// real error, stop tailing
			return
		}

		// update offset
		t.offset += int64(len(line))

		// trim newline
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		if line == "" {
			continue
		}

		// parse line and emit event
		event := t.parseLine(line)
		if event != nil {
			select {
			case t.eventCh <- *event:
			default:
				// channel full, drop event
			}
		}
	}
}

// timestamp regex: [YY-MM-DD HH:MM:SS]
var timestampRegex = regexp.MustCompile(`^\[(\d{2}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\] (.*)$`)

// section header regex: --- section name ---
var sectionRegex = regexp.MustCompile(`^--- (.+) ---$`)

// task iteration regex: task iteration N (extracts the number)
var taskIterationRegex = regexp.MustCompile(`(?i)^task iteration (\d+)$`)

// parseLine parses a progress file line and returns an Event.
// returns nil for lines that should be skipped (header lines).
func (t *Tailer) parseLine(line string) *Event {
	// check for header separator
	if strings.HasPrefix(line, "---") && strings.Count(line, "-") > 20 && !strings.Contains(line, " ") {
		t.inHeader = false
		return nil
	}

	// skip header lines
	if t.inHeader {
		return nil
	}

	// check for section header
	if matches := sectionRegex.FindStringSubmatch(line); matches != nil {
		sectionName := matches[1]
		t.updatePhaseFromSection(sectionName)
		return &Event{
			Type:      EventTypeSection,
			Phase:     t.phase,
			Section:   sectionName,
			Text:      sectionName,
			Timestamp: time.Now(),
		}
	}

	// check for timestamped line
	if matches := timestampRegex.FindStringSubmatch(line); matches != nil {
		text := matches[2]

		// parse timestamp
		ts, err := time.Parse("06-01-02 15:04:05", matches[1])
		if err != nil {
			ts = time.Now()
		}

		// detect event type from content
		eventType := detectEventType(text)
		event := Event{
			Type:      eventType,
			Phase:     t.phase,
			Text:      text,
			Timestamp: ts,
		}

		// extract signal if present
		if sig := extractSignalFromText(text); sig != "" {
			event.Signal = sig
			event.Type = EventTypeSignal
		}

		return &event
	}

	// plain line (no timestamp) - treat as output
	return &Event{
		Type:      EventTypeOutput,
		Phase:     t.phase,
		Text:      line,
		Timestamp: time.Now(),
	}
}

// updatePhaseFromSection updates the current phase based on section name.
// uses the shared phaseFromSection helper to avoid duplicate logic.
func (t *Tailer) updatePhaseFromSection(name string) {
	t.phase = phaseFromSection(name)
}

// detectEventType determines the event type from line content.
func detectEventType(text string) EventType {
	textLower := strings.ToLower(text)

	if strings.HasPrefix(textLower, "error:") || strings.HasPrefix(text, "ERROR:") {
		return EventTypeError
	}
	if strings.HasPrefix(textLower, "warn:") || strings.HasPrefix(text, "WARN:") {
		return EventTypeWarn
	}
	if extractSignalFromText(text) != "" {
		return EventTypeSignal
	}

	return EventTypeOutput
}

// extractSignalFromText extracts normalized signal name from <<<RALPHEX:SIGNAL>>> format
// or plain signal markers like ALL_TASKS_DONE, TASK_FAILED, REVIEW_DONE.
// returns "COMPLETED" for ALL_TASKS_DONE, "FAILED" for TASK_FAILED, or raw signal for unknown tokens.
func extractSignalFromText(text string) string {
	const prefix = "<<<RALPHEX:"
	const suffix = ">>>"

	start := strings.Index(text, prefix)
	if start == -1 {
		return normalizePlainSignal(text)
	}

	end := strings.Index(text[start:], suffix)
	if end == -1 {
		return ""
	}

	rawSignal := text[start+len(prefix) : start+end]

	return normalizeTokenSignal(rawSignal)
}

func normalizePlainSignal(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	switch trimmed {
	case "ALL_TASKS_DONE", "COMPLETED":
		return "COMPLETED"
	case "TASK_FAILED", "ALL_TASKS_FAILED", "FAILED":
		return "FAILED"
	case "REVIEW_DONE":
		return "REVIEW_DONE"
	case "CODEX_REVIEW_DONE":
		return "CODEX_REVIEW_DONE"
	default:
		return ""
	}
}

// normalizeTokenSignal maps raw token signals to dashboard-friendly values.
func normalizeTokenSignal(rawSignal string) string {
	switch rawSignal {
	case "ALL_TASKS_DONE":
		return "COMPLETED"
	case "TASK_FAILED", "ALL_TASKS_FAILED":
		return "FAILED"
	case "REVIEW_DONE":
		return "REVIEW_DONE"
	case "CODEX_REVIEW_DONE":
		return "CODEX_REVIEW_DONE"
	default:
		return rawSignal
	}
}
