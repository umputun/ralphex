package status

import "fmt"

// SectionType represents the semantic type of a section header.
// the web layer uses these types to emit appropriate boundary events:
//   - SectionTaskIteration: emits task_start/task_end events
//   - SectionClaudeReview, SectionCodexIteration: emits iteration_start events
//   - SectionGeneric, SectionClaudeEval: no boundary events, just section headers
//
// invariants:
//   - Iteration > 0 for SectionTaskIteration, SectionClaudeReview, SectionCodexIteration
//   - Iteration == 0 for SectionGeneric, SectionClaudeEval
//
// prefer using the constructor functions (NewTaskIterationSection, etc.) to ensure
// these invariants are maintained.
type SectionType int

const (
	// SectionGeneric is a static section header with no iteration.
	SectionGeneric SectionType = iota
	// SectionTaskIteration represents a task execution iteration.
	SectionTaskIteration
	// SectionClaudeReview represents a Claude review iteration.
	SectionClaudeReview
	// SectionCodexIteration represents a Codex review iteration.
	SectionCodexIteration
	// SectionClaudeEval represents Claude evaluating codex findings.
	SectionClaudeEval
	// SectionPlanIteration represents a plan creation iteration.
	SectionPlanIteration
	// SectionCustomIteration represents a custom review tool iteration.
	SectionCustomIteration
)

// Section carries structured information about a section header.
// instead of parsing section names with regex, consumers can access
// the Type and Iteration fields directly.
//
// use the provided constructors (NewTaskIterationSection, etc.) to create sections
// with proper Type/Iteration/Label consistency.
type Section struct {
	Type      SectionType
	Iteration int    // 0 for non-iterated sections
	Label     string // human-readable display text
}

// NewTaskIterationSection creates a section for task execution iteration.
func NewTaskIterationSection(iteration int) Section {
	return Section{
		Type:      SectionTaskIteration,
		Iteration: iteration,
		Label:     fmt.Sprintf("task iteration %d", iteration),
	}
}

// NewClaudeReviewSection creates a section for Claude review iteration.
// suffix is appended after the iteration number (e.g., ": critical/major").
func NewClaudeReviewSection(iteration int, suffix string) Section {
	return Section{
		Type:      SectionClaudeReview,
		Iteration: iteration,
		Label:     fmt.Sprintf("claude review %d%s", iteration, suffix),
	}
}

// NewCodexIterationSection creates a section for Codex review iteration.
func NewCodexIterationSection(iteration int) Section {
	return Section{
		Type:      SectionCodexIteration,
		Iteration: iteration,
		Label:     fmt.Sprintf("codex iteration %d", iteration),
	}
}

// NewClaudeEvalSection creates a section for Claude evaluating codex findings.
func NewClaudeEvalSection() Section {
	return Section{
		Type:  SectionClaudeEval,
		Label: "claude evaluating codex findings",
	}
}

// NewGenericSection creates a static section header with no iteration.
func NewGenericSection(label string) Section {
	return Section{
		Type:  SectionGeneric,
		Label: label,
	}
}

// NewPlanIterationSection creates a section for plan creation iteration.
func NewPlanIterationSection(iteration int) Section {
	return Section{
		Type:      SectionPlanIteration,
		Iteration: iteration,
		Label:     fmt.Sprintf("plan iteration %d", iteration),
	}
}

// NewCustomIterationSection creates a section for custom review tool iteration.
func NewCustomIterationSection(iteration int) Section {
	return Section{
		Type:      SectionCustomIteration,
		Iteration: iteration,
		Label:     fmt.Sprintf("custom review iteration %d", iteration),
	}
}
