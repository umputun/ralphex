package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/processor/mocks"
	"github.com/umputun/ralphex/pkg/progress"
)

func TestNewBroadcastLogger(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc:     func(progress.Phase) {},
		PrintFunc:        func(string, ...any) {},
		PrintRawFunc:     func(string, ...any) {},
		PrintSectionFunc: func(string) {},
		PrintAlignedFunc: func(string) {},
		PathFunc:         func() string { return "/test/path" },
	}
	hub := NewHub()
	buffer := NewBuffer(100)

	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	assert.NotNil(t, bl)
	assert.Equal(t, progress.PhaseTask, bl.phase)
}

func TestBroadcastLogger_SetPhase(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc: func(progress.Phase) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	bl.SetPhase(progress.PhaseReview)

	assert.Equal(t, progress.PhaseReview, bl.phase)
	require.Len(t, mockLogger.SetPhaseCalls(), 1)
	assert.Equal(t, progress.PhaseReview, mockLogger.SetPhaseCalls()[0].Phase)
}

func TestBroadcastLogger_Print(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc: func(string, ...any) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	// subscribe to receive events
	ch := hub.Subscribe()

	bl.Print("hello %s", "world")

	// verify inner logger was called
	require.Len(t, mockLogger.PrintCalls(), 1)
	assert.Equal(t, "hello %s", mockLogger.PrintCalls()[0].Format)
	assert.Equal(t, []any{"world"}, mockLogger.PrintCalls()[0].Args)

	// verify event was broadcast
	select {
	case e := <-ch:
		assert.Equal(t, EventTypeOutput, e.Type)
		assert.Equal(t, "hello world", e.Text)
		assert.Equal(t, progress.PhaseTask, e.Phase)
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}

	// verify event was buffered
	events := buffer.All()
	require.Len(t, events, 1)
	assert.Equal(t, "hello world", events[0].Text)
}

func TestBroadcastLogger_PrintRaw(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintRawFunc: func(string, ...any) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch := hub.Subscribe()

	bl.PrintRaw("raw %d", 42)

	// verify inner logger was called
	require.Len(t, mockLogger.PrintRawCalls(), 1)
	assert.Equal(t, "raw %d", mockLogger.PrintRawCalls()[0].Format)

	// verify event was broadcast
	select {
	case e := <-ch:
		assert.Equal(t, EventTypeOutput, e.Type)
		assert.Equal(t, "raw 42", e.Text)
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestBroadcastLogger_PrintSection(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(string) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch := hub.Subscribe()

	bl.PrintSection("Test Section")

	// verify inner logger was called
	require.Len(t, mockLogger.PrintSectionCalls(), 1)
	assert.Equal(t, "Test Section", mockLogger.PrintSectionCalls()[0].Name)

	// verify event was broadcast with section type
	select {
	case e := <-ch:
		assert.Equal(t, EventTypeSection, e.Type)
		assert.Equal(t, "Test Section", e.Section)
		assert.Equal(t, "Test Section", e.Text)
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestBroadcastLogger_PrintAligned(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintAlignedFunc: func(string) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch := hub.Subscribe()

	bl.PrintAligned("aligned text")

	// verify inner logger was called
	require.Len(t, mockLogger.PrintAlignedCalls(), 1)
	assert.Equal(t, "aligned text", mockLogger.PrintAlignedCalls()[0].Text)

	// verify event was broadcast
	select {
	case e := <-ch:
		assert.Equal(t, EventTypeOutput, e.Type)
		assert.Equal(t, "aligned text", e.Text)
	case <-time.After(time.Second):
		t.Fatal("did not receive event")
	}
}

func TestBroadcastLogger_Path(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PathFunc: func() string { return "/test/progress.txt" },
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	path := bl.Path()

	assert.Equal(t, "/test/progress.txt", path)
	require.Len(t, mockLogger.PathCalls(), 1)
}

func TestBroadcastLogger_PhaseAffectsEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc: func(progress.Phase) {},
		PrintFunc:    func(string, ...any) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch := hub.Subscribe()

	// print with default phase (task)
	bl.Print("task message")
	e1 := <-ch
	assert.Equal(t, progress.PhaseTask, e1.Phase)

	// change phase and print again
	bl.SetPhase(progress.PhaseCodex)
	bl.Print("codex message")
	e2 := <-ch
	assert.Equal(t, progress.PhaseCodex, e2.Phase)
}

func TestBroadcastLogger_BufferAndHubBothReceive(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc: func(string, ...any) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	// subscribe two clients
	ch1 := hub.Subscribe()
	ch2 := hub.Subscribe()

	bl.Print("message for all")

	// both clients should receive
	e1 := <-ch1
	e2 := <-ch2
	assert.Equal(t, "message for all", e1.Text)
	assert.Equal(t, "message for all", e2.Text)

	// buffer should have the event
	events := buffer.All()
	require.Len(t, events, 1)
	assert.Equal(t, "message for all", events[0].Text)
}

func TestFormatText(t *testing.T) {
	tests := []struct {
		format string
		args   []any
		want   string
	}{
		{"plain text", nil, "plain text"},
		{"hello %s", []any{"world"}, "hello world"},
		{"num: %d", []any{42}, "num: 42"},
		{"%s has %d items", []any{"list", 3}, "list has 3 items"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatText(tt.format, tt.args...)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBroadcastLogger_PrintSection_TaskBoundaryEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(string) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch := hub.Subscribe()

	// emit task iteration section - should emit task start + section events
	bl.PrintSection("task iteration 1")

	// should receive task_start event first
	e1 := <-ch
	assert.Equal(t, EventTypeTaskStart, e1.Type)
	assert.Equal(t, 1, e1.TaskNum)
	assert.Equal(t, "task iteration 1", e1.Text)

	// then section event
	e2 := <-ch
	assert.Equal(t, EventTypeSection, e2.Type)
	assert.Equal(t, "task iteration 1", e2.Section)

	// emit another task iteration - should emit task end for previous, then task start
	bl.PrintSection("task iteration 2")

	// should receive task_end event for task 1
	e3 := <-ch
	assert.Equal(t, EventTypeTaskEnd, e3.Type)
	assert.Equal(t, 1, e3.TaskNum)

	// then task_start for task 2
	e4 := <-ch
	assert.Equal(t, EventTypeTaskStart, e4.Type)
	assert.Equal(t, 2, e4.TaskNum)

	// then section event
	e5 := <-ch
	assert.Equal(t, EventTypeSection, e5.Type)
}

func TestBroadcastLogger_PrintSection_IterationEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(string) {},
		SetPhaseFunc:     func(progress.Phase) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch := hub.Subscribe()

	// test claude review iteration pattern
	bl.SetPhase(progress.PhaseReview)
	bl.PrintSection("claude review 3: critical/major")

	e1 := <-ch
	assert.Equal(t, EventTypeIterationStart, e1.Type)
	assert.Equal(t, 3, e1.IterationNum)
	assert.Equal(t, progress.PhaseReview, e1.Phase)

	e2 := <-ch
	assert.Equal(t, EventTypeSection, e2.Type)

	// test codex iteration pattern
	bl.SetPhase(progress.PhaseCodex)
	bl.PrintSection("codex iteration 5")

	e3 := <-ch
	assert.Equal(t, EventTypeIterationStart, e3.Type)
	assert.Equal(t, 5, e3.IterationNum)
	assert.Equal(t, progress.PhaseCodex, e3.Phase)

	e4 := <-ch
	assert.Equal(t, EventTypeSection, e4.Type)
}

func TestBroadcastLogger_PrintSection_NoExtraEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(string) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch := hub.Subscribe()

	// regular section that doesn't match any pattern
	bl.PrintSection("review: all findings")

	// should only receive section event
	e1 := <-ch
	assert.Equal(t, EventTypeSection, e1.Type)
	assert.Equal(t, "review: all findings", e1.Section)

	// verify no more events
	select {
	case <-ch:
		t.Fatal("received unexpected event")
	case <-time.After(50 * time.Millisecond):
		// expected - no more events
	}
}
