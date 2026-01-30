package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"

	"github.com/umputun/ralphex/pkg/input"
)

// PendingQuestion represents a question waiting for an answer.
type PendingQuestion struct {
	ID       string   // unique question identifier
	Question string   // the question text
	Options  []string // available answer options
	answerCh chan string
}

// draftReviewResponse holds the action and feedback from a draft review submission.
type draftReviewResponse struct {
	Action   string
	Feedback string
}

// PendingDraftReview represents a draft review waiting for user action.
type PendingDraftReview struct {
	ID          string // unique review identifier
	Question    string // the review prompt
	PlanContent string // the plan markdown content
	responseCh  chan draftReviewResponse
}

// WebInputCollector implements input.Collector for web-based input collection.
// It uses channel-based coordination where AskQuestion blocks until SubmitAnswer is called.
type WebInputCollector struct {
	mu           sync.Mutex
	session      *Session
	pending      *PendingQuestion
	pendingDraft *PendingDraftReview
}

// NewWebInputCollector creates a new WebInputCollector for the given session.
func NewWebInputCollector(session *Session) *WebInputCollector {
	return &WebInputCollector{
		session: session,
	}
}

// AskQuestion presents a question with options and blocks until an answer is submitted.
// Implements input.Collector interface.
func (w *WebInputCollector) AskQuestion(ctx context.Context, question string, options []string) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no options provided")
	}

	questionID := generateQuestionID()
	answerCh := make(chan string, 1)

	// set pending question
	w.mu.Lock()
	w.pending = &PendingQuestion{
		ID:       questionID,
		Question: question,
		Options:  options,
		answerCh: answerCh,
	}
	w.mu.Unlock()

	// publish question event to SSE clients
	event := NewQuestionEvent(questionID, question, options, "")
	if err := w.session.Publish(event); err != nil {
		log.Printf("[ERROR] failed to publish question event: %v", err)
	} else {
		log.Printf("[INFO] published question event: id=%s, question=%s", questionID, question)
	}

	// wait for answer or context cancellation
	var answer string
	var err error

	select {
	case answer = <-answerCh:
		// answer received
	case <-ctx.Done():
		err = ctx.Err()
	}

	// clear pending question
	w.mu.Lock()
	w.pending = nil
	w.mu.Unlock()

	if err != nil {
		return "", fmt.Errorf("question canceled: %w", err)
	}
	return answer, nil
}

// SubmitAnswer submits an answer to the pending question.
func (w *WebInputCollector) SubmitAnswer(questionID, answer string) error {
	w.mu.Lock()

	if w.pending == nil {
		w.mu.Unlock()
		return errors.New("no pending question")
	}

	if w.pending.ID != questionID {
		w.mu.Unlock()
		return errors.New("question ID mismatch")
	}

	// validate answer is in options
	if !slices.Contains(w.pending.Options, answer) {
		w.mu.Unlock()
		return errors.New("invalid answer: not in options list")
	}

	// send answer (non-blocking since channel is buffered)
	select {
	case w.pending.answerCh <- answer:
	default:
		// channel already has a value (shouldn't happen with proper usage)
	}

	w.mu.Unlock()

	// broadcast answer so other clients can mark it as resolved
	if err := w.session.Publish(NewQuestionAnsweredEvent(questionID, answer)); err != nil {
		log.Printf("[WARN] failed to publish answer event: %v", err)
	}

	return nil
}

// GetPendingQuestion returns the current pending question, or nil if none.
// Safe for concurrent access.
func (w *WebInputCollector) GetPendingQuestion() *PendingQuestion {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.pending == nil {
		return nil
	}

	// return a copy without the answer channel (internal detail)
	return &PendingQuestion{
		ID:       w.pending.ID,
		Question: w.pending.Question,
		Options:  append([]string{}, w.pending.Options...), // defensive copy
	}
}

// AskDraftReview presents a plan draft for review and blocks until the user submits an action.
// Returns action ("accept", "revise", "reject") and feedback (non-empty only for "revise").
// Implements input.Collector interface.
func (w *WebInputCollector) AskDraftReview(ctx context.Context, question, planContent string) (string, string, error) {
	reviewID := generateQuestionID()
	responseCh := make(chan draftReviewResponse, 1)

	// set pending draft review
	w.mu.Lock()
	w.pendingDraft = &PendingDraftReview{
		ID:          reviewID,
		Question:    question,
		PlanContent: planContent,
		responseCh:  responseCh,
	}
	w.mu.Unlock()

	// publish draft review event to SSE clients
	event := NewDraftReviewEvent(reviewID, question, planContent)
	if err := w.session.Publish(event); err != nil {
		log.Printf("[ERROR] failed to publish draft review event: %v", err)
	} else {
		log.Printf("[INFO] published draft review event: id=%s", reviewID)
	}

	// wait for response or context cancellation
	var resp draftReviewResponse
	var err error

	select {
	case resp = <-responseCh:
		// response received
	case <-ctx.Done():
		err = ctx.Err()
	}

	// clear pending draft review
	w.mu.Lock()
	w.pendingDraft = nil
	w.mu.Unlock()

	if err != nil {
		return "", "", fmt.Errorf("draft review canceled: %w", err)
	}
	return resp.Action, resp.Feedback, nil
}

// SubmitDraftReview submits a draft review action (accept/revise/reject) with optional feedback.
func (w *WebInputCollector) SubmitDraftReview(reviewID, action, feedback string) error {
	// validate action
	switch action {
	case input.ActionAccept, input.ActionRevise, input.ActionReject:
		// valid
	default:
		return fmt.Errorf("invalid action: %s (must be accept, revise, or reject)", action)
	}

	if action == input.ActionRevise && feedback == "" {
		return errors.New("revision feedback cannot be empty")
	}

	w.mu.Lock()

	if w.pendingDraft == nil {
		w.mu.Unlock()
		return errors.New("no pending draft review")
	}

	if w.pendingDraft.ID != reviewID {
		w.mu.Unlock()
		return errors.New("review ID mismatch")
	}

	// send response (non-blocking since channel is buffered)
	select {
	case w.pendingDraft.responseCh <- draftReviewResponse{Action: action, Feedback: feedback}:
	default:
		w.mu.Unlock()
		return errors.New("draft review already submitted")
	}

	w.mu.Unlock()

	// broadcast response so other clients can see the result
	if err := w.session.Publish(NewDraftReviewSubmittedEvent(reviewID, action, feedback)); err != nil {
		log.Printf("[WARN] failed to publish draft review response event: %v", err)
	}

	return nil
}

// GetPendingDraftReview returns the current pending draft review, or nil if none.
// Safe for concurrent access.
func (w *WebInputCollector) GetPendingDraftReview() *PendingDraftReview {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.pendingDraft == nil {
		return nil
	}

	// return a copy without the response channel (internal detail)
	return &PendingDraftReview{
		ID:          w.pendingDraft.ID,
		Question:    w.pendingDraft.Question,
		PlanContent: w.pendingDraft.PlanContent,
	}
}

// generateQuestionID creates a random 16-character hex string for question IDs.
func generateQuestionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
