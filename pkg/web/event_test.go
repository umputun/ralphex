package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/progress"
)

func TestNewOutputEvent(t *testing.T) {
	before := time.Now()
	e := NewOutputEvent(progress.PhaseTask, "test message")
	after := time.Now()

	assert.Equal(t, EventTypeOutput, e.Type)
	assert.Equal(t, progress.PhaseTask, e.Phase)
	assert.Equal(t, "test message", e.Text)
	assert.True(t, e.Timestamp.After(before) || e.Timestamp.Equal(before))
	assert.True(t, e.Timestamp.Before(after) || e.Timestamp.Equal(after))
	assert.Empty(t, e.Section)
	assert.Empty(t, e.Signal)
}

func TestNewSectionEvent(t *testing.T) {
	e := NewSectionEvent(progress.PhaseReview, "Review Section")

	assert.Equal(t, EventTypeSection, e.Type)
	assert.Equal(t, progress.PhaseReview, e.Phase)
	assert.Equal(t, "Review Section", e.Section)
	assert.Equal(t, "Review Section", e.Text)
}

func TestNewErrorEvent(t *testing.T) {
	e := NewErrorEvent(progress.PhaseCodex, "something failed")

	assert.Equal(t, EventTypeError, e.Type)
	assert.Equal(t, progress.PhaseCodex, e.Phase)
	assert.Equal(t, "something failed", e.Text)
}

func TestNewWarnEvent(t *testing.T) {
	e := NewWarnEvent(progress.PhaseTask, "warning message")

	assert.Equal(t, EventTypeWarn, e.Type)
	assert.Equal(t, progress.PhaseTask, e.Phase)
	assert.Equal(t, "warning message", e.Text)
}

func TestNewSignalEvent(t *testing.T) {
	e := NewSignalEvent(progress.PhaseTask, "COMPLETED")

	assert.Equal(t, EventTypeSignal, e.Type)
	assert.Equal(t, progress.PhaseTask, e.Phase)
	assert.Equal(t, "COMPLETED", e.Text)
	assert.Equal(t, "COMPLETED", e.Signal)
}

func TestEvent_JSON(t *testing.T) {
	t.Run("output event serializes correctly", func(t *testing.T) {
		e := NewOutputEvent(progress.PhaseTask, "test output")

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
		e := NewSectionEvent(progress.PhaseReview, "Test Section")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "Test Section", decoded["section"])
	})

	t.Run("signal event includes signal field", func(t *testing.T) {
		e := NewSignalEvent(progress.PhaseTask, "DONE")

		data, err := e.JSON()
		require.NoError(t, err)

		var decoded map[string]any
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, "DONE", decoded["signal"])
	})

	t.Run("omits empty fields", func(t *testing.T) {
		e := NewOutputEvent(progress.PhaseTask, "simple output")

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
}
