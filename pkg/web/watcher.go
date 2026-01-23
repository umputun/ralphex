package web

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors directories for progress file changes.
// it uses fsnotify for efficient file system event detection
// and notifies the SessionManager when new progress files appear.
type Watcher struct {
	dirs    []string
	sm      *SessionManager
	watcher *fsnotify.Watcher

	mu      sync.Mutex
	started bool
}

// NewWatcher creates a watcher for the specified directories.
// directories are watched recursively for progress-*.txt files.
func NewWatcher(dirs []string, sm *SessionManager) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}

	return &Watcher{
		dirs:    dirs,
		sm:      sm,
		watcher: w,
	}, nil
}

// Start begins watching directories for progress file changes.
// runs until the context is canceled.
// performs initial discovery before starting the watch loop.
func (w *Watcher) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return nil
	}
	w.started = true
	w.mu.Unlock()

	// add all directories to watcher (including subdirectories)
	for _, dir := range w.dirs {
		if err := w.addRecursive(dir); err != nil {
			return err
		}
	}

	// initial discovery (recursive to find existing progress files in subdirectories)
	for _, dir := range w.dirs {
		if _, err := w.sm.DiscoverRecursive(dir); err != nil {
			log.Printf("[WARN] initial discovery failed for %s: %v", dir, err)
		}
	}

	// start tailing for active sessions
	w.sm.StartTailingActive()

	// start periodic state refresh to detect completed sessions
	go w.refreshLoop(ctx)

	// run the watch loop
	return w.run(ctx)
}

// addRecursive adds a directory and all its subdirectories to the watcher.
func (w *Watcher) addRecursive(dir string) error {
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// skip directories that can't be accessed
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return err
		}

		if d.IsDir() {
			name := d.Name()
			// skip hidden directories
			if strings.HasPrefix(name, ".") && path != dir {
				return filepath.SkipDir
			}
			// skip directories that typically contain many subdirs and no progress files
			switch name {
			case "node_modules", "vendor", "__pycache__", "target", "build", "dist":
				return filepath.SkipDir
			}
			// best-effort: continue walking even if we can't watch a specific directory
			if err := w.watcher.Add(path); err != nil {
				log.Printf("[WARN] failed to watch directory %s: %v", path, err)
			}
		}
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walk directory %s: %w", dir, walkErr)
	}
	return nil
}

// run is the main watch loop processing fsnotify events.
func (w *Watcher) run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return w.Close()

		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}
			w.handleEvent(event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			// log error but continue watching
			log.Printf("[WARN] fsnotify error: %v", err)
		}
	}
}

// handleEvent processes a single fsnotify event.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// filter for progress-*.txt files only
	if !isProgressFile(event.Name) {
		w.handleNonProgressEvent(event)
		return
	}

	// handle create or write events
	if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
		w.handleProgressFileChange(event.Name)
	}

	// handle remove events
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		id := sessionIDFromPath(event.Name)
		w.sm.Remove(id)
	}
}

// handleNonProgressEvent handles events for non-progress files (e.g., new directories).
func (w *Watcher) handleNonProgressEvent(event fsnotify.Event) {
	if !event.Has(fsnotify.Create) {
		return
	}
	info, err := os.Stat(event.Name)
	if err != nil || !info.IsDir() {
		return
	}
	if err := w.addRecursive(event.Name); err != nil {
		log.Printf("[WARN] failed to watch new directory %s: %v", event.Name, err)
	}
}

// handleProgressFileChange handles create/write events for progress files.
func (w *Watcher) handleProgressFileChange(path string) {
	dir := filepath.Dir(path)
	ids, err := w.sm.Discover(dir)
	if err != nil {
		log.Printf("[WARN] discovery failed for %s: %v", dir, err)
		return
	}

	// start tailing for any newly active sessions
	for _, id := range ids {
		w.startTailingIfNeeded(id)
	}
}

// startTailingIfNeeded starts tailing for a session if it's active and not already tailing.
func (w *Watcher) startTailingIfNeeded(id string) {
	session := w.sm.Get(id)
	if session == nil {
		return
	}
	if session.GetState() != SessionStateActive || session.IsTailing() {
		return
	}
	if err := session.StartTailing(true); err != nil {
		log.Printf("[WARN] failed to start tailing for session %s: %v", id, err)
	}
}

// refreshLoop periodically checks for session state changes (active->completed).
// runs until context is canceled.
func (w *Watcher) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sm.RefreshStates()
		}
	}
}

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	if err := w.watcher.Close(); err != nil {
		return fmt.Errorf("close fsnotify watcher: %w", err)
	}
	return nil
}

// isProgressFile returns true if the path matches progress-*.txt pattern.
func isProgressFile(path string) bool {
	name := filepath.Base(path)
	return strings.HasPrefix(name, "progress-") && strings.HasSuffix(name, ".txt")
}

// ResolveWatchDirs determines the directories to watch based on precedence:
// CLI flags > config file > current directory (default).
// returns at least one directory (current directory if nothing else specified).
func ResolveWatchDirs(cliDirs, configDirs []string) []string {
	// CLI flags take highest precedence
	if len(cliDirs) > 0 {
		return normalizeDirs(cliDirs)
	}

	// config file is second
	if len(configDirs) > 0 {
		return normalizeDirs(configDirs)
	}

	// default to current directory
	cwd, err := os.Getwd()
	if err != nil {
		return []string{"."}
	}
	return []string{cwd}
}

// normalizeDirs converts relative paths to absolute and removes duplicates.
// logs warnings for invalid directories to help users debug configuration issues.
func normalizeDirs(dirs []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(dirs))

	for _, dir := range dirs {
		// convert to absolute path
		abs, err := filepath.Abs(dir)
		if err != nil {
			log.Printf("[WARN] failed to resolve path %q: %v", dir, err)
			abs = dir
		}

		// resolve symlinks for consistent deduplication (macOS has /var -> /private/var)
		if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
			abs = resolved
		}

		// skip duplicates
		if seen[abs] {
			continue
		}
		seen[abs] = true

		// verify directory exists
		info, err := os.Stat(abs)
		if err != nil {
			log.Printf("[WARN] watch directory %q does not exist: %v", abs, err)
			continue
		}
		if !info.IsDir() {
			log.Printf("[WARN] watch path %q is not a directory", abs)
			continue
		}
		result = append(result, abs)
	}

	// fallback to current directory if all specified dirs are invalid
	if len(result) == 0 {
		log.Printf("[WARN] all watch directories invalid, falling back to current directory")
		cwd, err := os.Getwd()
		if err != nil {
			return []string{"."}
		}
		return []string{cwd}
	}

	return result
}
