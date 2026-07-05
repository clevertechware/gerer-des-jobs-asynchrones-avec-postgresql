package repository

import (
	"context"

	"csv-job-processor/internal/domain"
)

// JobRepository defines the interface for job persistence
type JobRepository interface {
	// Create creates a new job and returns it with assigned ID
	Create(ctx context.Context, tenantID string, jobType domain.JobType, config interface{}) (*domain.Job, error)

	// GetByID retrieves a job by its ID
	GetByID(ctx context.Context, id int64) (*domain.Job, error)

	// Dequeue retrieves and locks a batch of pending jobs for processing
	// The jobs are automatically marked as RUNNING and their started_at timestamp is set
	Dequeue(ctx context.Context, batchSize int) ([]*domain.Job, error)

	// UpdateStatus updates the job status and related fields
	UpdateStatus(ctx context.Context, id int64, status domain.JobStatus, result interface{}, errMsg *string, durationMs *int64) error

	// UpdateToPending resets a job to PENDING status (for retries)
	UpdateToPending(ctx context.Context, id int64, runAfter *string, errorMsg *string) error

	// GetQueueStats returns statistics about the job queue
	GetQueueStats(ctx context.Context) (*domain.QueueStats, error)

	// GetJobsByStatus returns jobs filtered by status
	GetJobsByStatus(ctx context.Context, status domain.JobStatus, limit int) ([]*domain.Job, error)
}
