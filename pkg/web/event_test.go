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
