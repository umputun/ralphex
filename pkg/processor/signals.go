package processor

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/umputun/ralphex/pkg/status"
)

// signal constants are aliases to the shared status package for convenience within processor.
// all signal values are defined in pkg/status to avoid circular dependencies.
const (
	SignalCompleted  = status.Completed
	SignalFailed     = status.Failed
	SignalReviewDone = status.ReviewDone
	SignalCodexDone  = status.CodexDone
	SignalQuestion   = status.Question
	SignalPlanReady  = status.PlanReady
	SignalPlanDraft  = status.PlanDraft
)

// questionSignalRe matches the QUESTION signal block with JSON payload
var questionSignalRe = regexp.MustCompile(`<<<RALPHEX:QUESTION>>>\s*([\s\S]*?)\s*<<<RALPHEX:END>>>`)

// planDraftSignalRe matches the PLAN_DRAFT signal block with plan content
var planDraftSignalRe = regexp.MustCompile(`<<<RALPHEX:PLAN_DRAFT>>>\s*([\s\S]*?)\s*<<<RALPHEX:END>>>`)

// QuestionPayload represents a question signal from Claude during plan creation
type QuestionPayload struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Context  string   `json:"context,omitempty"`
}

// IsReviewDone returns true if signal indicates review phase is complete.
func IsReviewDone(signal string) bool {
	return signal == SignalReviewDone
}

// IsCodexDone returns true if signal indicates codex phase is complete.
func IsCodexDone(signal string) bool {
	return signal == SignalCodexDone
}

// IsPlanReady returns true if signal indicates plan creation is complete.
func IsPlanReady(signal string) bool {
	return signal == SignalPlanReady
}

// ErrNoQuestionSignal indicates no question signal was found in output
var ErrNoQuestionSignal = errors.New("no question signal found")

// ErrNoPlanDraftSignal indicates no plan draft signal was found in output
var ErrNoPlanDraftSignal = errors.New("no plan draft signal found")

// ParseQuestionPayload extracts a QuestionPayload from output containing QUESTION signal.
// returns ErrNoQuestionSignal if no question signal is found.
// returns other error if signal is found but JSON is malformed.
func ParseQuestionPayload(output string) (*QuestionPayload, error) {
	// check if output contains the question signal at all
	if !strings.Contains(output, SignalQuestion) {
		return nil, ErrNoQuestionSignal
	}

	// extract the JSON payload between QUESTION and END markers
	matches := questionSignalRe.FindStringSubmatch(output)
	if len(matches) < 2 {
		return nil, errors.New("malformed question signal: missing END marker or empty payload")
	}

	jsonStr := strings.TrimSpace(matches[1])
	if jsonStr == "" {
		return nil, errors.New("malformed question signal: empty JSON payload")
	}

	var payload QuestionPayload
	if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
		return nil, fmt.Errorf("malformed question signal: invalid JSON: %w", err)
	}

	// validate required fields
	if payload.Question == "" {
		return nil, errors.New("malformed question signal: missing question field")
	}
	if len(payload.Options) == 0 {
		return nil, errors.New("malformed question signal: missing or empty options field")
	}

	return &payload, nil
}

// ParsePlanDraftPayload extracts plan content from output containing PLAN_DRAFT signal.
// returns ErrNoPlanDraftSignal if no plan draft signal is found.
// returns other error if signal is found but content is malformed.
func ParsePlanDraftPayload(output string) (string, error) {
	// check if output contains the plan draft signal at all
	if !strings.Contains(output, SignalPlanDraft) {
		return "", ErrNoPlanDraftSignal
	}

	// extract the content between PLAN_DRAFT and END markers
	matches := planDraftSignalRe.FindStringSubmatch(output)
	if len(matches) < 2 {
		return "", errors.New("malformed plan draft signal: missing END marker or empty content")
	}

	content := strings.TrimSpace(matches[1])
	if content == "" {
		return "", errors.New("malformed plan draft signal: empty plan content")
	}

	return content, nil
}
