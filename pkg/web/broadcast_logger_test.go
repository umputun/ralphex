package web

import (
	"testing"

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
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	bl := NewBroadcastLogger(mockLogger, session)

	assert.NotNil(t, bl)
	assert.Equal(t, processor.PhaseTask, bl.phase)
}

func TestBroadcastLogger_SetPhase(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc: func(processor.Phase) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.SetPhase(processor.PhaseReview)

	assert.Equal(t, processor.PhaseReview, bl.phase)
	require.Len(t, mockLogger.SetPhaseCalls(), 1)
	assert.Equal(t, processor.PhaseReview, mockLogger.SetPhaseCalls()[0].Phase)
}

func TestBroadcastLogger_SetPhase_EmitsTaskEnd(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc:     func(processor.Phase) {},
		PrintSectionFunc: func(processor.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// set task phase and start a task
	bl.SetPhase(processor.PhaseTask)
	bl.PrintSection(processor.NewTaskIterationSection(1))

	// track current task
	assert.Equal(t, 1, bl.currentTask)

	// transition away from task phase - should reset currentTask
	bl.SetPhase(processor.PhaseReview)

	// currentTask should be reset to 0
	assert.Equal(t, 0, bl.currentTask)
}

func TestBroadcastLogger_Print(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc: func(string, ...any) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.Print("hello %s", "world")

	// verify inner logger was called
	require.Len(t, mockLogger.PrintCalls(), 1)
	assert.Equal(t, "hello %s", mockLogger.PrintCalls()[0].Format)
	assert.Equal(t, []any{"world"}, mockLogger.PrintCalls()[0].Args)
}

func TestBroadcastLogger_PrintRaw(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintRawFunc: func(string, ...any) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.PrintRaw("raw %d", 42)

	// verify inner logger was called
	require.Len(t, mockLogger.PrintRawCalls(), 1)
	assert.Equal(t, "raw %d", mockLogger.PrintRawCalls()[0].Format)
	assert.Equal(t, []any{42}, mockLogger.PrintRawCalls()[0].Args)
}

func TestBroadcastLogger_PrintSection(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(processor.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	section := processor.NewGenericSection("Test Section")
	bl.PrintSection(section)

	// verify inner logger was called
	require.Len(t, mockLogger.PrintSectionCalls(), 1)
	assert.Equal(t, "Test Section", mockLogger.PrintSectionCalls()[0].Section.Label)
}

func TestBroadcastLogger_PrintAligned(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintAlignedFunc: func(string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	bl.PrintAligned("aligned text")

	// verify inner logger was called
	require.Len(t, mockLogger.PrintAlignedCalls(), 1)
	assert.Equal(t, "aligned text", mockLogger.PrintAlignedCalls()[0].Text)
}

func TestBroadcastLogger_Path(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PathFunc: func() string { return "/test/progress.txt" },
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	path := bl.Path()

	assert.Equal(t, "/test/progress.txt", path)
	require.Len(t, mockLogger.PathCalls(), 1)
}

func TestBroadcastLogger_PhaseAffectsEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		SetPhaseFunc: func(processor.Phase) {},
		PrintFunc:    func(string, ...any) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// print with default phase (task)
	bl.Print("task message")
	assert.Equal(t, processor.PhaseTask, bl.phase)

	// change phase and verify it's updated
	bl.SetPhase(processor.PhaseCodex)
	assert.Equal(t, processor.PhaseCodex, bl.phase)
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
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// emit task iteration section - should set currentTask
	bl.PrintSection(processor.NewTaskIterationSection(1))
	assert.Equal(t, 1, bl.currentTask)

	// emit another task iteration - should update currentTask
	bl.PrintSection(processor.NewTaskIterationSection(2))
	assert.Equal(t, 2, bl.currentTask)
}

func TestBroadcastLogger_PrintSection_IterationEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(processor.Section) {},
		SetPhaseFunc:     func(processor.Phase) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	bl := NewBroadcastLogger(mockLogger, session)

	// test claude review iteration pattern
	bl.SetPhase(processor.PhaseReview)
	bl.PrintSection(processor.NewClaudeReviewSection(3, ": critical/major"))

	// verify inner logger was called
	require.Len(t, mockLogger.PrintSectionCalls(), 1)

	// test codex iteration pattern
	bl.SetPhase(processor.PhaseCodex)
	bl.PrintSection(processor.NewCodexIterationSection(5))

	// verify inner logger was called again
	require.Len(t, mockLogger.PrintSectionCalls(), 2)
}

func TestExtractTerminalSignal(t *testing.T) {
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
			name:   "failed",
			text:   "task failed <<<RALPHEX:TASK_FAILED>>>",
			signal: "FAILED",
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
		{
			name:   "no signal",
			text:   "regular output",
			signal: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTerminalSignal(tc.text)
			assert.Equal(t, tc.signal, got)
		})
	}
}
