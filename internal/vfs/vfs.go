package vfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrInvalidPath     = errors.New("invalid or illegal file path")
	ErrPathEscapesRoot = errors.New("path escapes workspace root")
	ErrFileNotFound    = errors.New("file not found")
)

// FileInfo holds metadata about a file in the workspace VFS.
type FileInfo struct {
	Path       string    `json:"path"` // Workspace-relative path starting with "/"
	Name       string    `json:"name"`
	IsDir      bool      `json:"is_dir"`
	SizeBytes  int64     `json:"size_bytes"`
	ModifiedAt time.Time `json:"modified_at"`
	SHA256     string    `json:"sha256,omitempty"`
}

// VFS defines the filesystem-first storage interface.
type VFS interface {
	WorkspaceID() string
	RootDir() string
	NormalizePath(relPath string) (string, error)
	ToDiskPath(relPath string) (string, error)
	ReadFile(relPath string) ([]byte, error)
	WriteFileAtomic(relPath string, data []byte) error
	AppendFile(relPath string, data []byte) error
	MoveFile(srcPath, destPath string) error
	SafeDelete(relPath string) (string, error)
	ListFiles(prefix string, recursive bool) ([]FileInfo, error)
	ComputeHash(relPath string) (string, int64, error)
	FileExists(relPath string) (bool, error)
}

// DiskVFS implements VFS backed by a local filesystem directory.
type DiskVFS struct {
	workspaceID string
	rootDir     string
}

// NewDiskVFS creates a new DiskVFS instance for a given workspace.
func NewDiskVFS(workspaceID, rootDir string) (*DiskVFS, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("invalid root dir: %w", err)
	}

	if err := os.MkdirAll(absRoot, 0755); err != nil {
		return nil, fmt.Errorf("failed creating workspace root: %w", err)
	}

	// Ensure .trash directory exists
	trashDir := filepath.Join(absRoot, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		return nil, fmt.Errorf("failed creating trash dir: %w", err)
	}

	return &DiskVFS{
		workspaceID: workspaceID,
		rootDir:     absRoot,
	}, nil
}

func (v *DiskVFS) WorkspaceID() string {
	return v.workspaceID
}

func (v *DiskVFS) RootDir() string {
	return v.rootDir
}

// NormalizePath normalizes a workspace-relative path, ensuring leading slash and no directory traversal outside root.
func (v *DiskVFS) NormalizePath(relPath string) (string, error) {
	relPath = strings.ReplaceAll(relPath, "\\", "/")
	
	// Check for raw traversal patterns before root stripping
	if strings.HasPrefix(relPath, "../") || relPath == ".." || strings.Contains(relPath, "/../") || strings.HasSuffix(relPath, "/..") {
		return "", ErrPathEscapesRoot
	}

	trimmed := strings.TrimPrefix(relPath, "/")
	cleaned := filepath.ToSlash(filepath.Clean(trimmed))

	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrPathEscapesRoot
	}

	if cleaned == "." || cleaned == "" {
		return "/", nil
	}

	return "/" + cleaned, nil
}

// ToDiskPath converts a normalized workspace path to an absolute path on disk.
func (v *DiskVFS) ToDiskPath(relPath string) (string, error) {
	norm, err := v.NormalizePath(relPath)
	if err != nil {
		return "", err
	}

	trimmed := strings.TrimPrefix(norm, "/")
	diskPath := filepath.Join(v.rootDir, filepath.FromSlash(trimmed))

	// Verify that the disk path is within rootDir
	rel, err := filepath.Rel(v.rootDir, diskPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", ErrPathEscapesRoot
	}

	return diskPath, nil
}

// ReadFile reads the full contents of a workspace file.
func (v *DiskVFS) ReadFile(relPath string) ([]byte, error) {
	diskPath, err := v.ToDiskPath(relPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(diskPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("failed reading file %s: %w", relPath, err)
	}

	return data, nil
}

// WriteFileAtomic writes data to a temporary file first, then atomically renames it.
func (v *DiskVFS) WriteFileAtomic(relPath string, data []byte) error {
	diskPath, err := v.ToDiskPath(relPath)
	if err != nil {
		return err
	}

	dir := filepath.Dir(diskPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed creating directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".engram_tmp_*")
	if err != nil {
		return fmt.Errorf("failed creating temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName) // safe cleanup if rename fails
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("failed writing data to temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed syncing temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed closing temp file: %w", err)
	}

	if err := os.Rename(tmpName, diskPath); err != nil {
		return fmt.Errorf("failed atomic rename to %s: %w", diskPath, err)
	}

	return nil
}

// AppendFile appends data to an existing file or creates it if it doesn't exist.
func (v *DiskVFS) AppendFile(relPath string, data []byte) error {
	diskPath, err := v.ToDiskPath(relPath)
	if err != nil {
		return err
	}

	dir := filepath.Dir(diskPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed creating directory %s: %w", dir, err)
	}

	f, err := os.OpenFile(diskPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed opening file %s for append: %w", relPath, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed appending to file %s: %w", relPath, err)
	}

	return nil
}

// MoveFile renames or moves a file on disk within the workspace.
func (v *DiskVFS) MoveFile(srcPath, destPath string) error {
	srcDisk, err := v.ToDiskPath(srcPath)
	if err != nil {
		return err
	}
	destDisk, err := v.ToDiskPath(destPath)
	if err != nil {
		return err
	}

	destDir := filepath.Dir(destDisk)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed creating destination directory: %w", err)
	}

	if err := os.Rename(srcDisk, destDisk); err != nil {
		return fmt.Errorf("failed moving file from %s to %s: %w", srcPath, destPath, err)
	}

	return nil
}

// SafeDelete moves a deleted file into .trash/ with a timestamp suffix instead of destroying it.
func (v *DiskVFS) SafeDelete(relPath string) (string, error) {
	srcDisk, err := v.ToDiskPath(relPath)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(srcDisk); err != nil {
		if os.IsNotExist(err) {
			return "", ErrFileNotFound
		}
		return "", err
	}

	baseName := filepath.Base(srcDisk)
	timestamp := time.Now().Format("20060102_150405")
	trashRelPath := filepath.Join(".trash", fmt.Sprintf("%s_%s", timestamp, baseName))
	trashDisk := filepath.Join(v.rootDir, trashRelPath)

	if err := os.Rename(srcDisk, trashDisk); err != nil {
		return "", fmt.Errorf("failed moving file to trash: %w", err)
	}

	return "/" + filepath.ToSlash(trashRelPath), nil
}

// ListFiles walks the workspace filesystem and returns file entries.
func (v *DiskVFS) ListFiles(prefix string, recursive bool) ([]FileInfo, error) {
	normPrefix, err := v.NormalizePath(prefix)
	if err != nil {
		return nil, err
	}

	startDiskPath, err := v.ToDiskPath(normPrefix)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(startDiskPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileInfo{}, nil
		}
		return nil, err
	}

	var results []FileInfo

	if !stat.IsDir() {
		// Single file
		h, size, _ := v.ComputeHash(normPrefix)
		results = append(results, FileInfo{
			Path:       normPrefix,
			Name:       stat.Name(),
			IsDir:      false,
			SizeBytes:  size,
			ModifiedAt: stat.ModTime(),
			SHA256:     h,
		})
		return results, nil
	}

	if !recursive {
		// Only immediate children
		entries, err := os.ReadDir(startDiskPath)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue // ignore hidden files and .trash
			}
			childRel := filepath.ToSlash(filepath.Join(strings.TrimPrefix(normPrefix, "/"), name))
			childNorm := "/" + childRel

			info, err := entry.Info()
			if err != nil {
				continue
			}

			var sha string
			if !entry.IsDir() {
				sha, _, _ = v.ComputeHash(childNorm)
			}

			results = append(results, FileInfo{
				Path:       childNorm,
				Name:       name,
				IsDir:      entry.IsDir(),
				SizeBytes:  info.Size(),
				ModifiedAt: info.ModTime(),
				SHA256:     sha,
			})
		}
		return results, nil
	}

	// Recursive walk
	err = filepath.WalkDir(startDiskPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // continue on error
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(v.rootDir, path)
		if err != nil || rel == "." {
			return nil
		}

		workspacePath := "/" + filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return nil
		}

		var sha string
		if !d.IsDir() {
			sha, _, _ = v.ComputeHash(workspacePath)
		}

		results = append(results, FileInfo{
			Path:       workspacePath,
			Name:       name,
			IsDir:      d.IsDir(),
			SizeBytes:  info.Size(),
			ModifiedAt: info.ModTime(),
			SHA256:     sha,
		})
		return nil
	})

	return results, err
}

// ComputeHash computes the SHA-256 hash and size of a file.
func (v *DiskVFS) ComputeHash(relPath string) (string, int64, error) {
	diskPath, err := v.ToDiskPath(relPath)
	if err != nil {
		return "", 0, err
	}

	f, err := os.Open(diskPath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", 0, err
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(h.Sum(nil)), stat.Size(), nil
}

// FileExists checks whether a file exists at the given relative path.
func (v *DiskVFS) FileExists(relPath string) (bool, error) {
	diskPath, err := v.ToDiskPath(relPath)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(diskPath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
