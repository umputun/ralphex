package phase

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGitStateSnapshot(t *testing.T) {
	git := NewGitState(&Deps{Git: &gitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "head", nil },
		DiffFingerprintFunc: func() (string, error) { return "diff", nil },
	}}, newMockLogger(""))

	snapshot := git.snapshot()

	assert.Equal(t, gitSnapshot{head: "head", diff: "diff"}, snapshot)
}

func TestGitStateErrorsReturnEmptySnapshot(t *testing.T) {
	log := newMockLogger("")
	git := NewGitState(&Deps{Git: &gitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "", errors.New("head failed") },
		DiffFingerprintFunc: func() (string, error) { return "", errors.New("diff failed") },
	}}, log)

	snapshot := git.snapshot()

	assert.Equal(t, gitSnapshot{}, snapshot)
	assertLogContains(t, log, "failed to get HEAD hash")
	assertLogContains(t, log, "failed to get diff fingerprint")
}

func TestStalemateStateUpdate(t *testing.T) {
	log := newMockLogger("")
	state := newStalemateState(Config{ReviewPatience: 2}, log)
	before := gitSnapshot{head: "head", diff: "diff"}
	after := gitSnapshot{head: "head", diff: "diff"}

	assert.False(t, state.Update(before, after))
	assert.True(t, state.Update(before, after))
	assertLogContains(t, log, "stalemate detected")
}

func TestStalemateStateUpdateResetsOnChange(t *testing.T) {
	state := newStalemateState(Config{ReviewPatience: 2}, newMockLogger(""))

	assert.False(t, state.Update(gitSnapshot{head: "a", diff: "a"}, gitSnapshot{head: "a", diff: "a"}))
	assert.False(t, state.Update(gitSnapshot{head: "a", diff: "a"}, gitSnapshot{head: "b", diff: "b"}))
	assert.False(t, state.Update(gitSnapshot{head: "b", diff: "b"}, gitSnapshot{head: "b", diff: "b"}))
}

func TestStalemateStateUpdateSkipsMissingAfterDiff(t *testing.T) {
	state := newStalemateState(Config{ReviewPatience: 1}, newMockLogger(""))

	stale := state.Update(gitSnapshot{head: "head", diff: "diff"}, gitSnapshot{head: "head"})

	assert.False(t, stale)
}
