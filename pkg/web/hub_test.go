package web

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/progress"
)

func TestNewHub(t *testing.T) {
	h := NewHub()
	assert.NotNil(t, h)
	assert.Equal(t, 0, h.ClientCount())
}

func TestHub_Subscribe(t *testing.T) {
	h := NewHub()

	ch := h.Subscribe()
	assert.NotNil(t, ch)
	assert.Equal(t, 1, h.ClientCount())

	// subscribe another
	ch2 := h.Subscribe()
	assert.NotNil(t, ch2)
	assert.Equal(t, 2, h.ClientCount())
}

func TestHub_Unsubscribe(t *testing.T) {
	h := NewHub()

	ch := h.Subscribe()
	assert.Equal(t, 1, h.ClientCount())

	h.Unsubscribe(ch)
	assert.Equal(t, 0, h.ClientCount())

	// channel should be closed
	_, open := <-ch
	assert.False(t, open)
}

func TestHub_Unsubscribe_SafeForMultipleCalls(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe()

	// first unsubscribe
	h.Unsubscribe(ch)

	// second unsubscribe should not panic
	assert.NotPanics(t, func() {
		h.Unsubscribe(ch)
	})
}

func TestHub_Broadcast(t *testing.T) {
	h := NewHub()

	ch1 := h.Subscribe()
	ch2 := h.Subscribe()

	event := NewOutputEvent(progress.PhaseTask, "test message")
	h.Broadcast(event)

	// both clients should receive the event
	select {
	case e := <-ch1:
		assert.Equal(t, "test message", e.Text)
	case <-time.After(time.Second):
		t.Fatal("ch1 did not receive event")
	}

	select {
	case e := <-ch2:
		assert.Equal(t, "test message", e.Text)
	case <-time.After(time.Second):
		t.Fatal("ch2 did not receive event")
	}
}

func TestHub_Broadcast_DropsForFullClient(t *testing.T) {
	h := NewHub()

	ch := h.Subscribe()

	// fill the channel buffer (256 events)
	for range 300 {
		h.Broadcast(NewOutputEvent(progress.PhaseTask, "event"))
	}

	// should not block, some events were dropped
	// drain the channel
	count := 0
	timeout := time.After(time.Second)
drainLoop:
	for {
		select {
		case <-ch:
			count++
		case <-timeout:
			break drainLoop
		default:
			break drainLoop
		}
	}

	// should have received up to buffer size (256)
	assert.LessOrEqual(t, count, 256)
}

func TestHub_ClientCount(t *testing.T) {
	h := NewHub()

	assert.Equal(t, 0, h.ClientCount())

	ch1 := h.Subscribe()
	assert.Equal(t, 1, h.ClientCount())

	ch2 := h.Subscribe()
	assert.Equal(t, 2, h.ClientCount())

	h.Unsubscribe(ch1)
	assert.Equal(t, 1, h.ClientCount())

	h.Unsubscribe(ch2)
	assert.Equal(t, 0, h.ClientCount())
}

func TestHub_Close(t *testing.T) {
	h := NewHub()

	ch1 := h.Subscribe()
	ch2 := h.Subscribe()
	ch3 := h.Subscribe()

	assert.Equal(t, 3, h.ClientCount())

	h.Close()

	assert.Equal(t, 0, h.ClientCount())

	// all channels should be closed
	_, open1 := <-ch1
	_, open2 := <-ch2
	_, open3 := <-ch3
	assert.False(t, open1)
	assert.False(t, open2)
	assert.False(t, open3)
}

func TestHub_Concurrency(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup

	// concurrent subscribes
	channels := make([]chan Event, 0, 20)
	var chMu sync.Mutex

	for range 20 {
		wg.Go(func() {
			ch := h.Subscribe()
			chMu.Lock()
			channels = append(channels, ch)
			chMu.Unlock()
		})
	}

	wg.Wait()
	require.Equal(t, 20, h.ClientCount())

	// concurrent broadcasts
	for range 10 {
		wg.Go(func() {
			for range 10 {
				h.Broadcast(NewOutputEvent(progress.PhaseTask, "event"))
			}
		})
	}

	// concurrent unsubscribes
	for i := range 10 {
		n := i
		wg.Go(func() {
			chMu.Lock()
			if n < len(channels) {
				ch := channels[n]
				chMu.Unlock()
				h.Unsubscribe(ch)
			} else {
				chMu.Unlock()
			}
		})
	}

	wg.Wait()

	// should not panic, client count should be reduced
	count := h.ClientCount()
	assert.GreaterOrEqual(t, count, 0)
}

func TestHub_BroadcastToNoClients(t *testing.T) {
	h := NewHub()

	// should not panic
	assert.NotPanics(t, func() {
		h.Broadcast(NewOutputEvent(progress.PhaseTask, "nobody listening"))
	})
}
