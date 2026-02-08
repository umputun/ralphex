package web

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/umputun/ralphex/pkg/status"
)

// TailerConfig holds configuration for the Tailer.
type TailerConfig struct {
	PollInterval time.Duration // how often to check for new content (default: 100ms)
	InitialPhase status.Phase  // phase to use for events (default: PhaseTask)
}

// DefaultTailerConfig returns default configuration.
func DefaultTailerConfig() TailerConfig {
	return TailerConfig{
		PollInterval: 100 * time.Millisecond,
		InitialPhase: status.PhaseTask,
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
	phase    status.Phase
	inHeader bool // true until we pass the header separator

	// defer section emission until first timestamped line (useful when reading from start)
	deferSections  bool
	pendingSection string
	pendingPhase   status.Phase
}

// NewTailer creates a new Tailer for the given progress file.
// the tailer starts in stopped state; call Start() to begin tailing.
func NewTailer(path string, config TailerConfig) *Tailer {
	if config.PollInterval <= 0 {
		config.PollInterval = 100 * time.Millisecond
	}
	if config.InitialPhase == "" {
		config.InitialPhase = status.PhaseTask
	}

	return &Tailer{
		path:     path,
		config:   config,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		eventCh:  make(chan Event, 2048),
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
	t.deferSections = fromStart
	t.pendingSection = ""
	t.pendingPhase = ""

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

		if t.deferSections {
			events := t.parseLineDeferred(line)
			for i := range events {
				t.sendEvent(events[i])
			}
			continue
		}

		// parse line and emit event
		event := t.parseLine(line)
		if event != nil {
			t.sendEvent(*event)
		}
	}
}

// sendEvent tries to enqueue an event; when the queue is full, it prefers
// keeping high-priority events (sections, task boundaries, signals) by dropping
// older events to make space.
func (t *Tailer) sendEvent(event Event) {
	select {
	case t.eventCh <- event:
		return
	default:
	}

	if !isPriorityEvent(event.Type) {
		return
	}

	// drop one event to make room (best-effort)
	select {
	case <-t.eventCh:
	default:
	}

	select {
	case t.eventCh <- event:
	default:
		// still full; drop
	}
}

func isPriorityEvent(eventType EventType) bool {
	switch eventType {
	case EventTypeSection, EventTypeTaskStart, EventTypeTaskEnd, EventTypeSignal:
		return true
	default:
		return false
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
	parsed, newInHeader := parseProgressLine(line, t.inHeader)
	t.inHeader = newInHeader

	switch parsed.Type {
	case ParsedLineSkip:
		return nil
	case ParsedLineSection:
		t.phase = parsed.Phase
		return &Event{
			Type:      EventTypeSection,
			Phase:     t.phase,
			Section:   parsed.Section,
			Text:      parsed.Text,
			Timestamp: time.Now(),
		}
	case ParsedLineTimestamp:
		return &Event{
			Type:      parsed.EventType,
			Phase:     t.phase,
			Text:      parsed.Text,
			Timestamp: parsed.Timestamp,
			Signal:    parsed.Signal,
		}
	case ParsedLinePlain:
		return &Event{
			Type:      EventTypeOutput,
			Phase:     t.phase,
			Text:      parsed.Text,
			Timestamp: time.Now(),
		}
	default:
		return nil
	}
}

// parseLineDeferred parses a line and defers section emission until the first
// timestamped or output line, so section timestamps align with log timestamps.
func (t *Tailer) parseLineDeferred(line string) []Event {
	parsed, newInHeader := parseProgressLine(line, t.inHeader)
	t.inHeader = newInHeader

	switch parsed.Type {
	case ParsedLineSkip:
		return nil
	case ParsedLineSection:
		t.phase = parsed.Phase
		var events []Event
		if t.pendingSection != "" {
			events = append(events, t.emitPendingSection(time.Now())...)
		}
		t.pendingSection = parsed.Section
		t.pendingPhase = parsed.Phase
		return events
	case ParsedLineTimestamp:
		var events []Event
		if t.pendingSection != "" {
			events = append(events, t.emitPendingSection(parsed.Timestamp)...)
		}
		events = append(events, Event{
			Type:      parsed.EventType,
			Phase:     t.phase,
			Text:      parsed.Text,
			Timestamp: parsed.Timestamp,
			Signal:    parsed.Signal,
		})
		return events
	case ParsedLinePlain:
		var events []Event
		if t.pendingSection != "" {
			events = append(events, t.emitPendingSection(time.Now())...)
		}
		events = append(events, Event{
			Type:      EventTypeOutput,
			Phase:     t.phase,
			Text:      parsed.Text,
			Timestamp: time.Now(),
		})
		return events
	default:
		return nil
	}
}

// emitPendingSection returns events for a pending section and clears it.
func (t *Tailer) emitPendingSection(ts time.Time) []Event {
	if t.pendingSection == "" {
		return nil
	}

	sectionName := t.pendingSection
	phase := t.pendingPhase
	t.pendingSection = ""
	t.pendingPhase = ""

	var events []Event
	if matches := taskIterationRegex.FindStringSubmatch(sectionName); matches != nil {
		if taskNum, err := strconv.Atoi(matches[1]); err == nil {
			events = append(events, Event{
				Type:      EventTypeTaskStart,
				Phase:     phase,
				TaskNum:   taskNum,
				Text:      sectionName,
				Timestamp: ts,
			})
		}
	}

	events = append(events, Event{
		Type:      EventTypeSection,
		Phase:     phase,
		Section:   sectionName,
		Text:      sectionName,
		Timestamp: ts,
	})

	return events
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
