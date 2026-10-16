package vfs

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// EventType defines the type of filesystem change.
type EventType int

const (
	EventCreate EventType = iota
	EventModify
	EventDelete
	EventRename
)

func (e EventType) String() string {
	switch e {
	case EventCreate:
		return "CREATE"
	case EventModify:
		return "MODIFY"
	case EventDelete:
		return "DELETE"
	case EventRename:
		return "RENAME"
	default:
		return "UNKNOWN"
	}
}

// FileEvent represents a debounced filesystem change.
type FileEvent struct {
	WorkspaceID string
	Path        string // Workspace-relative path (e.g., /atlas/intro.md)
	Type        EventType
	Timestamp   time.Time
}

// EventHandler is called when a debounced file event occurs.
type EventHandler func(event FileEvent)

// Watcher monitors a workspace directory recursively with debouncing.
type Watcher struct {
	vfs          VFS
	fsWatcher    *fsnotify.Watcher
	debounceTime time.Duration
	handler      EventHandler

	mu      sync.Mutex
	timers  map[string]*time.Timer
	pending map[string]EventType
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewWatcher creates a new reactive filesystem watcher.
func NewWatcher(vfs VFS, debounceTime time.Duration, handler EventHandler) (*Watcher, error) {
	if debounceTime <= 0 {
		debounceTime = 350 * time.Millisecond
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed creating fsnotify watcher: %w", err)
	}

	w := &Watcher{
		vfs:          vfs,
		fsWatcher:    fsw,
		debounceTime: debounceTime,
		handler:      handler,
		timers:       make(map[string]*time.Timer),
		pending:      make(map[string]EventType),
		stopCh:       make(chan struct{}),
	}

	return w, nil
}

// Start begins monitoring the filesystem in the background.
func (w *Watcher) Start(ctx context.Context) error {
	// Add root and all subdirectories to watcher
	err := filepath.WalkDir(w.vfs.RootDir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return w.fsWatcher.Add(path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed walking directories for watcher: %w", err)
	}

	w.wg.Add(1)
	go w.eventLoop(ctx)

	return nil
}

// Stop terminates the watcher and waits for routines to finish.
func (w *Watcher) Stop() error {
	close(w.stopCh)
	err := w.fsWatcher.Close()
	w.wg.Wait()

	w.mu.Lock()
	for _, timer := range w.timers {
		timer.Stop()
	}
	w.timers = make(map[string]*time.Timer)
	w.pending = make(map[string]EventType)
	w.mu.Unlock()

	return err
}

func (w *Watcher) eventLoop(ctx context.Context) {
	defer w.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			w.handleFSEvent(event)
		case _, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			// Silently continue on non-fatal fsnotify errors
		}
	}
}

func (w *Watcher) handleFSEvent(event fsnotify.Event) {
	name := filepath.Base(event.Name)

	// Ignore hidden files, temporary files, and .trash
	if strings.HasPrefix(name, ".") || strings.Contains(event.Name, string(filepath.Separator)+".trash") {
		return
	}

	rel, err := filepath.Rel(w.vfs.RootDir(), event.Name)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}

	workspacePath := "/" + filepath.ToSlash(rel)

	// Check if a new directory was created, and add to watch list
	if event.Has(fsnotify.Create) {
		if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
			_ = w.fsWatcher.Add(event.Name)
			return
		}
	}

	var evType EventType
	if event.Has(fsnotify.Create) {
		evType = EventCreate
	} else if event.Has(fsnotify.Write) {
		evType = EventModify
	} else if event.Has(fsnotify.Remove) {
		evType = EventDelete
	} else if event.Has(fsnotify.Rename) {
		evType = EventRename
	} else {
		return
	}

	w.scheduleDebounced(workspacePath, evType)
}

func (w *Watcher) scheduleDebounced(workspacePath string, evType EventType) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Update pending event type (Delete overrides previous modifications)
	if evType == EventDelete {
		w.pending[workspacePath] = EventDelete
	} else if existing, ok := w.pending[workspacePath]; ok && existing == EventCreate {
		// Keep Create if it was just created
		w.pending[workspacePath] = EventCreate
	} else {
		w.pending[workspacePath] = evType
	}

	// Reset existing timer or create a new one
	if timer, ok := w.timers[workspacePath]; ok {
		timer.Stop()
	}

	w.timers[workspacePath] = time.AfterFunc(w.debounceTime, func() {
		w.mu.Lock()
		finalType, exists := w.pending[workspacePath]
		delete(w.pending, workspacePath)
		delete(w.timers, workspacePath)
		w.mu.Unlock()

		if exists && w.handler != nil {
			w.handler(FileEvent{
				WorkspaceID: w.vfs.WorkspaceID(),
				Path:        workspacePath,
				Type:        finalType,
				Timestamp:   time.Now(),
			})
		}
	})
}
