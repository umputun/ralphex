package status

import "sync"

// PhaseHolder stores the current execution phase in a thread-safe way.
// it is the single source of truth for the current phase across all components.
type PhaseHolder struct {
	mu       sync.RWMutex
	phase    Phase
	onChange func(old, cur Phase)
}

// OnChange registers a callback that fires when the phase changes.
// only one callback is supported; subsequent calls replace the previous one.
func (h *PhaseHolder) OnChange(fn func(old, cur Phase)) {
	h.mu.Lock()
	h.onChange = fn
	h.mu.Unlock()
}

// Set updates the current phase and fires the OnChange callback if the phase changed.
func (h *PhaseHolder) Set(p Phase) {
	h.mu.Lock()
	old := h.phase
	h.phase = p
	cb := h.onChange
	h.mu.Unlock()

	if old != p && cb != nil {
		cb(old, p)
	}
}

// Get returns the current phase.
func (h *PhaseHolder) Get() Phase {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.phase
}
