package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"csv-job-processor/internal/domain"
	"csv-job-processor/internal/repository"
)

// JobHandler is a function that processes a specific type of job
type JobHandler func(ctx context.Context, config json.RawMessage) (json.RawMessage, error)

// Worker processes jobs from the queue
type Worker struct {
	db           *pgxpool.Pool
	repo         repository.JobRepository
	handlers     map[domain.JobType]JobHandler
	workerID     uuid.UUID
	batchSize    int
	pollInterval time.Duration
	concurrency  int
}

// WorkerOption is a function that configures a Worker
type WorkerOption func(*Worker)

// WithBatchSize sets the batch size for dequeueing jobs
func WithBatchSize(size int) WorkerOption {
	return func(w *Worker) {
		w.batchSize = size
	}
}

// WithPollInterval sets the poll interval
func WithPollInterval(interval time.Duration) WorkerOption {
	return func(w *Worker) {
		w.pollInterval = interval
	}
}

// WithConcurrency sets the number of concurrent workers
func WithConcurrency(n int) WorkerOption {
	return func(w *Worker) {
		w.concurrency = n
	}
}

// NewWorker creates a new Worker
func NewWorker(db *pgxpool.Pool, repo repository.JobRepository, opts ...WorkerOption) *Worker {
	w := &Worker{
		db:           db,
		repo:         repo,
		handlers:     make(map[domain.JobType]JobHandler),
		workerID:     uuid.New(),
		batchSize:    5,
		pollInterval: 100 * time.Millisecond,
		concurrency:  1,
	}

	for _, opt := range opts {
		opt(w)
	}

	return w
}

// RegisterHandler registers a handler for a specific job type
func (w *Worker) RegisterHandler(jobType domain.JobType, handler JobHandler) {
	w.handlers[jobType] = handler
}

// Run starts the worker loop
func (w *Worker) Run(ctx context.Context) error {
	log.Printf("Starting worker %s with batch size %d, poll interval %v, concurrency %d",
		w.workerID, w.batchSize, w.pollInterval, w.concurrency)

	// Create a channel for job batches
	jobChan := make(chan []*domain.Job, w.concurrency)

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go w.processWorker(ctx, &wg, jobChan)
	}

	// Start dequeue loop
	for {
		select {
		case <-ctx.Done():
			close(jobChan)
			wg.Wait()
			log.Printf("Worker %s stopped", w.workerID)
			return nil

		case <-time.After(w.pollInterval):
			// Dequeue jobs
			jobs, err := w.repo.Dequeue(ctx, w.batchSize)
			if err != nil {
				log.Printf("Error dequeueing jobs: %v", err)
				continue
			}

			if len(jobs) == 0 {
				continue
			}

			// Send jobs to worker channel
			jobChan <- jobs
		}
	}
}

// processWorker processes a batch of jobs
func (w *Worker) processWorker(ctx context.Context, wg *sync.WaitGroup, jobChan <-chan []*domain.Job) {
	defer wg.Done()

	for jobs := range jobChan {
		for _, job := range jobs {
			// Check if context is cancelled
			if ctx.Err() != nil {
				return
			}

			// Process the job
			w.processJob(ctx, job)
		}
	}
}

// processJob processes a single job
func (w *Worker) processJob(ctx context.Context, job *domain.Job) {
	start := time.Now()

	// Mark job as being processed by this worker
	job.OperatedBy = &w.workerID

	// Get handler for job type
	handler, ok := w.handlers[job.Type]
	if !ok {
		w.failJob(ctx, job.ID, fmt.Sprintf("unknown job type: %s", job.Type), start)
		return
	}

	// Execute the handler
	result, err := handler(ctx, job.Config)
	if err != nil {
		// Handle retries
		if w.shouldRetry(job) {
			w.requeueJob(ctx, job, err, start)
		} else {
			w.failJob(ctx, job.ID, err.Error(), start)
		}
		return
	}

	// Job completed successfully
	durationMs := time.Since(start).Milliseconds()
	errMsg := ""

	if err := w.repo.UpdateStatus(ctx, job.ID, domain.JobStatusCompleted, result, &errMsg, &durationMs); err != nil {
		log.Printf("Failed to update job %d status: %v", job.ID, err)
	}
}

// shouldRetry determines if a job should be retried
func (w *Worker) shouldRetry(job *domain.Job) bool {
	// Don't retry if max attempts reached
	if job.Attempts >= job.MaxAttempts && job.MaxAttempts > 0 {
		return false
	}

	// Always retry for now (in production, you might want more sophisticated logic)
	return true
}

// requeueJob requeues a job with exponential backoff
func (w *Worker) requeueJob(ctx context.Context, job *domain.Job, err error, start time.Time) {
	durationMs := time.Since(start).Milliseconds()

	// Calculate backoff: 2^(attempts-1) minutes
	backoffMinutes := 1 << (job.Attempts - 1) // 1, 2, 4, 8, ...
	if backoffMinutes > 60 {
		backoffMinutes = 60 // Cap at 60 minutes
	}

	runAfter := time.Now().Add(time.Duration(backoffMinutes) * time.Minute).Format(time.RFC3339)
	errMsg := err.Error()

	// Update job to PENDING with backoff
	if err := w.repo.UpdateToPending(ctx, job.ID, &runAfter, &errMsg); err != nil {
		log.Printf("Failed to requeue job %d: %v", job.ID, err)
		// If requeue fails, mark as failed
		if updateErr := w.repo.UpdateStatus(ctx, job.ID, domain.JobStatusFailed, nil, &errMsg, &durationMs); updateErr != nil {
			log.Printf("Failed to update job %d to failed: %v", job.ID, updateErr)
		}
	}
}

// failJob marks a job as failed
func (w *Worker) failJob(ctx context.Context, jobID int64, errMsg string, start time.Time) {
	durationMs := time.Since(start).Milliseconds()

	if err := w.repo.UpdateStatus(ctx, jobID, domain.JobStatusFailed, nil, &errMsg, &durationMs); err != nil {
		log.Printf("Failed to update job %d to failed: %v", jobID, err)
	}
}

// Reaper cleans up stuck jobs (jobs that have been running too long)
// This should be run as a separate goroutine
func (w *Worker) Reaper(ctx context.Context, interval time.Duration, maxDuration time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reapStuckJobs(ctx, maxDuration)
		}
	}
}

// reapStuckJobs finds and requeues jobs that have been running too long
func (w *Worker) reapStuckJobs(ctx context.Context, maxDuration time.Duration) {
	const query = `
		UPDATE jobs
		SET status = 'PENDING',
		    error = 'Job was stuck and reaped',
		    run_after = NULL,
		    updated_at = now()
		WHERE status = 'RUNNING'
		  AND started_at < now() - $1::interval
		RETURNING id
	`

	rows, err := w.db.Query(ctx, query, fmt.Sprintf("%d minutes", int(maxDuration.Minutes())))
	if err != nil {
		log.Printf("Error reaping stuck jobs: %v", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			log.Printf("Error scanning reaped job: %v", err)
			continue
		}
		count++
		log.Printf("Reaped stuck job %d", id)
	}

	if count > 0 {
		log.Printf("Reaped %d stuck jobs", count)
	}
}

// ErrJobNotRegistered is returned when trying to start a worker without handlers
var ErrJobNotRegistered = errors.New("no handlers registered for any job type")
