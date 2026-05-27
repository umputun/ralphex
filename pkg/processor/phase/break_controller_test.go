package phase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBreakController_Drain(t *testing.T) {
	t.Run("drains pending value", func(t *testing.T) {
		breakCh := make(chan struct{}, 1)
		breakCh <- struct{}{}
		ctrl := NewBreakController(&Deps{BreakCh: breakCh})

		ctrl.drain()

		select {
		case <-breakCh:
			t.Fatal("channel should be empty after drain")
		default:
		}
	})

	t.Run("no-op on empty channel", func(t *testing.T) {
		breakCh := make(chan struct{}, 1)
		ctrl := NewBreakController(&Deps{BreakCh: breakCh})

		ctrl.drain()
	})

	t.Run("no-op on nil channel", func(t *testing.T) {
		ctrl := NewBreakController(&Deps{})

		ctrl.drain()
	})
}

func TestBreakController_Context(t *testing.T) {
	breakCh := make(chan struct{}, 1)
	ctrl := NewBreakController(&Deps{BreakCh: breakCh})
	ctx, cancel := ctrl.context(t.Context())
	defer cancel()

	breakCh <- struct{}{}
	<-ctx.Done()

	assert.True(t, ctrl.isBreak(ctx, context.Background()))
}

func TestBreakController_ContextWithNilChannel(t *testing.T) {
	parent := t.Context()
	ctrl := NewBreakController(&Deps{})
	ctx, cancel := ctrl.context(parent)
	defer cancel()

	assert.Same(t, parent, ctx)
	assert.False(t, ctrl.isBreak(ctx, parent))
}
