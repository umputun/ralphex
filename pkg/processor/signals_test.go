package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsTerminalSignal(t *testing.T) {
	tests := []struct {
		signal string
		want   bool
	}{
		{SignalCompleted, true},
		{SignalFailed, true},
		{SignalReviewDone, false},
		{SignalCodexDone, false},
		{"", false},
		{"OTHER", false},
	}

	for _, tc := range tests {
		t.Run(tc.signal, func(t *testing.T) {
			assert.Equal(t, tc.want, IsTerminalSignal(tc.signal))
		})
	}
}

func TestIsReviewDone(t *testing.T) {
	tests := []struct {
		signal string
		want   bool
	}{
		{SignalReviewDone, true},
		{SignalCompleted, false},
		{SignalFailed, false},
		{SignalCodexDone, false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.signal, func(t *testing.T) {
			assert.Equal(t, tc.want, IsReviewDone(tc.signal))
		})
	}
}

func TestIsCodexDone(t *testing.T) {
	tests := []struct {
		signal string
		want   bool
	}{
		{SignalCodexDone, true},
		{SignalCompleted, false},
		{SignalFailed, false},
		{SignalReviewDone, false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.signal, func(t *testing.T) {
			assert.Equal(t, tc.want, IsCodexDone(tc.signal))
		})
	}
}
