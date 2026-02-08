package status

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPhaseHolder_SetGet(t *testing.T) {
	h := &PhaseHolder{}
	assert.Equal(t, Phase(""), h.Get())

	h.Set(PhaseTask)
	assert.Equal(t, PhaseTask, h.Get())

	h.Set(PhaseReview)
	assert.Equal(t, PhaseReview, h.Get())
}

func TestPhaseHolder_OnChange_Fires(t *testing.T) {
	h := &PhaseHolder{}

	var captured []struct{ old, cur Phase }
	h.OnChange(func(old, cur Phase) {
		captured = append(captured, struct{ old, cur Phase }{old, cur})
	})

	h.Set(PhaseTask)
	h.Set(PhaseReview)

	require.Len(t, captured, 2)
	assert.Equal(t, Phase(""), captured[0].old)
	assert.Equal(t, PhaseTask, captured[0].cur)
	assert.Equal(t, PhaseTask, captured[1].old)
	assert.Equal(t, PhaseReview, captured[1].cur)
}

func TestPhaseHolder_OnChange_NotFiredOnSamePhase(t *testing.T) {
	h := &PhaseHolder{}

	callCount := 0
	h.OnChange(func(_, _ Phase) { callCount++ })

	h.Set(PhaseTask)
	h.Set(PhaseTask) // same phase - should not fire

	assert.Equal(t, 1, callCount)
}

func TestPhaseHolder_OnChange_NilCallbackSafe(t *testing.T) {
	h := &PhaseHolder{}
	// no callback registered - should not panic
	h.Set(PhaseTask)
	assert.Equal(t, PhaseTask, h.Get())
}

func TestPhaseHolder_ConcurrentAccess(t *testing.T) {
	h := &PhaseHolder{}
	phases := []Phase{PhaseTask, PhaseReview, PhaseCodex, PhaseClaudeEval, PhaseFinalize}

	var cbCount atomic.Int64
	h.OnChange(func(_, _ Phase) {
		_ = h.Get() // exercise read path from callback (deadlock risk if lock held)
		cbCount.Add(1)
	})

	start := make(chan struct{})
	var wg sync.WaitGroup

	workers := 32
	iters := 500
	for w := range workers {
		wg.Go(func() {
			<-start
			for i := range iters {
				h.Set(phases[(w+i)%len(phases)])
				h.Get()
			}
		})
	}

	close(start)
	wg.Wait()

	got := h.Get()
	assert.Contains(t, phases, got)
	assert.Positive(t, cbCount.Load())
}
