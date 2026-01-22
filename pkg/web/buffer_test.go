package web

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/progress"
)

func TestNewBuffer(t *testing.T) {
	t.Run("uses default size when zero", func(t *testing.T) {
		b := NewBuffer(0)
		assert.Equal(t, DefaultBufferSize, b.maxSize)
	})

	t.Run("uses default size when negative", func(t *testing.T) {
		b := NewBuffer(-1)
		assert.Equal(t, DefaultBufferSize, b.maxSize)
	})

	t.Run("uses specified size", func(t *testing.T) {
		b := NewBuffer(100)
		assert.Equal(t, 100, b.maxSize)
	})
}

func TestBuffer_Add(t *testing.T) {
	t.Run("adds events", func(t *testing.T) {
		b := NewBuffer(10)

		e1 := NewOutputEvent(progress.PhaseTask, "first")
		e2 := NewOutputEvent(progress.PhaseTask, "second")

		b.Add(e1)
		b.Add(e2)

		assert.Equal(t, 2, b.Count())
	})

	t.Run("overwrites oldest when full", func(t *testing.T) {
		b := NewBuffer(3)

		b.Add(NewOutputEvent(progress.PhaseTask, "first"))
		b.Add(NewOutputEvent(progress.PhaseTask, "second"))
		b.Add(NewOutputEvent(progress.PhaseTask, "third"))
		b.Add(NewOutputEvent(progress.PhaseTask, "fourth"))

		events := b.All()
		require.Len(t, events, 3)
		// oldest (first) should be gone
		assert.Equal(t, "second", events[0].Text)
		assert.Equal(t, "third", events[1].Text)
		assert.Equal(t, "fourth", events[2].Text)
	})
}

func TestBuffer_All(t *testing.T) {
	t.Run("returns nil for empty buffer", func(t *testing.T) {
		b := NewBuffer(10)
		assert.Nil(t, b.All())
	})

	t.Run("returns all events in order", func(t *testing.T) {
		b := NewBuffer(10)

		b.Add(NewOutputEvent(progress.PhaseTask, "first"))
		time.Sleep(time.Millisecond) // ensure different timestamps
		b.Add(NewOutputEvent(progress.PhaseReview, "second"))
		time.Sleep(time.Millisecond)
		b.Add(NewOutputEvent(progress.PhaseCodex, "third"))

		events := b.All()
		require.Len(t, events, 3)
		assert.Equal(t, "first", events[0].Text)
		assert.Equal(t, "second", events[1].Text)
		assert.Equal(t, "third", events[2].Text)
	})

	t.Run("returns events in order after wrap", func(t *testing.T) {
		b := NewBuffer(3)

		// add 5 events to wrap around
		for i := range 5 {
			time.Sleep(time.Millisecond)
			b.Add(NewOutputEvent(progress.PhaseTask, string(rune('A'+i))))
		}

		events := b.All()
		require.Len(t, events, 3)
		// should have C, D, E (A and B were overwritten)
		assert.Equal(t, "C", events[0].Text)
		assert.Equal(t, "D", events[1].Text)
		assert.Equal(t, "E", events[2].Text)
	})
}

func TestBuffer_ByPhase(t *testing.T) {
	t.Run("returns nil for empty phase", func(t *testing.T) {
		b := NewBuffer(10)
		assert.Nil(t, b.ByPhase(progress.PhaseTask))
	})

	t.Run("filters by phase", func(t *testing.T) {
		b := NewBuffer(10)

		b.Add(NewOutputEvent(progress.PhaseTask, "task1"))
		time.Sleep(time.Millisecond)
		b.Add(NewOutputEvent(progress.PhaseReview, "review1"))
		time.Sleep(time.Millisecond)
		b.Add(NewOutputEvent(progress.PhaseTask, "task2"))
		time.Sleep(time.Millisecond)
		b.Add(NewOutputEvent(progress.PhaseCodex, "codex1"))

		taskEvents := b.ByPhase(progress.PhaseTask)
		require.Len(t, taskEvents, 2)
		assert.Equal(t, "task1", taskEvents[0].Text)
		assert.Equal(t, "task2", taskEvents[1].Text)

		reviewEvents := b.ByPhase(progress.PhaseReview)
		require.Len(t, reviewEvents, 1)
		assert.Equal(t, "review1", reviewEvents[0].Text)
	})

	t.Run("handles phase after wrap", func(t *testing.T) {
		b := NewBuffer(4)

		b.Add(NewOutputEvent(progress.PhaseTask, "task1"))
		time.Sleep(time.Millisecond)
		b.Add(NewOutputEvent(progress.PhaseReview, "review1"))
		time.Sleep(time.Millisecond)
		b.Add(NewOutputEvent(progress.PhaseTask, "task2"))
		time.Sleep(time.Millisecond)
		b.Add(NewOutputEvent(progress.PhaseReview, "review2"))
		time.Sleep(time.Millisecond)
		// this overwrites task1
		b.Add(NewOutputEvent(progress.PhaseTask, "task3"))

		taskEvents := b.ByPhase(progress.PhaseTask)
		// task1 was overwritten, should have task2 and task3
		require.Len(t, taskEvents, 2)
		assert.Equal(t, "task2", taskEvents[0].Text)
		assert.Equal(t, "task3", taskEvents[1].Text)
	})
}

func TestBuffer_Count(t *testing.T) {
	t.Run("returns zero for empty buffer", func(t *testing.T) {
		b := NewBuffer(10)
		assert.Equal(t, 0, b.Count())
	})

	t.Run("returns correct count", func(t *testing.T) {
		b := NewBuffer(10)
		b.Add(NewOutputEvent(progress.PhaseTask, "one"))
		b.Add(NewOutputEvent(progress.PhaseTask, "two"))
		assert.Equal(t, 2, b.Count())
	})

	t.Run("caps at max size", func(t *testing.T) {
		b := NewBuffer(3)
		for range 10 {
			b.Add(NewOutputEvent(progress.PhaseTask, "event"))
		}
		assert.Equal(t, 3, b.Count())
	})
}

func TestBuffer_Clear(t *testing.T) {
	b := NewBuffer(10)
	b.Add(NewOutputEvent(progress.PhaseTask, "one"))
	b.Add(NewOutputEvent(progress.PhaseTask, "two"))

	b.Clear()

	assert.Equal(t, 0, b.Count())
	assert.Nil(t, b.All())
	assert.Nil(t, b.ByPhase(progress.PhaseTask))
}

func TestBuffer_Concurrency(t *testing.T) {
	b := NewBuffer(100)
	var wg sync.WaitGroup

	// concurrent writes
	for range 10 {
		wg.Go(func() {
			for range 100 {
				b.Add(NewOutputEvent(progress.PhaseTask, "event"))
			}
		})
	}

	// concurrent reads
	for range 5 {
		wg.Go(func() {
			for range 50 {
				_ = b.All()
				_ = b.ByPhase(progress.PhaseTask)
				_ = b.Count()
			}
		})
	}

	wg.Wait()

	// should not panic and have valid count
	count := b.Count()
	assert.Positive(t, count)
	assert.LessOrEqual(t, count, 100)
}

func TestBuffer_PhaseIndexCleanup(t *testing.T) {
	// test that phase index is properly cleaned up on wrap
	b := NewBuffer(2)

	b.Add(NewOutputEvent(progress.PhaseTask, "task1"))
	b.Add(NewOutputEvent(progress.PhaseReview, "review1"))
	// this overwrites task1
	b.Add(NewOutputEvent(progress.PhaseCodex, "codex1"))

	taskEvents := b.ByPhase(progress.PhaseTask)
	// task1 was overwritten, phase index should be cleaned
	assert.Empty(t, taskEvents)
}
