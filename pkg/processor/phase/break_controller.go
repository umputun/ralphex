package phase

import "context"

// BreakController translates break-channel events into cancellable phase contexts.
type BreakController struct {
	deps *Deps
}

// NewBreakController creates a break controller backed by shared dependencies.
func NewBreakController(deps *Deps) *BreakController {
	return &BreakController{deps: deps}
}

func (b *BreakController) context(parent context.Context) (context.Context, context.CancelFunc) {
	ch := b.breakCh()
	if ch == nil {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (b *BreakController) isBreak(loopCtx, parentCtx context.Context) bool {
	return loopCtx.Err() != nil && parentCtx.Err() == nil
}

func (b *BreakController) drain() {
	ch := b.breakCh()
	if ch == nil {
		return
	}
	select {
	case <-ch:
	default:
	}
}

func (b *BreakController) breakCh() <-chan struct{} {
	if b == nil || b.deps == nil {
		return nil
	}
	return b.deps.BreakCh
}
