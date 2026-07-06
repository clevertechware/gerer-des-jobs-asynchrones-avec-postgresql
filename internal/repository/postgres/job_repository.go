package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/domain"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/repository"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// JobRepository implements repository.JobRepository using PostgreSQL
type JobRepository struct {
	txManager *PGTxManager
}

// NewJobRepository creates a new JobRepository
func NewJobRepository(txManager *PGTxManager) repository.JobRepository {
	return &JobRepository{txManager: txManager}
}

// jobColumns is the shared column list for SELECT/UPDATE...RETURNING queries against jobs,
// in the order scanJob expects them.
const jobColumns = "id, tenant_id, type, status, operated_by, config, result, error, trace_id, started_at, finished_at, duration_ms, created_at, updated_at, run_after, attempts, max_attempts"

// scanJob scans a single row (from QueryRow or Query.Next) into a domain.Job, matching jobColumns.
func scanJob(row pgx.Row) (*domain.Job, error) {
	var job domain.Job
	err := row.Scan(
		&job.ID,
		&job.TenantID,
		&job.Type,
		&job.Status,
		&job.OperatedBy,
		&job.Config,
		&job.Result,
		&job.Error,
		&job.TraceID,
		&job.StartedAt,
		&job.FinishedAt,
		&job.DurationMs,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.RunAfter,
		&job.Attempts,
		&job.MaxAttempts,
	)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// Create creates a new job and returns it with assigned ID
func (r *JobRepository) Create(ctx context.Context, tenantID string, jobType domain.JobType, config interface{}) (*domain.Job, error) {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant ID: %w", err)
	}

	const query = `
		INSERT INTO jobs (tenant_id, type, config)
		VALUES ($1, $2, $3)
		RETURNING id, tenant_id, type, status, config, result, error, trace_id, started_at, finished_at, duration_ms, created_at, updated_at, run_after, attempts, max_attempts
	`

	pgClient := r.txManager.GetClient(ctx)
	var job domain.Job
	err = pgClient.QueryRow(ctx, query, tenantUUID, jobType, configJSON).Scan(
		&job.ID,
		&job.TenantID,
		&job.Type,
		&job.Status,
		&job.Config,
		&job.Result,
		&job.Error,
		&job.TraceID,
		&job.StartedAt,
		&job.FinishedAt,
		&job.DurationMs,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.RunAfter,
		&job.Attempts,
		&job.MaxAttempts,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	return &job, nil
}

// GetByID retrieves a job by its ID
func (r *JobRepository) GetByID(ctx context.Context, id int64) (*domain.Job, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM jobs
		WHERE id = $1
	`, jobColumns)

	pgClient := r.txManager.GetClient(ctx)
	job, err := scanJob(pgClient.QueryRow(ctx, query, id))
	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	return job, nil
}

// Dequeue retrieves and locks a batch of pending jobs for processing
// Uses FOR UPDATE SKIP LOCKED to ensure jobs are only processed once
func (r *JobRepository) Dequeue(ctx context.Context, batchSize int) ([]*domain.Job, error) {
	query := fmt.Sprintf(`
		UPDATE jobs
		SET status = 'RUNNING',
		    started_at = now(),
		    updated_at = now(),
		    attempts = attempts + 1
		WHERE id IN (
			SELECT id
			FROM jobs
			WHERE status = 'PENDING'
			  AND (run_after IS NULL OR run_after <= now())
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		RETURNING %s
	`, jobColumns)

	pgClient := r.txManager.GetClient(ctx)
	rows, err := pgClient.Query(ctx, query, batchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]*domain.Job, 0, batchSize)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		jobs = append(jobs, job)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating jobs: %w", err)
	}

	return jobs, nil
}

// UpdateStatus updates the job status and related fields
func (r *JobRepository) UpdateStatus(ctx context.Context, id int64, status domain.JobStatus, result interface{}, errMsg *string, durationMs *int64) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	const query = `
		UPDATE jobs
		SET status = $2,
		    result = result || $3::jsonb,
		    error = $4,
		    finished_at = now(),
		    duration_ms = $5,
		    updated_at = now()
		WHERE id = $1
	`

	pgClient := r.txManager.GetClient(ctx)
	_, err = pgClient.Exec(ctx, query, id, status, resultJSON, errMsg, durationMs)
	if err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	return nil
}

// UpdateToPending resets a job to PENDING status (for retries with backoff)
func (r *JobRepository) UpdateToPending(ctx context.Context, id int64, runAfter *string, errorMsg *string) error {
	const query = `
		UPDATE jobs
		SET status = 'PENDING',
		    error = $2,
		    run_after = $3,
		    updated_at = now()
		WHERE id = $1
	`

	pgClient := r.txManager.GetClient(ctx)
	_, err := pgClient.Exec(ctx, query, id, errorMsg, runAfter)
	if err != nil {
		return fmt.Errorf("failed to update job to pending: %w", err)
	}

	return nil
}

// GetQueueStats returns statistics about the job queue
func (r *JobRepository) GetQueueStats(ctx context.Context) (*domain.QueueStats, error) {
	const query = `
		SELECT type, status, COUNT(*) as count
		FROM jobs
		WHERE status IN ('PENDING', 'RUNNING', 'COMPLETED', 'COMPLETED_WITH_ERRORS', 'FAILED')
		GROUP BY type, status
	`

	pgClient := r.txManager.GetClient(ctx)
	rows, err := pgClient.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue stats: %w", err)
	}
	defer rows.Close()

	stats := &domain.QueueStats{
		ByType: make(map[domain.JobType]domain.JobStats),
	}

	for rows.Next() {
		var jobType string
		var status string
		var count int
		if err = rows.Scan(&jobType, &status, &count); err != nil {
			return nil, fmt.Errorf("failed to scan stats: %w", err)
		}

		jobTypeEnum := domain.JobType(jobType)
		statusEnum := domain.JobStatus(status)

		// Get or create job stats for this type
		jobStats, exists := stats.ByType[jobTypeEnum]
		if !exists {
			jobStats = domain.JobStats{Type: jobTypeEnum}
			stats.ByType[jobTypeEnum] = jobStats
		}

		switch statusEnum {
		case domain.JobStatusPending:
			jobStats.Pending += count
			stats.ByType[jobTypeEnum] = jobStats
			stats.TotalPending += count
		case domain.JobStatusRunning:
			jobStats.Running += count
			stats.ByType[jobTypeEnum] = jobStats
			stats.TotalRunning += count
		case domain.JobStatusCompleted, domain.JobStatusCompletedWithErr:
			jobStats.Completed += count
			stats.ByType[jobTypeEnum] = jobStats
		case domain.JobStatusFailed:
			jobStats.Failed += count
			stats.ByType[jobTypeEnum] = jobStats
		}
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating stats: %w", err)
	}

	return stats, nil
}

// GetJobsByStatus returns jobs filtered by status
func (r *JobRepository) GetJobsByStatus(ctx context.Context, status domain.JobStatus, limit int) ([]*domain.Job, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM jobs
		WHERE status = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, jobColumns)

	pgClient := r.txManager.GetClient(ctx)
	rows, err := pgClient.Query(ctx, query, status, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs by status: %w", err)
	}
	defer rows.Close()

	jobs := make([]*domain.Job, 0, limit)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		jobs = append(jobs, job)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating jobs: %w", err)
	}

	return jobs, nil
}
