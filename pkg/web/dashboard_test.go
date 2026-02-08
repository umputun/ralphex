package web

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/progress"
	"github.com/umputun/ralphex/pkg/status"
)

func TestNewDashboard(t *testing.T) {
	colors := testColors()
	holder := &status.PhaseHolder{}
	cfg := DashboardConfig{
		Port:            8080,
		PlanFile:        "test.md",
		Branch:          "main",
		WatchDirs:       []string{"/tmp"},
		ConfigWatchDirs: []string{"/var"},
		Colors:          colors,
	}

	d := NewDashboard(cfg, holder)
	require.NotNil(t, d)
	assert.Equal(t, 8080, d.port)
	assert.Equal(t, "test.md", d.planFile)
	assert.Equal(t, "main", d.branch)
	assert.Equal(t, []string{"/tmp"}, d.watchDirs)
	assert.Equal(t, []string{"/var"}, d.configWatchDirs)
}

func TestDashboard_Start_SingleSession(t *testing.T) {
	tmpDir := t.TempDir()
	progressPath := filepath.Join(tmpDir, "progress.txt")

	// create mock base logger
	colors := testColors()
	holder := &status.PhaseHolder{}
	baseLog, err := progress.NewLogger(progress.Config{
		PlanFile: progressPath,
		Mode:     "test",
		Branch:   "main",
		NoColor:  true,
	}, colors, holder)
	require.NoError(t, err)
	defer baseLog.Close()

	cfg := DashboardConfig{
		BaseLog:         baseLog,
		Port:            0, // use random port
		PlanFile:        "test.md",
		Branch:          "main",
		WatchDirs:       nil, // single-session mode
		ConfigWatchDirs: nil,
		Colors:          colors,
	}

	d := NewDashboard(cfg, holder)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// start dashboard (should return broadcast logger)
	broadcastLog, err := d.Start(ctx)
	require.NoError(t, err)
	require.NotNil(t, broadcastLog)

	// verify it's a broadcast logger by checking it has the path from base logger
	assert.Equal(t, baseLog.Path(), broadcastLog.Path())
}

func TestDashboard_Start_MultiSession(t *testing.T) {
	tmpDir := t.TempDir()
	progressPath := filepath.Join(tmpDir, "progress.txt")

	// create mock base logger
	colors := testColors()
	holder := &status.PhaseHolder{}
	baseLog, err := progress.NewLogger(progress.Config{
		PlanFile: progressPath,
		Mode:     "test",
		Branch:   "main",
		NoColor:  true,
	}, colors, holder)
	require.NoError(t, err)
	defer baseLog.Close()

	cfg := DashboardConfig{
		BaseLog:         baseLog,
		Port:            0, // use random port
		PlanFile:        "test.md",
		Branch:          "main",
		WatchDirs:       []string{tmpDir}, // multi-session mode
		ConfigWatchDirs: nil,
		Colors:          colors,
	}

	d := NewDashboard(cfg, holder)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// start dashboard (should return broadcast logger)
	broadcastLog, err := d.Start(ctx)
	require.NoError(t, err)
	require.NotNil(t, broadcastLog)

	// verify it's a broadcast logger
	assert.Equal(t, baseLog.Path(), broadcastLog.Path())
}

func TestDashboard_RunWatchOnly_NoWatchDirs(t *testing.T) {
	colors := testColors()
	holder := &status.PhaseHolder{}
	cfg := DashboardConfig{
		Port:   8080,
		Colors: colors,
	}

	d := NewDashboard(cfg, holder)
	ctx := context.Background()

	err := d.RunWatchOnly(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no watch directories configured")
}

func TestDashboard_RunWatchOnly_Success(t *testing.T) {
	tmpDir := t.TempDir()

	colors := testColors()
	holder := &status.PhaseHolder{}
	cfg := DashboardConfig{
		Port:   0, // use random port
		Colors: colors,
	}

	d := NewDashboard(cfg, holder)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := d.RunWatchOnly(ctx, []string{tmpDir})
	// should return nil when context is canceled (normal shutdown)
	assert.NoError(t, err)
}

func TestSetupWatchMode(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	srvErrCh, watchErrCh, err := setupWatchMode(ctx, 0, []string{tmpDir})
	require.NoError(t, err)
	assert.NotNil(t, srvErrCh)
	assert.NotNil(t, watchErrCh)
}

func TestStartServerAsync_Success(t *testing.T) {
	tmpDir := t.TempDir()
	session := NewSession("test", filepath.Join(tmpDir, "progress.txt"))
	srv, err := NewServer(ServerConfig{
		Port:     0, // use random port
		PlanName: "test",
		Branch:   "main",
		PlanFile: "test.md",
	}, session)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh, err := startServerAsync(ctx, srv, 0)
	require.NoError(t, err)
	assert.NotNil(t, errCh)

	// give server time to start
	time.Sleep(200 * time.Millisecond)

	// verify server is running by checking if error channel is still open
	select {
	case err := <-errCh:
		// if channel is closed or has error, server didn't start properly
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}
	default:
		// channel still open, server is running
	}
}

func TestStartServerAsync_PortInUse(t *testing.T) {
	tmpDir := t.TempDir()

	session := NewSession("test", filepath.Join(tmpDir, "progress.txt"))
	srv, err := NewServer(ServerConfig{
		Port:     8999, // specific port
		PlanName: "test",
		Branch:   "main",
		PlanFile: "test.md",
	}, session)
	require.NoError(t, err)

	// start another server on same port
	srv2, err := NewServer(ServerConfig{
		Port:     8999,
		PlanName: "test2",
		Branch:   "main",
		PlanFile: "test2.md",
	}, session)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// first server should start
	errCh, err := startServerAsync(ctx, srv, 8999)
	require.NoError(t, err)
	defer func() { <-errCh }()

	// second server should fail
	_, err = startServerAsync(ctx, srv2, 8999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start")
}

func TestMonitorErrors_ContextCanceled(t *testing.T) {
	colors := testColors()
	ctx, cancel := context.WithCancel(context.Background())

	srvErrCh := make(chan error)
	watchErrCh := make(chan error)

	// cancel context immediately
	cancel()

	err := monitorErrors(ctx, srvErrCh, watchErrCh, colors)
	assert.NoError(t, err)
}

func TestMonitorErrors_BothChannelsClosed(t *testing.T) {
	colors := testColors()
	ctx := context.Background()

	srvErrCh := make(chan error)
	watchErrCh := make(chan error)

	// close both channels
	close(srvErrCh)
	close(watchErrCh)

	err := monitorErrors(ctx, srvErrCh, watchErrCh, colors)
	assert.NoError(t, err)
}

func TestMonitorErrors_ErrorInServer(t *testing.T) {
	colors := testColors()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	srvErrCh := make(chan error, 1)
	watchErrCh := make(chan error)

	// send error to server channel
	go func() {
		srvErrCh <- assert.AnError
		close(srvErrCh)
		close(watchErrCh)
	}()

	err := monitorErrors(ctx, srvErrCh, watchErrCh, colors)
	assert.NoError(t, err) // errors are logged, not returned
}

func TestPrintWatchInfo(t *testing.T) {
	colors := testColors()

	// just verify it doesn't panic
	printWatchInfo([]string{"/tmp", "/var"}, 8080, colors)
}
