//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartBreakSignal_RepeatedSends(t *testing.T) {
	signal.Reset(syscall.SIGQUIT)
	ch := startBreakSignal()
	require.NotNil(t, ch)

	// send first SIGQUIT and consume from channel
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGQUIT))
	select {
	case <-ch:
		// ok, first value received
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first break signal")
	}

	// send second SIGQUIT — verifies channel is not closed and can receive again
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGQUIT))
	select {
	case <-ch:
		// ok, second value received
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second break signal")
	}
}

func TestStartBreakSignal_BufferedDropsWhenFull(t *testing.T) {
	signal.Reset(syscall.SIGQUIT)
	ch := startBreakSignal()
	require.NotNil(t, ch)

	// send SIGQUIT twice without consuming — second should be dropped (buffer size 1)
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGQUIT))
	time.Sleep(100 * time.Millisecond) // let goroutine process
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGQUIT))
	time.Sleep(100 * time.Millisecond)

	// consume the one buffered value
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for buffered signal")
	}

	// channel should be empty now — no second value
	select {
	case <-ch:
		t.Fatal("unexpected second value in channel — buffer should be 1")
	case <-time.After(200 * time.Millisecond):
		// ok, channel is empty as expected
	}
}

func TestStartBreakSignal_ReturnsBidirectionalChannel(t *testing.T) {
	signal.Reset(syscall.SIGQUIT)
	ch := startBreakSignal()
	require.NotNil(t, ch)

	// verify we can send directly on the channel (bidirectional type)
	select {
	case ch <- struct{}{}:
		// ok, direct send works
	default:
		t.Fatal("should be able to send on bidirectional channel")
	}

	// consume
	select {
	case <-ch:
	default:
		t.Fatal("should have a value after direct send")
	}

	assert.Equal(t, 1, cap(ch), "channel buffer size should be 1")
}
