package web

import (
	"sync"

	"github.com/umputun/ralphex/pkg/progress"
)

// DefaultBufferSize is the default maximum number of events to keep in the buffer.
const DefaultBufferSize = 10000

// Buffer is a thread-safe ring buffer for storing events with phase indexing.
// supports quick filtering by phase for clients that join late.
type Buffer struct {
	mu       sync.RWMutex
	events   []Event
	maxSize  int
	writePos int // next position to write (wraps around)
	count    int // total events written (for full detection)

	// phase indexes store positions of events by phase for quick filtering
	phaseIndex map[progress.Phase][]int
}

// NewBuffer creates a new ring buffer with the specified max size.
// if maxSize is 0, DefaultBufferSize is used.
func NewBuffer(maxSize int) *Buffer {
	if maxSize <= 0 {
		maxSize = DefaultBufferSize
	}
	return &Buffer{
		events:     make([]Event, maxSize),
		maxSize:    maxSize,
		phaseIndex: make(map[progress.Phase][]int),
	}
}

// Add appends an event to the buffer, overwriting oldest if full.
func (b *Buffer) Add(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// if buffer is full, clean up old index entry BEFORE overwriting
	if b.count >= b.maxSize {
		b.cleanOldIndexEntry(b.writePos)
	}

	// store event at current write position
	b.events[b.writePos] = e

	// update phase index
	b.phaseIndex[e.Phase] = append(b.phaseIndex[e.Phase], b.writePos)

	// advance write position (wrap around)
	b.writePos = (b.writePos + 1) % b.maxSize
	b.count++
}

// cleanOldIndexEntry removes stale index entries for the position being overwritten.
// must be called with lock held.
func (b *Buffer) cleanOldIndexEntry(pos int) {
	oldEvent := b.events[pos]
	if indices, ok := b.phaseIndex[oldEvent.Phase]; ok {
		// find and remove the old position from index
		// phase index entries are in chronological order, so old ones are at the start
		newIndices := make([]int, 0, len(indices))
		for _, idx := range indices {
			if idx != pos {
				newIndices = append(newIndices, idx)
			}
		}
		if len(newIndices) == 0 {
			delete(b.phaseIndex, oldEvent.Phase)
		} else {
			b.phaseIndex[oldEvent.Phase] = newIndices
		}
	}
}

// All returns all events in chronological order.
func (b *Buffer) All() []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.count == 0 {
		return nil
	}

	// determine actual count (min of count and maxSize)
	actualCount := min(b.count, b.maxSize)

	result := make([]Event, actualCount)

	if b.count <= b.maxSize {
		// buffer not full yet, just copy from start
		copy(result, b.events[:b.count])
	} else {
		// buffer wrapped, read from writePos to end, then start to writePos
		tailLen := b.maxSize - b.writePos
		copy(result[:tailLen], b.events[b.writePos:])
		copy(result[tailLen:], b.events[:b.writePos])
	}

	return result
}

// ByPhase returns all events for the given phase in chronological order.
func (b *Buffer) ByPhase(phase progress.Phase) []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()

	indices, ok := b.phaseIndex[phase]
	if !ok || len(indices) == 0 {
		return nil
	}

	// convert indices to events in chronological order
	// note: indices are added chronologically, so we need to sort by buffer position
	// taking into account wraparound

	result := make([]Event, len(indices))
	for i, idx := range indices {
		result[i] = b.events[idx]
	}

	// sort by timestamp to ensure chronological order (handles wraparound correctly)
	// using simple insertion sort as arrays are typically small
	for i := 1; i < len(result); i++ {
		j := i
		for j > 0 && result[j].Timestamp.Before(result[j-1].Timestamp) {
			result[j], result[j-1] = result[j-1], result[j]
			j--
		}
	}

	return result
}

// Count returns the total number of events currently in the buffer.
func (b *Buffer) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.count > b.maxSize {
		return b.maxSize
	}
	return b.count
}

// Clear removes all events from the buffer.
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.events = make([]Event, b.maxSize)
	b.writePos = 0
	b.count = 0
	b.phaseIndex = make(map[progress.Phase][]int)
}
