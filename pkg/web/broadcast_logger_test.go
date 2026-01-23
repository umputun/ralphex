package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
)

func TestNewBroadcastLogger(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc:     func(processor.Phase) {},
		PrintFunc:        func(string, ...any) {},
		PrintRawFunc:     func(string, ...any) {},
		PrintSectionFunc: func(processor.Section) {},
		PrintAlignedFunc: func(string) {},
		PathFunc:         func() string { return "/test/path" },
	}
	hub := NewHub()
	buffer := NewBuffer(100)

	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	assert.NotNil(t, bl)
	assert.Equal(t, processor.PhaseTask, bl.phase)
}

func TestBroadcastLogger_SetPhase(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc: func(processor.Phase) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	bl.SetPhase(processor.PhaseReview)

	assert.Equal(t, processor.PhaseReview, bl.phase)
	require.Len(t, mockLogger.SetPhaseCalls(), 1)
	assert.Equal(t, processor.PhaseReview, mockLogger.SetPhaseCalls()[0].Phase)
}

func TestBroadcastLogger_Print(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc: func(string, ...any) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	// subscribe to receive events
	ch, err := hub.Subscribe()
	require.NoError(t, err)

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
		assert.Equal(t, processor.PhaseTask, e.Phase)
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

	ch, err := hub.Subscribe()
	require.NoError(t, err)

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
		PrintSectionFunc: func(processor.Section) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch, err := hub.Subscribe()
	require.NoError(t, err)

	section := processor.NewGenericSection("Test Section")
	bl.PrintSection(section)

	// verify inner logger was called
	require.Len(t, mockLogger.PrintSectionCalls(), 1)
	assert.Equal(t, "Test Section", mockLogger.PrintSectionCalls()[0].Section.Label)

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

	ch, err := hub.Subscribe()
	require.NoError(t, err)

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

func TestBroadcastLogger_PrintAligned_Signal(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		signal string
	}{
		{
			name:   "completed",
			text:   "task done <<<RALPHEX:ALL_TASKS_DONE>>>",
			signal: "COMPLETED",
		},
		{
			name:   "review-done",
			text:   "review done <<<RALPHEX:REVIEW_DONE>>>",
			signal: "REVIEW_DONE",
		},
		{
			name:   "codex-review-done",
			text:   "codex done <<<RALPHEX:CODEX_REVIEW_DONE>>>",
			signal: "CODEX_REVIEW_DONE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockLogger := &mocks.LoggerMock{
				PrintAlignedFunc: func(string) {},
			}
			hub := NewHub()
			buffer := NewBuffer(100)
			bl := NewBroadcastLogger(mockLogger, hub, buffer)

			ch, err := hub.Subscribe()
			require.NoError(t, err)

			bl.PrintAligned(tc.text)

			select {
			case e1 := <-ch:
				assert.Equal(t, EventTypeOutput, e1.Type)
			case <-time.After(time.Second):
				t.Fatal("did not receive output event")
			}

			select {
			case e2 := <-ch:
				assert.Equal(t, EventTypeSignal, e2.Type)
				assert.Equal(t, tc.signal, e2.Signal)
			case <-time.After(time.Second):
				t.Fatal("did not receive signal event")
			}
		})
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
		SetPhaseFunc: func(processor.Phase) {},
		PrintFunc:    func(string, ...any) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch, err := hub.Subscribe()
	require.NoError(t, err)

	// print with default phase (task)
	bl.Print("task message")
	e1 := <-ch
	assert.Equal(t, processor.PhaseTask, e1.Phase)

	// change phase and print again
	bl.SetPhase(processor.PhaseCodex)
	bl.Print("codex message")
	e2 := <-ch
	assert.Equal(t, processor.PhaseCodex, e2.Phase)
}

func TestBroadcastLogger_BufferAndHubBothReceive(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc: func(string, ...any) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	// subscribe two clients
	ch1, err := hub.Subscribe()
	require.NoError(t, err)
	ch2, err := hub.Subscribe()
	require.NoError(t, err)

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
		PrintSectionFunc: func(processor.Section) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch, err := hub.Subscribe()
	require.NoError(t, err)

	// emit task iteration section - should emit task start + section events
	bl.PrintSection(processor.NewTaskIterationSection(1))

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
	bl.PrintSection(processor.NewTaskIterationSection(2))

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
		PrintSectionFunc: func(processor.Section) {},
		SetPhaseFunc:     func(processor.Phase) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch, err := hub.Subscribe()
	require.NoError(t, err)

	// test claude review iteration pattern
	bl.SetPhase(processor.PhaseReview)
	bl.PrintSection(processor.NewClaudeReviewSection(3, ": critical/major"))

	e1 := <-ch
	assert.Equal(t, EventTypeIterationStart, e1.Type)
	assert.Equal(t, 3, e1.IterationNum)
	assert.Equal(t, processor.PhaseReview, e1.Phase)

	e2 := <-ch
	assert.Equal(t, EventTypeSection, e2.Type)

	// test codex iteration pattern
	bl.SetPhase(processor.PhaseCodex)
	bl.PrintSection(processor.NewCodexIterationSection(5))

	e3 := <-ch
	assert.Equal(t, EventTypeIterationStart, e3.Type)
	assert.Equal(t, 5, e3.IterationNum)
	assert.Equal(t, processor.PhaseCodex, e3.Phase)

	e4 := <-ch
	assert.Equal(t, EventTypeSection, e4.Type)
}

func TestBroadcastLogger_PrintSection_NoExtraEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(processor.Section) {},
	}
	hub := NewHub()
	buffer := NewBuffer(100)
	bl := NewBroadcastLogger(mockLogger, hub, buffer)

	ch, err := hub.Subscribe()
	require.NoError(t, err)

	// regular section that doesn't match any iteration pattern
	bl.PrintSection(processor.NewGenericSection("review: all findings"))

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
