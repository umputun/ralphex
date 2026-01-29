package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/processor"
)

func TestNewOutputEvent(t *testing.T) {
	before := time.Now()
	e := NewOutputEvent(processor.PhaseTask, "test message")
	after := time.Now()

	assert.Equal(t, EventTypeOutput, e.Type)
	assert.Equal(t, processor.PhaseTask, e.Phase)
	assert.Equal(t, "test message", e.Text)
	assert.True(t, e.Timestamp.After(before) || e.Timestamp.Equal(before))
	assert.True(t, e.Timestamp.Before(after) || e.Timestamp.Equal(after))
	assert.Empty(t, e.Section)
	assert.Empty(t, e.Signal)
}

func TestNewSectionEvent(t *testing.T) {
	e := NewSectionEvent(processor.PhaseReview, "Review Section")

	assert.Equal(t, EventTypeSection, e.Type)
	assert.Equal(t, processor.PhaseReview, e.Phase)
	assert.Equal(t, "Review Section", e.Section)
	assert.Equal(t, "Review Section", e.Text)
}

func TestNewErrorEvent(t *testing.T) {
	e := NewErrorEvent(processor.PhaseCodex, "something failed")

	assert.Equal(t, EventTypeError, e.Type)
	assert.Equal(t, processor.PhaseCodex, e.Phase)
	assert.Equal(t, "something failed", e.Text)
}

func TestNewWarnEvent(t *testing.T) {
	e := NewWarnEvent(processor.PhaseTask, "warning message")

	assert.Equal(t, EventTypeWarn, e.Type)
	assert.Equal(t, processor.PhaseTask, e.Phase)
	assert.Equal(t, "warning message", e.Text)
}

func TestNewSignalEvent(t *testing.T) {
	e := NewSignalEvent(processor.PhaseTask, "COMPLETED")

	assert.Equal(t, EventTypeSignal, e.Type)
	assert.Equal(t, processor.PhaseTask, e.Phase)
	assert.Equal(t, "COMPLETED", e.Text)
	assert.Equal(t, "COMPLETED", e.Signal)
}

func TestEvent_JSON(t *testing.T) {
	t.Run("output event serializes correctly", func(t *testing.T) {
		e := NewOutputEvent(processor.PhaseTask, "test output")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded Event
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, e.Type, decoded.Type)
		assert.Equal(t, e.Phase, decoded.Phase)
		assert.Equal(t, e.Text, decoded.Text)
	})

	t.Run("section event includes section field", func(t *testing.T) {
		e := NewSectionEvent(processor.PhaseReview, "Test Section")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "Test Section", decoded["section"])
	})

	t.Run("signal event includes signal field", func(t *testing.T) {
		e := NewSignalEvent(processor.PhaseTask, "DONE")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "DONE", decoded["signal"])
	})

	t.Run("omits empty fields", func(t *testing.T) {
		e := NewOutputEvent(processor.PhaseTask, "simple output")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		_, hasSection := decoded["section"]
		_, hasSignal := decoded["signal"]
		assert.False(t, hasSection, "section should be omitted")
		assert.False(t, hasSignal, "signal should be omitted")
	})
}

func TestEventType_Constants(t *testing.T) {
	// verify event type values for API stability
	assert.Equal(t, EventTypeOutput, EventType("output"))
	assert.Equal(t, EventTypeSection, EventType("section"))
	assert.Equal(t, EventTypeError, EventType("error"))
	assert.Equal(t, EventTypeWarn, EventType("warn"))
	assert.Equal(t, EventTypeSignal, EventType("signal"))
	assert.Equal(t, EventTypeTaskStart, EventType("task_start"))
	assert.Equal(t, EventTypeTaskEnd, EventType("task_end"))
	assert.Equal(t, EventTypeIterationStart, EventType("iteration_start"))
	assert.Equal(t, EventTypeQuestion, EventType("question"))
	assert.Equal(t, EventTypeQuestionAnswered, EventType("question_answered"))
}

func TestNewQuestionEvent(t *testing.T) {
	before := time.Now()
	e := NewQuestionEvent("q-123", "Which approach?", []string{"Option A", "Option B"}, "some context")
	after := time.Now()

	assert.Equal(t, EventTypeQuestion, e.Type)
	assert.Equal(t, "Which approach?", e.Text)
	assert.True(t, e.Timestamp.After(before) || e.Timestamp.Equal(before))
	assert.True(t, e.Timestamp.Before(after) || e.Timestamp.Equal(after))

	// verify question data
	require.NotNil(t, e.QuestionData)
	assert.Equal(t, "q-123", e.QuestionData.QuestionID)
	assert.Equal(t, "Which approach?", e.QuestionData.Question)
	assert.Equal(t, []string{"Option A", "Option B"}, e.QuestionData.Options)
	assert.Equal(t, "some context", e.QuestionData.Context)
}

func TestNewQuestionEvent_NoContext(t *testing.T) {
	e := NewQuestionEvent("q-456", "Pick one", []string{"A", "B", "C"}, "")

	assert.Equal(t, EventTypeQuestion, e.Type)
	require.NotNil(t, e.QuestionData)
	assert.Equal(t, "q-456", e.QuestionData.QuestionID)
	assert.Empty(t, e.QuestionData.Context)
}

func TestNewQuestionAnsweredEvent(t *testing.T) {
	before := time.Now()
	e := NewQuestionAnsweredEvent("q-999", "Option A")
	after := time.Now()

	assert.Equal(t, EventTypeQuestionAnswered, e.Type)
	assert.Equal(t, "Option A", e.Text)
	assert.True(t, e.Timestamp.After(before) || e.Timestamp.Equal(before))
	assert.True(t, e.Timestamp.Before(after) || e.Timestamp.Equal(after))

	require.NotNil(t, e.AnswerData)
	assert.Equal(t, "q-999", e.AnswerData.QuestionID)
	assert.Equal(t, "Option A", e.AnswerData.Answer)
}

func TestEvent_JSON_QuestionEvent(t *testing.T) {
	t.Run("question event includes question_data", func(t *testing.T) {
		e := NewQuestionEvent("q-789", "Choose", []string{"X", "Y"}, "context here")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		qd, ok := decoded["question_data"].(map[string]any)
		require.True(t, ok, "question_data should be a map")
		assert.Equal(t, "q-789", qd["question_id"])
		assert.Equal(t, "Choose", qd["question"])
		assert.Equal(t, []any{"X", "Y"}, qd["options"])
		assert.Equal(t, "context here", qd["context"])
	})

	t.Run("omits question_data when nil", func(t *testing.T) {
		e := NewOutputEvent(processor.PhaseTask, "regular output")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		_, hasQuestionData := decoded["question_data"]
		assert.False(t, hasQuestionData, "question_data should be omitted when nil")
	})

	t.Run("omits empty context in question_data", func(t *testing.T) {
		e := NewQuestionEvent("q-000", "Question", []string{"A"}, "")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		qd := decoded["question_data"].(map[string]any)
		_, hasContext := qd["context"]
		assert.False(t, hasContext, "context should be omitted when empty")
	})

	t.Run("answer event includes answer_data", func(t *testing.T) {
		e := NewQuestionAnsweredEvent("q-111", "B")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		ad, ok := decoded["answer_data"].(map[string]any)
		require.True(t, ok, "answer_data should be a map")
		assert.Equal(t, "q-111", ad["question_id"])
		assert.Equal(t, "B", ad["answer"])
	})

	t.Run("omits answer_data when nil", func(t *testing.T) {
		e := NewOutputEvent(processor.PhaseTask, "regular output")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		_, hasAnswerData := decoded["answer_data"]
		assert.False(t, hasAnswerData, "answer_data should be omitted when nil")
	})
}

func TestNewTaskStartEvent(t *testing.T) {
	before := time.Now()
	e := NewTaskStartEvent(processor.PhaseTask, 3, "task iteration 3")
	after := time.Now()

	assert.Equal(t, EventTypeTaskStart, e.Type)
	assert.Equal(t, processor.PhaseTask, e.Phase)
	assert.Equal(t, "task iteration 3", e.Text)
	assert.Equal(t, 3, e.TaskNum)
	assert.Zero(t, e.IterationNum)
	assert.True(t, e.Timestamp.After(before) || e.Timestamp.Equal(before))
	assert.True(t, e.Timestamp.Before(after) || e.Timestamp.Equal(after))
}

func TestNewTaskEndEvent(t *testing.T) {
	e := NewTaskEndEvent(processor.PhaseTask, 2, "task 2 completed")

	assert.Equal(t, EventTypeTaskEnd, e.Type)
	assert.Equal(t, processor.PhaseTask, e.Phase)
	assert.Equal(t, "task 2 completed", e.Text)
	assert.Equal(t, 2, e.TaskNum)
	assert.Zero(t, e.IterationNum)
}

func TestNewIterationStartEvent(t *testing.T) {
	e := NewIterationStartEvent(processor.PhaseReview, 5, "claude review 5: critical/major")

	assert.Equal(t, EventTypeIterationStart, e.Type)
	assert.Equal(t, processor.PhaseReview, e.Phase)
	assert.Equal(t, "claude review 5: critical/major", e.Text)
	assert.Equal(t, 5, e.IterationNum)
	assert.Zero(t, e.TaskNum)
}

func TestEvent_JSON_TaskAndIterationFields(t *testing.T) {
	t.Run("task event includes task_num", func(t *testing.T) {
		e := NewTaskStartEvent(processor.PhaseTask, 7, "task iteration 7")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.InDelta(t, 7, decoded["task_num"], 0.001)
	})

	t.Run("iteration event includes iteration_num", func(t *testing.T) {
		e := NewIterationStartEvent(processor.PhaseCodex, 3, "codex iteration 3")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.InDelta(t, 3, decoded["iteration_num"], 0.001)
	})

	t.Run("omits zero task_num and iteration_num", func(t *testing.T) {
		e := NewOutputEvent(processor.PhaseTask, "simple output")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		_, hasTaskNum := decoded["task_num"]
		_, hasIterationNum := decoded["iteration_num"]
		assert.False(t, hasTaskNum, "task_num should be omitted when zero")
		assert.False(t, hasIterationNum, "iteration_num should be omitted when zero")
	})
}

func TestEvent_ToSSEMessage(t *testing.T) {
	t.Run("converts output event to SSE message", func(t *testing.T) {
		e := NewOutputEvent(processor.PhaseTask, "test message")
		msg := e.ToSSEMessage()

		// no SSE event type set (onmessage only catches typeless events)
		assert.Empty(t, msg.Type.String())

		// verify the message can be serialized with JSON data
		data, err := msg.MarshalText()
		require.NoError(t, err)
		assert.Contains(t, string(data), "test message")
		assert.Contains(t, string(data), `"type":"output"`) // type is in JSON payload
	})

	t.Run("converts signal event to SSE message", func(t *testing.T) {
		e := NewSignalEvent(processor.PhaseTask, "COMPLETED")
		msg := e.ToSSEMessage()

		data, err := msg.MarshalText()
		require.NoError(t, err)
		assert.Contains(t, string(data), "COMPLETED")
		assert.Contains(t, string(data), `"type":"signal"`)
	})

	t.Run("converts section event to SSE message", func(t *testing.T) {
		e := NewSectionEvent(processor.PhaseReview, "Review Section")
		msg := e.ToSSEMessage()

		data, err := msg.MarshalText()
		require.NoError(t, err)
		assert.Contains(t, string(data), "Review Section")
		assert.Contains(t, string(data), `"type":"section"`)
	})

	t.Run("data field contains full JSON event", func(t *testing.T) {
		e := NewTaskStartEvent(processor.PhaseTask, 3, "task iteration 3")
		msg := e.ToSSEMessage()

		data, err := msg.MarshalText()
		require.NoError(t, err)

		// should contain JSON with task_num field
		assert.Contains(t, string(data), "task_num")
		assert.Contains(t, string(data), "task_start")
	})
}
