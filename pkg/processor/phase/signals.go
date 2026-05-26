package phase

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/umputun/ralphex/pkg/status"
)

// Signal aliases mirror pkg/status values used by phase prompt contracts and parser helpers.
const (
	SignalCompleted  = status.Completed
	SignalFailed     = status.Failed
	SignalReviewDone = status.ReviewDone
	SignalCodexDone  = status.CodexDone
	SignalQuestion   = status.Question
	SignalPlanReady  = status.PlanReady
	SignalPlanDraft  = status.PlanDraft
)

var questionSignalRe = regexp.MustCompile(`<<<RALPHEX:QUESTION>>>\s*([\s\S]*?)\s*<<<RALPHEX:END>>>`)

var planDraftSignalRe = regexp.MustCompile(`<<<RALPHEX:PLAN_DRAFT>>>\s*([\s\S]*?)\s*<<<RALPHEX:END>>>`)

// QuestionPayload represents a question signal from the plan creation phase.
type QuestionPayload struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Context  string   `json:"context,omitempty"`
}

// IsReviewDone reports whether signal marks internal review completion.
func IsReviewDone(signal string) bool {
	return signal == SignalReviewDone
}

// IsCodexDone reports whether signal marks external review completion.
func IsCodexDone(signal string) bool {
	return signal == SignalCodexDone
}

// IsPlanReady reports whether signal marks plan creation completion.
func IsPlanReady(signal string) bool {
	return signal == SignalPlanReady
}

// ErrNoQuestionSignal indicates no question signal was found in output.
var ErrNoQuestionSignal = errors.New("no question signal found")

// ErrNoPlanDraftSignal indicates no plan draft signal was found in output.
var ErrNoPlanDraftSignal = errors.New("no plan draft signal found")

// ParseQuestionPayload extracts a question payload from output containing a QUESTION signal.
func ParseQuestionPayload(output string) (*QuestionPayload, error) {
	if !strings.Contains(output, SignalQuestion) {
		return nil, ErrNoQuestionSignal
	}

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

	if payload.Question == "" {
		return nil, errors.New("malformed question signal: missing question field")
	}
	if len(payload.Options) == 0 {
		return nil, errors.New("malformed question signal: missing or empty options field")
	}

	return &payload, nil
}

// ParsePlanDraftPayload extracts plan content from output containing a PLAN_DRAFT signal.
func ParsePlanDraftPayload(output string) (string, error) {
	if !strings.Contains(output, SignalPlanDraft) {
		return "", ErrNoPlanDraftSignal
	}

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
