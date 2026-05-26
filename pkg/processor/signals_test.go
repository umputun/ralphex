package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/ralphex/pkg/status"
)

func TestSignalAliasesMirrorStatus(t *testing.T) {
	assert.Equal(t, status.Completed, SignalCompleted)
	assert.Equal(t, status.Failed, SignalFailed)
	assert.Equal(t, status.ReviewDone, SignalReviewDone)
	assert.Equal(t, status.CodexDone, SignalCodexDone)
	assert.Equal(t, status.Question, SignalQuestion)
	assert.Equal(t, status.PlanReady, SignalPlanReady)
	assert.Equal(t, status.PlanDraft, SignalPlanDraft)
}
