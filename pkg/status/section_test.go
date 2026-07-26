package status

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReviewSections_TypeAndLabelContract(t *testing.T) {
	tests := []struct {
		name          string
		section       Section
		wantType      SectionType
		wantIteration int
		wantLabel     string
		noCodex       bool
	}{
		{
			name:          "claude first pass review",
			section:       NewClaudeReviewSection(0, ": all findings"),
			wantType:      SectionInternalReview,
			wantIteration: 0,
			wantLabel:     "claude review 0: all findings",
		},
		{
			name:          "internal codex review",
			section:       NewInternalReviewSection(3, ": critical/major"),
			wantType:      SectionInternalReview,
			wantIteration: 3,
			wantLabel:     "review 3: critical/major",
			noCodex:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantType, tc.section.Type)
			assert.Equal(t, tc.wantIteration, tc.section.Iteration)
			assert.Equal(t, tc.wantLabel, tc.section.Label)
			if tc.noCodex {
				assert.NotContains(t, tc.section.Label, "codex")
			}
		})
	}
}

func TestSections_TypeAndLabelContract(t *testing.T) {
	tests := []struct {
		name          string
		section       Section
		wantType      SectionType
		wantIteration int
		wantLabel     string
	}{
		{name: "task", section: NewTaskIterationSection(1), wantType: SectionTaskIteration, wantIteration: 1, wantLabel: "task iteration 1"},
		{name: "codex", section: NewCodexIterationSection(2), wantType: SectionCodexIteration, wantIteration: 2, wantLabel: "codex iteration 2"},
		{name: "plan", section: NewPlanIterationSection(4), wantType: SectionPlanIteration, wantIteration: 4, wantLabel: "plan iteration 4"},
		{name: "custom", section: NewCustomIterationSection(7), wantType: SectionCustomIteration, wantIteration: 7, wantLabel: "custom review iteration 7"},
		{name: "claude eval", section: NewClaudeEvalSection(), wantType: SectionClaudeEval, wantIteration: 0, wantLabel: "claude evaluating codex findings"},
		{name: "generic", section: NewGenericSection("finalize"), wantType: SectionGeneric, wantIteration: 0, wantLabel: "finalize"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantType, tc.section.Type)
			assert.Equal(t, tc.wantIteration, tc.section.Iteration)
			assert.Equal(t, tc.wantLabel, tc.section.Label)
		})
	}
}
