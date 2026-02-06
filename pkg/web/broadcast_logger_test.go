package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/status"
	"github.com/umputun/ralphex/pkg/web/mocks"
)

func TestNewBroadcastLogger(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc:        func(string, ...any) {},
		PrintRawFunc:     func(string, ...any) {},
		PrintSectionFunc: func(status.Section) {},
		PrintAlignedFunc: func(string) {},
		PathFunc:         func() string { return "/test/path" },
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	assert.NotNil(t, bl)
	assert.Equal(t, status.Phase(""), holder.Get()) // default phase is empty
}

func TestBroadcastLogger_PhaseTransition_EmitsTaskEnd(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(status.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	// set task phase and start a task
	holder.Set(status.PhaseTask)
	bl.PrintSection(status.NewTaskIterationSection(1))

	// track current task
	assert.Equal(t, 1, bl.currentTask)

	// transition away from task phase - should reset currentTask via OnChange callback
	holder.Set(status.PhaseReview)

	// currentTask should be reset to 0
	assert.Equal(t, 0, bl.currentTask)
}

func TestBroadcastLogger_Print(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc: func(string, ...any) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

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

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	bl.PrintRaw("raw %d", 42)

	// verify inner logger was called
	require.Len(t, mockLogger.PrintRawCalls(), 1)
	assert.Equal(t, "raw %d", mockLogger.PrintRawCalls()[0].Format)
	assert.Equal(t, []any{42}, mockLogger.PrintRawCalls()[0].Args)
}

func TestBroadcastLogger_PrintSection(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(status.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	section := status.NewGenericSection("Test Section")
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

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

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

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	path := bl.Path()

	assert.Equal(t, "/test/progress.txt", path)
	require.Len(t, mockLogger.PathCalls(), 1)
}

func TestBroadcastLogger_PhaseAffectsEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintFunc: func(string, ...any) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	// print with default phase (empty)
	bl.Print("task message")
	assert.Equal(t, status.Phase(""), holder.Get())

	// change phase and verify it's updated
	holder.Set(status.PhaseCodex)
	assert.Equal(t, status.PhaseCodex, holder.Get())
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
		PrintSectionFunc: func(status.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	// emit task iteration section - should set currentTask
	bl.PrintSection(status.NewTaskIterationSection(1))
	assert.Equal(t, 1, bl.currentTask)

	// emit another task iteration - should update currentTask
	bl.PrintSection(status.NewTaskIterationSection(2))
	assert.Equal(t, 2, bl.currentTask)
}

func TestBroadcastLogger_PrintSection_IterationEvents(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		PrintSectionFunc: func(status.Section) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	// test claude review iteration pattern
	holder.Set(status.PhaseReview)
	bl.PrintSection(status.NewClaudeReviewSection(3, ": critical/major"))

	// verify inner logger was called
	require.Len(t, mockLogger.PrintSectionCalls(), 1)

	// test codex iteration pattern
	holder.Set(status.PhaseCodex)
	bl.PrintSection(status.NewCodexIterationSection(5))

	// verify inner logger was called again
	require.Len(t, mockLogger.PrintSectionCalls(), 2)
}

func TestBroadcastLogger_LogQuestion(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		LogQuestionFunc: func(string, []string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	bl.LogQuestion("Which database?", []string{"PostgreSQL", "MySQL", "SQLite"})

	require.Len(t, mockLogger.LogQuestionCalls(), 1)
	assert.Equal(t, "Which database?", mockLogger.LogQuestionCalls()[0].Question)
	assert.Equal(t, []string{"PostgreSQL", "MySQL", "SQLite"}, mockLogger.LogQuestionCalls()[0].Options)
}

func TestBroadcastLogger_LogAnswer(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		LogAnswerFunc: func(string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	bl.LogAnswer("PostgreSQL")

	require.Len(t, mockLogger.LogAnswerCalls(), 1)
	assert.Equal(t, "PostgreSQL", mockLogger.LogAnswerCalls()[0].Answer)
}

func TestBroadcastLogger_LogDraftReview_Accept(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		LogDraftReviewFunc: func(string, string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	bl.LogDraftReview("accept", "")

	require.Len(t, mockLogger.LogDraftReviewCalls(), 1)
	assert.Equal(t, "accept", mockLogger.LogDraftReviewCalls()[0].Action)
	assert.Empty(t, mockLogger.LogDraftReviewCalls()[0].Feedback)
}

func TestBroadcastLogger_LogDraftReview_ReviseWithFeedback(t *testing.T) {
	mockLogger := &mocks.LoggerMock{
		LogDraftReviewFunc: func(string, string) {},
	}
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	holder := &status.PhaseHolder{}
	bl := NewBroadcastLogger(mockLogger, session, holder)

	bl.LogDraftReview("revise", "Please add more details to Task 3")

	require.Len(t, mockLogger.LogDraftReviewCalls(), 1)
	assert.Equal(t, "revise", mockLogger.LogDraftReviewCalls()[0].Action)
	assert.Equal(t, "Please add more details to Task 3", mockLogger.LogDraftReviewCalls()[0].Feedback)
}

func TestExtractTerminalSignal(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		signal string
	}{
		{name: "completed", text: "task done " + status.Completed, signal: "COMPLETED"},
		{name: "failed", text: "task failed " + status.Failed, signal: "FAILED"},
		{name: "review-done", text: "review done " + status.ReviewDone, signal: "REVIEW_DONE"},
		{name: "codex-review-done", text: "codex done " + status.CodexDone, signal: "CODEX_REVIEW_DONE"},
		{name: "no signal", text: "regular output", signal: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTerminalSignal(tc.text)
			assert.Equal(t, tc.signal, got)
		})
	}
}
