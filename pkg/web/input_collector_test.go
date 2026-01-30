package web

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/input"
)

func TestNewWebInputCollector(t *testing.T) {
	session := NewSession("test-session", "/tmp/progress.txt")
	defer session.Close()

	collector := NewWebInputCollector(session)

	assert.NotNil(t, collector)
	assert.Equal(t, session, collector.session)
	assert.Nil(t, collector.pending)
}

func TestWebInputCollector_AskQuestion(t *testing.T) {
	t.Run("blocks until answer is submitted", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		question := "Which option?"
		options := []string{"Option A", "Option B", "Option C"}

		// start AskQuestion in a goroutine
		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)
		go func() {
			answer, err := collector.AskQuestion(context.Background(), question, options)
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- answer
		}()

		// give time for the question to be registered
		time.Sleep(50 * time.Millisecond)

		// verify question is pending
		pending := collector.GetPendingQuestion()
		require.NotNil(t, pending)
		assert.Equal(t, question, pending.Question)
		assert.Equal(t, options, pending.Options)

		// submit answer
		err := collector.SubmitAnswer(pending.ID, "Option B")
		require.NoError(t, err)

		// wait for result
		select {
		case answer := <-resultCh:
			assert.Equal(t, "Option B", answer)
		case err := <-errCh:
			t.Fatalf("unexpected error: %v", err)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for answer")
		}

		// verify pending is cleared
		assert.Nil(t, collector.GetPendingQuestion())
	})

	t.Run("returns error when context is canceled", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		ctx, cancel := context.WithCancel(context.Background())

		errCh := make(chan error, 1)
		go func() {
			_, err := collector.AskQuestion(ctx, "Question?", []string{"A", "B"})
			errCh <- err
		}()

		// give time for the question to be registered
		time.Sleep(50 * time.Millisecond)

		// cancel context
		cancel()

		// wait for error
		select {
		case err := <-errCh:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for cancellation error")
		}

		// verify pending is cleared
		assert.Nil(t, collector.GetPendingQuestion())
	})

	t.Run("returns error for empty options", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		_, err := collector.AskQuestion(context.Background(), "Question?", []string{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no options provided")
	})
}

func TestWebInputCollector_SubmitAnswer(t *testing.T) {
	t.Run("validates question ID", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		// no pending question
		err := collector.SubmitAnswer("wrong-id", "Answer")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no pending question")
	})

	t.Run("validates answer is in options list", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		// start a question
		go func() {
			_, _ = collector.AskQuestion(context.Background(), "Pick one", []string{"A", "B"})
		}()

		// wait for question to be pending
		time.Sleep(50 * time.Millisecond)
		pending := collector.GetPendingQuestion()
		require.NotNil(t, pending)

		// try invalid answer
		err := collector.SubmitAnswer(pending.ID, "C")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid answer")
	})

	t.Run("validates mismatched question ID", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		// start a question
		go func() {
			_, _ = collector.AskQuestion(context.Background(), "Pick one", []string{"A", "B"})
		}()

		// wait for question to be pending
		time.Sleep(50 * time.Millisecond)

		// try with wrong question ID
		err := collector.SubmitAnswer("wrong-id", "A")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "question ID mismatch")
	})
}

func TestWebInputCollector_GetPendingQuestion(t *testing.T) {
	t.Run("returns nil when no question pending", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		assert.Nil(t, collector.GetPendingQuestion())
	})

	t.Run("returns copy of pending question", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		go func() {
			_, _ = collector.AskQuestion(context.Background(), "Question?", []string{"X", "Y"})
		}()

		time.Sleep(50 * time.Millisecond)

		pending := collector.GetPendingQuestion()
		require.NotNil(t, pending)
		assert.Equal(t, "Question?", pending.Question)
		assert.Equal(t, []string{"X", "Y"}, pending.Options)
		assert.NotEmpty(t, pending.ID)
	})
}

func TestWebInputCollector_AskDraftReview(t *testing.T) {
	t.Run("blocks until review is submitted", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		planContent := "# My Plan\n\n## Tasks\n- Task 1\n- Task 2"

		// start AskDraftReview in a goroutine
		type result struct {
			action, feedback string
			err              error
		}
		resultCh := make(chan result, 1)
		go func() {
			action, feedback, err := collector.AskDraftReview(context.Background(), "Review the plan", planContent)
			resultCh <- result{action, feedback, err}
		}()

		// give time for the review to be registered
		time.Sleep(50 * time.Millisecond)

		// verify draft review is pending
		pending := collector.GetPendingDraftReview()
		require.NotNil(t, pending)
		assert.Equal(t, "Review the plan", pending.Question)
		assert.Equal(t, planContent, pending.PlanContent)

		// submit accept action
		err := collector.SubmitDraftReview(pending.ID, input.ActionAccept, "")
		require.NoError(t, err)

		// wait for result
		select {
		case r := <-resultCh:
			require.NoError(t, r.err)
			assert.Equal(t, input.ActionAccept, r.action)
			assert.Empty(t, r.feedback)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for draft review result")
		}

		// verify pending is cleared
		assert.Nil(t, collector.GetPendingDraftReview())
	})

	t.Run("returns revise action with feedback", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		type result struct {
			action, feedback string
			err              error
		}
		resultCh := make(chan result, 1)
		go func() {
			action, feedback, err := collector.AskDraftReview(context.Background(), "Review", "# Plan")
			resultCh <- result{action, feedback, err}
		}()

		time.Sleep(50 * time.Millisecond)

		pending := collector.GetPendingDraftReview()
		require.NotNil(t, pending)

		err := collector.SubmitDraftReview(pending.ID, input.ActionRevise, "add more tests")
		require.NoError(t, err)

		select {
		case r := <-resultCh:
			require.NoError(t, r.err)
			assert.Equal(t, input.ActionRevise, r.action)
			assert.Equal(t, "add more tests", r.feedback)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for draft review result")
		}
	})

	t.Run("returns reject action", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		type result struct {
			action, feedback string
			err              error
		}
		resultCh := make(chan result, 1)
		go func() {
			action, feedback, err := collector.AskDraftReview(context.Background(), "Review", "# Plan")
			resultCh <- result{action, feedback, err}
		}()

		time.Sleep(50 * time.Millisecond)

		pending := collector.GetPendingDraftReview()
		require.NotNil(t, pending)

		err := collector.SubmitDraftReview(pending.ID, input.ActionReject, "")
		require.NoError(t, err)

		select {
		case r := <-resultCh:
			require.NoError(t, r.err)
			assert.Equal(t, input.ActionReject, r.action)
			assert.Empty(t, r.feedback)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for draft review result")
		}
	})

	t.Run("returns error when context is canceled", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		ctx, cancel := context.WithCancel(context.Background())

		errCh := make(chan error, 1)
		go func() {
			_, _, err := collector.AskDraftReview(ctx, "Review", "# Plan")
			errCh <- err
		}()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case err := <-errCh:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for cancellation error")
		}

		assert.Nil(t, collector.GetPendingDraftReview())
	})
}

func TestWebInputCollector_SubmitDraftReview(t *testing.T) {
	t.Run("rejects no pending review", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		err := collector.SubmitDraftReview("some-id", input.ActionAccept, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no pending draft review")
	})

	t.Run("rejects mismatched review ID", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		go func() {
			_, _, _ = collector.AskDraftReview(t.Context(), "Review", "# Plan")
		}()

		time.Sleep(50 * time.Millisecond)

		err := collector.SubmitDraftReview("wrong-id", input.ActionAccept, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "review ID mismatch")
	})

	t.Run("rejects invalid action", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		go func() {
			_, _, _ = collector.AskDraftReview(t.Context(), "Review", "# Plan")
		}()

		time.Sleep(50 * time.Millisecond)

		pending := collector.GetPendingDraftReview()
		require.NotNil(t, pending)

		err := collector.SubmitDraftReview(pending.ID, "invalid", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid action")
	})

	t.Run("rejects revise without feedback", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		go func() {
			_, _, _ = collector.AskDraftReview(t.Context(), "Review", "# Plan")
		}()

		time.Sleep(50 * time.Millisecond)

		pending := collector.GetPendingDraftReview()
		require.NotNil(t, pending)

		err := collector.SubmitDraftReview(pending.ID, input.ActionRevise, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "revision feedback cannot be empty")
	})

	t.Run("rejects submission after review completed", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		go func() {
			_, _, _ = collector.AskDraftReview(context.Background(), "Review", "# Plan")
		}()

		time.Sleep(50 * time.Millisecond)

		pending := collector.GetPendingDraftReview()
		require.NotNil(t, pending)

		// first submission succeeds
		err := collector.SubmitDraftReview(pending.ID, input.ActionAccept, "")
		require.NoError(t, err)

		// wait for AskDraftReview to complete and clear pending
		time.Sleep(50 * time.Millisecond)

		// second submission fails because pending is cleared
		err = collector.SubmitDraftReview(pending.ID, input.ActionAccept, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no pending draft review")
	})
}

func TestWebInputCollector_GetPendingDraftReview(t *testing.T) {
	t.Run("returns nil when no review pending", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		assert.Nil(t, collector.GetPendingDraftReview())
	})

	t.Run("returns copy of pending review", func(t *testing.T) {
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		collector := NewWebInputCollector(session)

		go func() {
			_, _, _ = collector.AskDraftReview(t.Context(), "Review?", "# Plan Content")
		}()

		time.Sleep(50 * time.Millisecond)

		pending := collector.GetPendingDraftReview()
		require.NotNil(t, pending)
		assert.Equal(t, "Review?", pending.Question)
		assert.Equal(t, "# Plan Content", pending.PlanContent)
		assert.NotEmpty(t, pending.ID)
	})
}

func TestGenerateQuestionID(t *testing.T) {
	t.Run("generates unique IDs", func(t *testing.T) {
		ids := make(map[string]bool)
		for range 100 {
			id := generateQuestionID()
			assert.False(t, ids[id], "duplicate ID generated: %s", id)
			ids[id] = true
		}
	})

	t.Run("generates 16-character hex strings", func(t *testing.T) {
		for range 10 {
			id := generateQuestionID()
			assert.Len(t, id, 16)
			// verify it's valid hex
			for _, c := range id {
				assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'), "invalid hex char: %c", c)
			}
		}
	})
}
