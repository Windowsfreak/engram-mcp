package vfs

import (
	"context"
	"fmt"
	"strings"

	"github.com/windowsfreak/engram-mcp/internal/db"
)

// ReconcileResult summarizes the reconciliation scan.
type ReconcileResult struct {
	CreatedCount   int
	UpdatedCount   int
	DeletedCount   int
	UnchangedCount int
	Errors         []string
}

// IngestCallback is invoked when a file needs to be ingested/re-indexed.
type IngestCallback func(ctx context.Context, workspaceID, relPath string) error

// Reconciler reconciles the filesystem state with the database index.
type Reconciler struct {
	vfs      VFS
	database *db.DB
	onIngest IngestCallback
}

// NewReconciler creates a new filesystem reconciler.
func NewReconciler(vfs VFS, database *db.DB, onIngest IngestCallback) *Reconciler {
	return &Reconciler{
		vfs:      vfs,
		database: database,
		onIngest: onIngest,
	}
}

// Reconcile executes a full reconciliation between disk and database.
func (r *Reconciler) Reconcile(ctx context.Context) (*ReconcileResult, error) {
	wsID := r.vfs.WorkspaceID()
	res := &ReconcileResult{}

	// 1. Fetch all documents currently registered in the database for this workspace
	dbDocs, err := r.database.ListDocuments(ctx, wsID, "")
	if err != nil {
		return nil, fmt.Errorf("failed listing db documents: %w", err)
	}

	dbDocMap := make(map[string]*db.Document)
	for _, doc := range dbDocs {
		dbDocMap[doc.Path] = doc
	}

	// 2. Walk disk to find all current files
	diskFiles, err := r.vfs.ListFiles("/", true)
	if err != nil {
		return nil, fmt.Errorf("failed listing disk files: %w", err)
	}

	diskMap := make(map[string]FileInfo)
	for _, fi := range diskFiles {
		if fi.IsDir {
			continue
		}
		// Skip non-supported or temporary files if needed
		if strings.HasSuffix(fi.Name, ".tmp") {
			continue
		}
		diskMap[fi.Path] = fi
	}

	// 3. Process disk files against DB
	for path, diskFile := range diskMap {
		existingDoc, existsInDB := dbDocMap[path]
		if !existsInDB {
			// New file on disk
			res.CreatedCount++
			if r.onIngest != nil {
				if err := r.onIngest(ctx, wsID, path); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("ingest error %s: %v", path, err))
				}
			}
		} else if existingDoc.SHA256 != diskFile.SHA256 {
			// Modified file on disk
			res.UpdatedCount++
			if r.onIngest != nil {
				if err := r.onIngest(ctx, wsID, path); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("update error %s: %v", path, err))
				}
			}
		} else {
			// Unchanged
			res.UnchangedCount++
		}
	}

	// 4. Identify deleted files (in DB but no longer on disk)
	for path := range dbDocMap {
		if _, existsOnDisk := diskMap[path]; !existsOnDisk {
			res.DeletedCount++
			if err := r.database.DeleteDocument(ctx, wsID, path); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("failed deleting %s from db: %v", path, err))
			}
		}
	}

	return res, nil
}
