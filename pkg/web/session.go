package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/tmaxmax/go-sse"

	"github.com/umputun/ralphex/pkg/status"
)

// DefaultReplayerSize is the maximum number of events to keep for replay to late-joining clients.
const DefaultReplayerSize = 10000

// allEventsReplayer wraps FiniteReplayer to replay ALL events when LastEventID is empty.
// standard FiniteReplayer only replays events after a specific ID, which doesn't work
// for first-time connections (no Last-Event-ID header).
//
// implementation note: FiniteReplayer assigns monotonically increasing integer IDs
// as strings starting at "1". by setting LastEventID to "0" when empty, we effectively
// request replay of all stored events. this depends on FiniteReplayer's internal
// ID generation scheme - if the library changes this behavior, replay may break.
type allEventsReplayer struct {
	inner *sse.FiniteReplayer
}

// Put delegates to the inner replayer.
func (r *allEventsReplayer) Put(message *sse.Message, topics []string) (*sse.Message, error) {
	return r.inner.Put(message, topics) //nolint:wrapcheck // pass through replayer errors as-is
}

// Replay replays events. If LastEventID is empty, replays from ID "0" (all events).
func (r *allEventsReplayer) Replay(subscription sse.Subscription) error {
	// if no LastEventID, replay from the beginning by using ID "0"
	// (our auto-generated IDs start at 1, so "0" means "replay everything")
	if subscription.LastEventID.String() == "" {
		subscription.LastEventID = sse.ID("0")
	}
	return r.inner.Replay(subscription) //nolint:wrapcheck // pass through replayer errors as-is
}

// SessionState represents the current state of a session.
type SessionState string

// session state constants.
const (
	SessionStateActive    SessionState = "active"    // session is running (progress file locked)
	SessionStateCompleted SessionState = "completed" // session finished (no lock held)
)

// SessionMetadata holds parsed information from progress file header.
type SessionMetadata struct {
	PlanPath  string    // path to plan file (from "Plan:" header line)
	Branch    string    // git branch (from "Branch:" header line)
	Mode      string    // execution mode: full, review, codex-only (from "Mode:" header line)
	StartTime time.Time // start time (from "Started:" header line)
}

// defaultTopic is the SSE topic used for all events within a session.
const defaultTopic = "events"

// Session represents a single ralphex execution instance.
// each session corresponds to one progress file and maintains its own SSE server.
type Session struct {
	mu sync.RWMutex

	// stopMu serializes concurrent StopTailing invocations so that two callers
	// cannot both observe s.tailer != nil, capture the same tailer reference,
	// and race in the final critical section. without this, the second caller
	// could clear stopping/tailer fields before the first caller's feedEvents
	// drain completes — letting a Reactivate slip in on a stale lastOffset and
	// then having the first caller's final lock clobber the new tailer.
	stopMu sync.Mutex

	// set once at creation, immutable after
	ID   string      // unique identifier (derived from progress filename)
	Path string      // full path to progress file
	SSE  *sse.Server // SSE server for this session (handles subscriptions and replay)

	metadata SessionMetadata // parsed header information
	state    SessionState    // current state (active/completed)
	tailer   *Tailer         // file tailer for reading new content (nil if not tailing)

	// lastModified tracks the file's last modification time for change detection
	lastModified time.Time

	// diffStats holds git diff statistics when available (nil if not set)
	diffStats *DiffStats

	// stopTailCh signals the tail feeder goroutine to stop
	stopTailCh chan struct{}

	// feedDoneCh is closed by the tail feeder goroutine when it returns.
	// StopTailing waits on this channel so that by the time it returns, the
	// feeder has drained any remaining events from the tailer's eventCh and
	// published them. without this wait, events buffered in eventCh between
	// tailer.Offset() advancing (in readNewLines) and feedEvents consuming
	// them could be silently dropped when stopTailCh is closed, leaving a
	// gap between lastOffset and what was actually published to SSE.
	feedDoneCh chan struct{}

	// loaded tracks whether historical data has been loaded into the SSE server
	loaded bool

	// lastOffset is the byte offset into the progress file of the last byte
	// already ingested (via the loader or a previous tailer). used by Reactivate
	// to resume tailing without re-emitting events into the SSE replay buffer.
	lastOffset int64

	// lastPhase is the parser phase after the last ingested byte (from the
	// loader or a previous tailer). used by Reactivate so a new tailer picks
	// up the correct phase for subsequent lines, rather than defaulting to
	// PhaseTask until the next section header. empty string means "unknown",
	// in which case the tailer's configured default phase is used.
	lastPhase status.Phase

	// lastPendingSection / lastPendingPhase carry a deferred section header
	// that a previous tailer read but never emitted (the section/task-start
	// event is only published when the next timestamped/output line arrives).
	// used by Reactivate so that if a tailer is stopped between reading a
	// section header and reading the following line, the new tailer still
	// emits the section event on the next line instead of silently dropping
	// it. empty string means nothing is pending.
	lastPendingSection string
	lastPendingPhase   status.Phase

	// stopping is true while StopTailing is mid-flight (between the first
	// locked section that captures tailer/stopCh/feedDone and the final
	// locked section that records lastOffset/lastPhase). during that window
	// s.mu is released so tailer.Stop() and feedEvents drain can run without
	// blocking other readers; without this flag a concurrent Reactivate /
	// StartTailing would see s.tailer.IsRunning()==false (tailer stopped)
	// with a STALE s.lastOffset (not yet captured) and start a new tailer
	// from the wrong byte position, duplicating or losing events in the
	// exact flock-race recovery path this PR is meant to fix.
	stopping bool
}

// NewSession creates a new session for the given progress file path.
// the session starts with an SSE server configured for event replay.
// metadata should be populated by calling ParseMetadata after creation.
func NewSession(id, path string) *Session {
	finiteReplayer, err := sse.NewFiniteReplayer(DefaultReplayerSize, true)
	if err != nil {
		// FiniteReplayer only returns error for count < 2, which won't happen
		log.Printf("[WARN] failed to create replayer: %v", err)
		finiteReplayer = nil
	}

	// wrap in allEventsReplayer to replay all events on first connection
	var replayer sse.Replayer
	if finiteReplayer != nil {
		replayer = &allEventsReplayer{inner: finiteReplayer}
	}

	sseServer := &sse.Server{
		Provider: &sse.Joe{
			Replayer: replayer,
		},
		OnSession: func(w http.ResponseWriter, r *http.Request) ([]string, bool) {
			return []string{defaultTopic}, true
		},
	}

	return &Session{
		ID:    id,
		Path:  path,
		state: SessionStateCompleted, // default to completed until proven active
		SSE:   sseServer,
	}
}

// SetMetadata updates the session's metadata thread-safely.
func (s *Session) SetMetadata(meta SessionMetadata) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadata = meta
}

// GetMetadata returns the session's metadata thread-safely.
func (s *Session) GetMetadata() SessionMetadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.metadata
}

// SetState updates the session's state thread-safely.
func (s *Session) SetState(state SessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// GetState returns the session's state thread-safely.
func (s *Session) GetState() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// GetTailer returns the session's tailer thread-safely.
func (s *Session) GetTailer() *Tailer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tailer
}

// SetTailer updates the session's tailer thread-safely.
func (s *Session) SetTailer(tailer *Tailer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tailer = tailer
}

// SetLastModified updates the last modified time thread-safely.
func (s *Session) SetLastModified(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastModified = t
}

// GetLastModified returns the last modified time thread-safely.
func (s *Session) GetLastModified() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastModified
}

// GetDiffStats returns a copy of the diff stats, or nil if not set.
func (s *Session) GetDiffStats() *DiffStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.diffStats == nil {
		return nil
	}
	copyStats := *s.diffStats
	return &copyStats
}

// SetDiffStats stores diff stats for the session.
func (s *Session) SetDiffStats(stats DiffStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diffStats = &stats
}

// IsLoaded returns whether historical data has been loaded into the SSE server.
func (s *Session) IsLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded
}

// MarkLoadedIfNot atomically checks if the session is not loaded and marks it as loaded.
// returns true if the session was successfully marked (was not loaded before),
// false if it was already loaded. this prevents double-loading race conditions.
func (s *Session) MarkLoadedIfNot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return false
	}
	s.loaded = true
	return true
}

// resetForNewRun clears per-run state for a progress file that was truncated
// and re-initialized by a new ralphex invocation. callers must have observed
// that the header's Started: timestamp changed, indicating the stored offset
// and loader state no longer correspond to current file content. callers are
// also responsible for stopping any ongoing tailer before invoking this method
// so offset capture in StopTailing does not race with the reset.
//
// SSE replay buffer is intentionally not cleared: the go-sse FiniteReplayer
// has no clear primitive, and stale events from the previous run will age out
// of the fixed-size buffer as the new run publishes events.
func (s *Session) resetForNewRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastOffset = 0
	s.lastPhase = ""
	s.lastPendingSection = ""
	s.lastPendingPhase = ""
	s.loaded = false
	s.diffStats = nil
}

// tailerStartMode selects how startTailerLocked begins tailing.
type tailerStartMode int

const (
	modeFromStart tailerStartMode = iota // read from beginning of file
	modeFromEnd                          // seek to end (only new writes are emitted)
	modeResume                           // resume from a stored byte offset
)

// startTailerLocked creates and starts a new tailer according to mode.
// the caller MUST hold s.mu as a write lock. on success, the new tailer is
// stored on the session, stopTailCh and feedDoneCh are allocated, and a
// feedEvents goroutine is launched. the goroutine receives tailer, stopCh,
// and feedDone as arguments captured at spawn time, so it does not depend on
// reading session fields after launch and does not need to acquire s.mu.
// on error, s.tailer, s.stopTailCh, and s.feedDoneCh are left unchanged.
//
// for modeResume and modeFromEnd, the tailer is pre-seeded with s.lastPhase
// (if set) so lines emitted after resume carry the correct phase. modeFromStart
// uses the tailer's default phase because it re-reads the whole file and will
// encounter section headers that update the phase naturally.
//
// for modeResume, the tailer is additionally pre-seeded with any pending
// section state captured when the previous tailer was stopped (see
// StopTailing). this ensures a section header read but not yet emitted by
// the previous tailer is still emitted by the resumed tailer on the next
// line, instead of being silently dropped across the restart.
func (s *Session) startTailerLocked(mode tailerStartMode, offset int64) error {
	cfg := DefaultTailerConfig()
	if mode != modeFromStart && s.lastPhase != "" {
		cfg.InitialPhase = s.lastPhase
	}
	if mode == modeResume {
		cfg.PendingSection = s.lastPendingSection
		cfg.PendingPhase = s.lastPendingPhase
	}
	tailer := NewTailer(s.Path, cfg)

	var err error
	switch mode {
	case modeFromStart:
		err = tailer.Start(true)
	case modeFromEnd:
		err = tailer.Start(false)
	case modeResume:
		err = tailer.StartFromOffset(offset)
	default:
		return fmt.Errorf("unknown tailer start mode: %d", mode)
	}
	if err != nil {
		return err
	}

	stopCh := make(chan struct{})
	feedDone := make(chan struct{})
	s.tailer = tailer
	s.stopTailCh = stopCh
	s.feedDoneCh = feedDone
	go s.feedEvents(tailer, stopCh, feedDone)
	return nil
}

// StartTailing begins tailing the progress file and feeding events to SSE clients.
// if fromStart is true, reads from the beginning of the file.
// does nothing if already tailing, or if StopTailing is currently in progress
// (caller must retry once stopping finishes — the watcher naturally does this
// on the next Write event).
func (s *Session) StartTailing(fromStart bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopping {
		return nil // StopTailing mid-flight; caller retries on next event
	}

	if s.tailer != nil && s.tailer.IsRunning() {
		return nil // already tailing
	}

	mode := modeFromEnd
	if fromStart {
		mode = modeFromStart
	}
	return s.startTailerLocked(mode, 0)
}

// Reactivate resumes tailing a session that was previously marked completed,
// e.g. after a flock race in RefreshStates that briefly observed the progress
// file as unlocked. it resumes from the stored lastOffset so events already
// in the SSE replay buffer (from the loader or a previous tailer) are not
// re-emitted. on success, the session state is flipped to active.
//
// idempotent: if a tailer is already running, returns nil and leaves the
// state as-is (the caller is responsible for having checked it). on
// tailer-start failure, returns the error and leaves state unchanged.
//
// if StopTailing is currently in progress on another goroutine, returns nil
// without starting a new tailer. lastOffset has not yet been captured at that
// point, so starting now would use a stale offset and duplicate/lose events.
// the watcher will re-call Reactivate on the next Write event, which lands
// after StopTailing completes and observes the correct lastOffset.
//
// callers should verify the session is in the SessionStateCompleted state and
// IsLoaded() before invoking this method; the watcher gates it that way to
// avoid racing with the initial progress-file loader.
func (s *Session) Reactivate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopping {
		return nil // StopTailing mid-flight; watcher retries on next Write event
	}

	if s.tailer != nil && s.tailer.IsRunning() {
		return nil
	}

	offset := s.lastOffset
	mode := modeFromEnd
	if offset > 0 {
		mode = modeResume
	}

	if err := s.startTailerLocked(mode, offset); err != nil {
		return err
	}

	s.state = SessionStateActive
	return nil
}

// StopTailing stops the tailer and event feeder goroutine, then captures the
// tailer's final byte offset, parser phase, and any deferred section state
// into lastOffset, lastPhase, and lastPendingSection/Phase so a subsequent
// Reactivate can resume from the exact byte where tailing stopped with the
// correct phase AND still emit any section header event the previous tailer
// had read but not yet published.
//
// ordering matters: the tailer is stopped first (blocking until its tail loop
// has exited, so eventCh receives no more writes), then stopTailCh is closed
// and feedEvents drains any remaining buffered events before returning. only
// then is lastOffset captured. without this synchronization, events buffered
// in the tailer's eventCh could be dropped by a racing select on stopTailCh
// while their bytes are already accounted for in tailer.Offset(), creating a
// gap in the SSE stream after Reactivate resumes from lastOffset.
//
// concurrency: the s.stopping flag is set under s.mu for the entire duration
// of tailer.Stop()+feedEvents drain, so a concurrent Reactivate/StartTailing
// that acquires s.mu during the unlocked drain window observes stopping=true
// and returns without starting a new tailer on a stale lastOffset. the final
// locked section clears stopping and nils out s.tailer so subsequent calls
// see a clean slate.
//
// if no tailer is running, all last* fields are preserved. safe to call
// concurrently and repeatedly; concurrent calls are serialized via stopMu so
// the second caller observes s.tailer==nil after the first finishes and returns
// without touching captured fields.
func (s *Session) StopTailing() {
	// serialize concurrent StopTailing calls; the unlocked drain window
	// (between the first and final mu critical sections) cannot otherwise
	// safely overlap, see stopMu doc on Session.
	s.stopMu.Lock()
	defer s.stopMu.Unlock()

	s.mu.Lock()
	if s.tailer == nil {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	tailer := s.tailer
	stopCh := s.stopTailCh
	feedDone := s.feedDoneCh
	s.stopTailCh = nil
	s.feedDoneCh = nil
	s.mu.Unlock()

	// stop the tailer first so no more events are pushed into eventCh.
	// tailer.Stop() is idempotent and blocks until the tail loop has exited.
	tailer.Stop()

	// signal feedEvents to drain any remaining buffered events and exit.
	// stopCh is only closed once because it was nil-swapped under the lock.
	if stopCh != nil {
		close(stopCh)
	}

	// wait for feedEvents to finish draining. after this returns, every event
	// the tailer produced has been published to SSE, so capturing tailer.Offset()
	// below gives a resume point whose bytes have all been published.
	if feedDone != nil {
		<-feedDone
	}

	s.mu.Lock()
	s.lastOffset = tailer.Offset()
	s.lastPhase = tailer.Phase()
	s.lastPendingSection, s.lastPendingPhase = tailer.PendingSection()
	s.tailer = nil
	s.stopping = false
	s.mu.Unlock()
}

// getLastOffset returns the byte offset of the last ingested content.
// package-internal accessor, thread-safe.
func (s *Session) getLastOffset() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastOffset
}

// setLastOffset updates the byte offset of the last ingested content.
// package-internal accessor, thread-safe.
func (s *Session) setLastOffset(offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastOffset = offset
}

// getLastPhase returns the parser phase after the last ingested byte, or an
// empty phase if nothing has been ingested yet. package-internal accessor,
// thread-safe.
func (s *Session) getLastPhase() status.Phase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastPhase
}

// setLastPhase updates the parser phase after the last ingested byte.
// package-internal accessor, thread-safe.
func (s *Session) setLastPhase(phase status.Phase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastPhase = phase
}

// IsTailing returns whether the session is currently tailing its progress file.
func (s *Session) IsTailing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tailer != nil && s.tailer.IsRunning()
}

// Publish sends an event to all connected SSE clients and stores it for replay.
// returns an error if publishing fails.
func (s *Session) Publish(event Event) error {
	msg := event.ToSSEMessage()
	if err := s.SSE.Publish(msg, defaultTopic); err != nil {
		return fmt.Errorf("publish event: %w", err)
	}
	return nil
}

// feedEvents reads events from the tailer and publishes them to SSE clients.
// the tailer, stopCh, and feedDone are captured at spawn time in startTailerLocked
// and passed explicitly so the goroutine is not subject to field reassignment
// races. when stopCh is signaled, feedEvents drains any remaining events from
// the tailer's eventCh before returning, so published bytes match tailer.Offset()
// (StopTailing guarantees the tailer is stopped before closing stopCh, so the
// pending events in eventCh are a bounded final set).
func (s *Session) feedEvents(tailer *Tailer, stopCh, feedDone chan struct{}) {
	defer close(feedDone)

	if tailer == nil {
		return
	}

	eventCh := tailer.Events()
	publish := func(event Event) {
		if event.Type == EventTypeOutput {
			if stats, ok := parseDiffStats(event.Text); ok {
				s.SetDiffStats(stats)
			}
		}
		if err := s.Publish(event); err != nil {
			log.Printf("[WARN] failed to publish tailed event: %v", err)
		}
	}

	for {
		select {
		case <-stopCh:
			// stopCh is closed only after tailer.Stop() has returned (see
			// StopTailing), so no more events will be pushed into eventCh.
			// drain the remaining buffered events so that offsets captured
			// from the tailer match bytes actually published to SSE.
			for {
				select {
				case event, ok := <-eventCh:
					if !ok {
						return
					}
					publish(event)
				default:
					return
				}
			}
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			publish(event)
		}
	}
}

// Close cleans up session resources including the tailer and SSE server.
func (s *Session) Close() {
	s.StopTailing()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.SSE.Shutdown(ctx); err != nil {
		log.Printf("[WARN] failed to shutdown SSE server: %v", err)
	}
}
