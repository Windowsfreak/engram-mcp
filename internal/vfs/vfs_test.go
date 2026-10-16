package vfs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/vfs"
)

func TestDiskVFSBasicOperations(t *testing.T) {
	tempDir := t.TempDir()
	diskVFS, err := vfs.NewDiskVFS("test_ws", tempDir)
	if err != nil {
		t.Fatalf("failed creating DiskVFS: %v", err)
	}

	testPath := "/atlas/overview.md"
	testContent := []byte("# System Architecture\n\nThis is a test note.")

	// Test Atomic Write
	if err := diskVFS.WriteFileAtomic(testPath, testContent); err != nil {
		t.Fatalf("failed writing file atomically: %v", err)
	}

	// Test FileExists
	exists, err := diskVFS.FileExists(testPath)
	if err != nil || !exists {
		t.Errorf("expected file to exist: %v, exists: %v", err, exists)
	}

	// Test ReadFile
	readData, err := diskVFS.ReadFile(testPath)
	if err != nil {
		t.Fatalf("failed reading file: %v", err)
	}
	if string(readData) != string(testContent) {
		t.Errorf("content mismatch, got: %s", string(readData))
	}

	// Test ComputeHash
	hash, size, err := diskVFS.ComputeHash(testPath)
	if err != nil || hash == "" || size != int64(len(testContent)) {
		t.Errorf("hash computation error: %v, hash: %s, size: %d", err, hash, size)
	}

	// Test AppendFile
	appendData := []byte("\n\n## Section 2\nAdditional details.")
	if err := diskVFS.AppendFile(testPath, appendData); err != nil {
		t.Fatalf("failed appending to file: %v", err)
	}

	updatedData, err := diskVFS.ReadFile(testPath)
	if err != nil {
		t.Fatalf("failed reading appended file: %v", err)
	}
	expectedUpdated := string(testContent) + string(appendData)
	if string(updatedData) != expectedUpdated {
		t.Errorf("appended content mismatch: %s", string(updatedData))
	}

	// Test MoveFile
	newPath := "/atlas/renamed_overview.md"
	if err := diskVFS.MoveFile(testPath, newPath); err != nil {
		t.Fatalf("failed moving file: %v", err)
	}

	existsOld, _ := diskVFS.FileExists(testPath)
	if existsOld {
		t.Error("old file should no longer exist after move")
	}
	existsNew, _ := diskVFS.FileExists(newPath)
	if !existsNew {
		t.Error("new file should exist after move")
	}

	// Test ListFiles
	files, err := diskVFS.ListFiles("/", true)
	if err != nil {
		t.Fatalf("failed listing files: %v", err)
	}
	foundRenamed := false
	for _, f := range files {
		if f.Path == newPath && !f.IsDir {
			foundRenamed = true
			break
		}
	}
	if !foundRenamed {
		t.Errorf("expected renamed file %s in list output: %+v", newPath, files)
	}

	// Test SafeDelete
	trashPath, err := diskVFS.SafeDelete(newPath)
	if err != nil {
		t.Fatalf("failed safe deleting: %v", err)
	}
	if trashPath == "" {
		t.Error("expected trash path returned")
	}
	existsAfterDelete, _ := diskVFS.FileExists(newPath)
	if existsAfterDelete {
		t.Error("file should be removed from active location after safe delete")
	}
}

func TestVFSPathEscapes(t *testing.T) {
	tempDir := t.TempDir()
	diskVFS, err := vfs.NewDiskVFS("test_ws", tempDir)
	if err != nil {
		t.Fatalf("failed creating DiskVFS: %v", err)
	}

	illegalPaths := []string{
		"/../etc/passwd",
		"../../secret.txt",
		"/atlas/../../outside.md",
		"..\\..\\secret.txt",
		"\\..\\secret.txt",
		"C:\\Windows\\System32",
		"C:/Windows/System32",
		"d:\\data\\workspaces",
		// UNC paths and device namespaces
		`\\server\share\file.md`,
		`//server/share/file.md`,
		`\\?\C:\Windows\System32`,
		`\\.\COM1`,
		`file://remote-server/share/file.md`,
		`file:///C:/Windows/System32/cmd.exe`,
	}

	for _, p := range illegalPaths {
		_, err := diskVFS.NormalizePath(p)
		if err != vfs.ErrPathEscapesRoot {
			t.Errorf("expected ErrPathEscapesRoot for %s, got: %v", p, err)
		}
	}

	// Valid Windows paths and file:// URIs should normalize to POSIX workspace paths
	validPaths := map[string]string{
		`atlas\sub\note.md`:                   "/atlas/sub/note.md",
		`\atlas\sub\note.md`:                  "/atlas/sub/note.md",
		`episodes\daily.md`:                   "/episodes/daily.md",
		`file:///atlas/sub/note.md`:           "/atlas/sub/note.md",
		`file://localhost/atlas/sub/note.md`:  "/atlas/sub/note.md",
		`file:///atlas/sub/my%20note.md`:      "/atlas/sub/my note.md",
		"file://" + filepath.ToSlash(tempDir) + "/atlas/note.md": "/atlas/note.md",
	}
	for input, expected := range validPaths {
		norm, err := diskVFS.NormalizePath(input)
		if err != nil || norm != expected {
			t.Errorf("NormalizePath(%q) = %q, err: %v; want %q", input, norm, err, expected)
		}
	}
}

func TestVFSWatcherDebouncing(t *testing.T) {
	tempDir := t.TempDir()
	diskVFS, err := vfs.NewDiskVFS("test_ws", tempDir)
	if err != nil {
		t.Fatalf("failed creating DiskVFS: %v", err)
	}

	eventsReceived := make(chan vfs.FileEvent, 10)
	watcher, err := vfs.NewWatcher(diskVFS, 100*time.Millisecond, func(event vfs.FileEvent) {
		eventsReceived <- event
	})
	if err != nil {
		t.Fatalf("failed creating watcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := watcher.Start(ctx); err != nil {
		t.Fatalf("failed starting watcher: %v", err)
	}
	defer watcher.Stop()

	// Rapidly write to the same file multiple times (simulating autosave)
	testFile := filepath.Join(tempDir, "rapid_write.md")
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(testFile, []byte("change"), 0644)
		time.Sleep(10 * time.Millisecond)
	}

	// Should receive only 1 debounced event within 300ms
	select {
	case ev := <-eventsReceived:
		if ev.Path != "/rapid_write.md" {
			t.Errorf("unexpected event path: %s", ev.Path)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for debounced watcher event")
	}

	// Verify no immediate second event follows (it was debounced)
	select {
	case ev2 := <-eventsReceived:
		t.Errorf("expected debounce to suppress extra events, but received: %+v", ev2)
	case <-time.After(150 * time.Millisecond):
		// Success!
	}
}
