package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/umputun/ralphex/pkg/progress"
	"github.com/umputun/ralphex/pkg/status"
)

// serverStartupTimeout is the time to wait for server startup before assuming success.
const serverStartupTimeout = 100 * time.Millisecond

// DashboardConfig holds configuration for dashboard initialization.
type DashboardConfig struct {
	BaseLog         Logger           // base progress logger
	Port            int              // web server port
	PlanFile        string           // path to plan file (empty for watch-only mode)
	Branch          string           // current git branch
	WatchDirs       []string         // CLI watch directories
	ConfigWatchDirs []string         // config file watch directories
	Colors          *progress.Colors // colors for output
}

// Dashboard manages web server and file watching for progress monitoring.
type Dashboard struct {
	port            int
	planFile        string
	branch          string
	baseLog         Logger
	watchDirs       []string
	configWatchDirs []string
	colors          *progress.Colors
	holder          *status.PhaseHolder
}

// NewDashboard creates a new dashboard with the given configuration.
func NewDashboard(cfg DashboardConfig, holder *status.PhaseHolder) *Dashboard {
	return &Dashboard{
		port:            cfg.Port,
		planFile:        cfg.PlanFile,
		branch:          cfg.Branch,
		baseLog:         cfg.BaseLog,
		watchDirs:       cfg.WatchDirs,
		configWatchDirs: cfg.ConfigWatchDirs,
		colors:          cfg.Colors,
		holder:          holder,
	}
}

// Start creates the web server and broadcast logger, starting the server in background.
// returns the broadcast logger to use for execution, or error if server fails to start.
// when watchDirs is non-empty, creates multi-session mode with file watching.
func (d *Dashboard) Start(ctx context.Context) (*BroadcastLogger, error) {
	// create session for SSE streaming (handles both live streaming and history replay)
	session := NewSession("main", d.baseLog.Path())
	broadcastLog := NewBroadcastLogger(d.baseLog, session, d.holder)

	// extract plan name for display
	planName := "(no plan)"
	if d.planFile != "" {
		planName = filepath.Base(d.planFile)
	}

	cfg := ServerConfig{
		Port:     d.port,
		PlanName: planName,
		Branch:   d.branch,
		PlanFile: d.planFile,
	}

	// determine if we should use multi-session mode
	// multi-session mode is enabled when watch dirs are provided via CLI or config
	useMultiSession := len(d.watchDirs) > 0 || len(d.configWatchDirs) > 0

	var srv *Server
	var watcher *Watcher

	if useMultiSession {
		// multi-session mode: use SessionManager and Watcher
		sm := NewSessionManager()

		// register the live execution session so dashboard uses it instead of creating a duplicate
		// this ensures live events from BroadcastLogger go to the same session the dashboard displays
		sm.Register(session)

		// resolve watch directories (CLI > config > cwd)
		dirs := ResolveWatchDirs(d.watchDirs, d.configWatchDirs)

		var err error
		watcher, err = NewWatcher(dirs, sm)
		if err != nil {
			return nil, fmt.Errorf("create watcher: %w", err)
		}

		srv, err = NewServerWithSessions(cfg, sm)
		if err != nil {
			return nil, fmt.Errorf("create web server: %w", err)
		}
	} else {
		// single-session mode: direct session for current execution
		var err error
		srv, err = NewServer(cfg, session)
		if err != nil {
			return nil, fmt.Errorf("create web server: %w", err)
		}
	}

	// start server with startup check
	srvErrCh, err := startServerAsync(ctx, srv, d.port)
	if err != nil {
		return nil, err
	}

	// start watcher in background if multi-session mode
	if watcher != nil {
		go func() {
			if watchErr := watcher.Start(ctx); watchErr != nil {
				// log error but don't fail - server can still work
				fmt.Fprintf(os.Stderr, "warning: watcher error: %v\n", watchErr)
			}
		}()
	}

	// monitor for late server errors in background
	// these are logged but don't fail the main execution since the dashboard is supplementary
	go func() {
		if srvErr := <-srvErrCh; srvErr != nil {
			fmt.Fprintf(os.Stderr, "warning: web server error during execution: %v\n", srvErr)
		}
	}()

	d.colors.Info().Printf("web dashboard: http://localhost:%d\n", d.port)
	return broadcastLog, nil
}

// RunWatchOnly runs the web dashboard in watch-only mode without plan execution.
// monitors directories for progress files and serves the multi-session dashboard.
func (d *Dashboard) RunWatchOnly(ctx context.Context, dirs []string) error {
	// fail fast if no watch directories configured
	if len(dirs) == 0 {
		return errors.New("no watch directories configured")
	}

	// setup server and watcher
	srvErrCh, watchErrCh, err := setupWatchMode(ctx, d.port, dirs)
	if err != nil {
		return err
	}

	// print startup info
	printWatchInfo(dirs, d.port, d.colors)

	// monitor for errors until shutdown
	return monitorErrors(ctx, srvErrCh, watchErrCh, d.colors)
}

// setupWatchMode creates and starts the web server and file watcher for watch-only mode.
// returns error channels for monitoring both components.
func setupWatchMode(ctx context.Context, port int, dirs []string) (chan error, chan error, error) {
	sm := NewSessionManager()
	watcher, err := NewWatcher(dirs, sm)
	if err != nil {
		return nil, nil, fmt.Errorf("create watcher: %w", err)
	}

	serverCfg := ServerConfig{
		Port:     port,
		PlanName: "(watch mode)",
		Branch:   "",
		PlanFile: "",
	}

	srv, err := NewServerWithSessions(serverCfg, sm)
	if err != nil {
		return nil, nil, fmt.Errorf("create web server: %w", err)
	}

	// start server with startup check
	srvErrCh, err := startServerAsync(ctx, srv, port)
	if err != nil {
		return nil, nil, err
	}

	// start watcher in background
	watchErrCh := make(chan error, 1)
	go func() {
		if watchErr := watcher.Start(ctx); watchErr != nil {
			watchErrCh <- watchErr
		}
		close(watchErrCh)
	}()

	return srvErrCh, watchErrCh, nil
}

// startServerAsync starts a web server in the background and waits briefly for startup errors.
// returns the error channel for monitoring late errors, or an error if startup fails.
func startServerAsync(ctx context.Context, srv *Server, port int) (chan error, error) {
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(ctx); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	// wait briefly for startup errors
	select {
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("web server failed to start on port %d: %w", port, err)
		}
	case <-time.After(serverStartupTimeout):
		// server started successfully
	}

	return errCh, nil
}

// monitorErrors monitors server and watcher error channels until shutdown.
func monitorErrors(ctx context.Context, srvErrCh, watchErrCh chan error, colors *progress.Colors) error {
	for {
		// exit when both channels are nil (closed and handled)
		if srvErrCh == nil && watchErrCh == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case srvErr, ok := <-srvErrCh:
			if !ok {
				srvErrCh = nil
				continue
			}
			if srvErr != nil && ctx.Err() == nil {
				colors.Error().Printf("web server error: %v\n", srvErr)
			}
		case watchErr, ok := <-watchErrCh:
			if !ok {
				watchErrCh = nil
				continue
			}
			if watchErr != nil && ctx.Err() == nil {
				colors.Error().Printf("file watcher error: %v\n", watchErr)
			}
		}
	}
}

// printWatchInfo prints startup information for watch-only mode.
func printWatchInfo(dirs []string, port int, colors *progress.Colors) {
	colors.Info().Printf("watch-only mode: monitoring %d directories\n", len(dirs))
	for _, dir := range dirs {
		colors.Info().Printf("  %s\n", dir)
	}
	colors.Info().Printf("web dashboard: http://localhost:%d\n", port)
	colors.Info().Printf("press Ctrl+C to exit\n")
}
