package embedder

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/db"
)

// WorkerPool processes pending embedding jobs from PostgreSQL asynchronously.
type WorkerPool struct {
	database  *db.DB
	embedder  Embedder
	batchSize int
	interval  time.Duration
	wakeCh    chan struct{}
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// NewWorkerPool creates a new resilient embedding worker pool.
func NewWorkerPool(database *db.DB, embedder Embedder, batchSize int, interval time.Duration) *WorkerPool {
	if batchSize <= 0 {
		batchSize = 16
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}

	return &WorkerPool{
		database:  database,
		embedder:  embedder,
		batchSize: batchSize,
		interval:  interval,
		wakeCh:    make(chan struct{}, 1),
		stopCh:    make(chan struct{}),
	}
}

// Start launches the worker loop in the background.
func (wp *WorkerPool) Start(ctx context.Context) {
	wp.wg.Add(1)
	go wp.loop(ctx)
}

// Trigger wakes up the worker loop immediately to process newly queued jobs.
func (wp *WorkerPool) Trigger() {
	select {
	case wp.wakeCh <- struct{}{}:
	default:
	}
}

// Stop cleanly terminates the worker pool.
func (wp *WorkerPool) Stop() {
	close(wp.stopCh)
	wp.wg.Wait()
}

func (wp *WorkerPool) loop(ctx context.Context) {
	defer wp.wg.Done()

	ticker := time.NewTicker(wp.interval)
	defer ticker.Stop()

	for {
		// Process available jobs until queue is empty
		for {
			processed, err := wp.processBatch(ctx)
			if err != nil {
				// Wait briefly on error to prevent CPU spinning
				time.Sleep(1 * time.Second)
				break
			}
			if processed == 0 {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-wp.stopCh:
			return
		case <-wp.wakeCh:
			// Woken up by new job notification
		case <-ticker.C:
			// Scheduled poll
		}
	}
}

// processBatch fetches and processes one batch of embedding jobs.
func (wp *WorkerPool) processBatch(ctx context.Context) (int, error) {
	jobs, err := wp.database.FetchPendingEmbeddingJobs(ctx, wp.batchSize)
	if err != nil {
		return 0, fmt.Errorf("failed fetching pending jobs: %w", err)
	}

	if len(jobs) == 0 {
		return 0, nil
	}

	// Prepare input texts
	texts := make([]string, len(jobs))
	for i, j := range jobs {
		texts[i] = j.InputText
	}

	// Generate embeddings using the configured provider
	embeddings, err := wp.embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		// Embedder failed for the batch: mark all jobs as failed with exponential backoff
		for _, j := range jobs {
			backoff := calculateBackoff(j.Attempts)
			_ = wp.database.FailEmbeddingJob(ctx, j.ID, err.Error(), backoff)
		}
		return len(jobs), err
	}

	// Save completed embeddings
	for i, j := range jobs {
		if i < len(embeddings) {
			if err := wp.database.CompleteEmbeddingJob(ctx, j.ID, embeddings[i]); err != nil {
				backoff := calculateBackoff(j.Attempts)
				_ = wp.database.FailEmbeddingJob(ctx, j.ID, err.Error(), backoff)
			}
		}
	}

	return len(jobs), nil
}

// calculateBackoff computes exponential backoff: 2s, 4s, 8s, 16s... up to 5 minutes.
func calculateBackoff(attempts int) time.Duration {
	secs := math.Pow(2, float64(attempts+1))
	if secs > 300 {
		secs = 300
	}
	return time.Duration(secs) * time.Second
}
