package postgres

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"csv-job-processor/internal/domain"
)

// TestJobRepository_Create tests the Create method
func (s *PostgresSuite) TestJobRepository_Create() {
	t := s.T()
	t.Parallel()

	t.Run("should create job with all fields", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		testTenantID := uuid.New().String()
		config := domain.CSVImportConfig{
			FilePath:    "/path/to/file.csv",
			Delimiter:   ",",
			HasHeader:   true,
			TargetTable: "users",
		}

		job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
		require.NoError(t, err, "Failed to create job")

		assert.NotZero(t, job.ID, "Expected job ID to be set")
		assert.NotZero(t, job.TenantID.String(), "Expected tenant ID to be set")
		assert.Equal(t, domain.JobTypeCSVImport, job.Type, "Expected job type to match")
		assert.Equal(t, domain.JobStatusPending, job.Status, "Expected job status to be PENDING")

		// Verify config is stored
		var storedConfig domain.CSVImportConfig
		err = json.Unmarshal(job.Config, &storedConfig)
		require.NoError(t, err, "Failed to unmarshal stored config")

		assert.Equal(t, config.FilePath, storedConfig.FilePath, "Expected file path to match")
		assert.Equal(t, config.Delimiter, storedConfig.Delimiter, "Expected delimiter to match")
		assert.Equal(t, config.HasHeader, storedConfig.HasHeader, "Expected has header to match")
		assert.Equal(t, config.TargetTable, storedConfig.TargetTable, "Expected target table to match")
	})

	t.Run("should create multiple jobs with different configs", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		testTenantID := uuid.New().String()

		// Create multiple jobs
		jobs := make([]*domain.Job, 3)
		for i := 0; i < 3; i++ {
			config := domain.CSVImportConfig{
				FilePath:    fmt.Sprintf("/path/to/file%d.csv", i),
				Delimiter:   ",",
				HasHeader:   true,
				TargetTable: "users",
			}
			job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
			require.NoError(t, err, "Failed to create job %d", i)
			jobs[i] = job
		}

		// Verify all jobs have unique IDs
		ids := make(map[int64]bool)
		for _, job := range jobs {
			assert.False(t, ids[job.ID], "Expected unique job IDs")
			ids[job.ID] = true
		}

		// Verify all jobs are in PENDING status
		for _, job := range jobs {
			assert.Equal(t, domain.JobStatusPending, job.Status, "Expected job status to be PENDING")
		}
	})
}

// TestJobRepository_GetByID tests the GetByID method
func (s *PostgresSuite) TestJobRepository_GetByID() {
	t := s.T()
	t.Parallel()

	t.Run("should retrieve job by ID", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create a job first
		testTenantID := uuid.New().String()
		config := domain.CSVImportConfig{
			FilePath: "/path/to/file.csv",
		}

		createdJob, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
		require.NoError(t, err, "Failed to create job")

		// Get the job by ID
		job, err := repo.GetByID(txCtx, createdJob.ID)
		require.NoError(t, err, "Failed to get job")

		assert.Equal(t, createdJob.ID, job.ID, "Expected job ID to match")
		assert.Equal(t, createdJob.TenantID, job.TenantID, "Expected tenant ID to match")
		assert.Equal(t, domain.JobStatusPending, job.Status, "Expected job status to be PENDING")
	})

	t.Run("should return error for non-existent job", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Try to get a non-existent job
		_, err := repo.GetByID(txCtx, 999999)
		require.Error(t, err, "Expected error for non-existent job")
	})
}

// TestJobRepository_Dequeue tests the Dequeue method
func (s *PostgresSuite) TestJobRepository_Dequeue() {
	t := s.T()
	t.Parallel()

	t.Run("should dequeue pending jobs", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create multiple jobs
		testTenantID := uuid.New().String()
		for i := 0; i < 5; i++ {
			config := domain.CSVImportConfig{
				FilePath: fmt.Sprintf("/path/to/file%d.csv", i),
			}
			_, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
			require.NoError(t, err, "Failed to create job %d", i)
		}

		// Dequeue jobs
		jobs, err := repo.Dequeue(txCtx, 3)
		require.NoError(t, err, "Failed to dequeue jobs")

		assert.Len(t, jobs, 3, "Expected 3 jobs to be dequeued")

		// All dequeued jobs should be RUNNING
		for _, job := range jobs {
			assert.Equal(t, domain.JobStatusRunning, job.Status, "Expected job status RUNNING")
			assert.NotNil(t, job.StartedAt, "Expected started_at to be set")
			assert.Equal(t, 1, job.Attempts, "Expected attempts to be 1")
		}

		// Dequeue again (should get remaining 2 jobs)
		jobs2, err := repo.Dequeue(txCtx, 3)
		require.NoError(t, err, "Failed to dequeue jobs second time")

		assert.Len(t, jobs2, 2, "Expected 2 jobs second time")

		// All dequeued jobs should be RUNNING
		for _, job := range jobs2 {
			assert.Equal(t, domain.JobStatusRunning, job.Status, "Expected job status RUNNING")
		}
	})

	t.Run("should return empty when no pending jobs", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Dequeue when no jobs exist
		jobs, err := repo.Dequeue(txCtx, 10)
		require.NoError(t, err, "Failed to dequeue jobs")

		assert.Empty(t, jobs, "Expected no jobs to be dequeued")
	})

	t.Run("should skip already running jobs", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create and dequeue some jobs
		testTenantID := uuid.New().String()
		for i := 0; i < 3; i++ {
			config := domain.CSVImportConfig{
				FilePath: fmt.Sprintf("/path/to/file%d.csv", i),
			}
			_, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
			require.NoError(t, err, "Failed to create job %d", i)
		}

		// Dequeue all jobs (they become RUNNING)
		jobs1, err := repo.Dequeue(txCtx, 10)
		require.NoError(t, err, "Failed to dequeue jobs")
		assert.Len(t, jobs1, 3, "Expected 3 jobs")

		// Try to dequeue again - should get 0 since all are RUNNING
		jobs2, err := repo.Dequeue(txCtx, 10)
		require.NoError(t, err, "Failed to dequeue jobs second time")
		assert.Empty(t, jobs2, "Expected no jobs since all are RUNNING")
	})
}

// TestJobRepository_UpdateStatus tests the UpdateStatus method
func (s *PostgresSuite) TestJobRepository_UpdateStatus() {
	t := s.T()
	t.Parallel()

	t.Run("should update job status to COMPLETED", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create a job
		testTenantID := uuid.New().String()
		config := domain.CSVImportConfig{FilePath: "/path/to/file.csv"}

		job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
		require.NoError(t, err, "Failed to create job")

		// Update job status to COMPLETED
		result := domain.CSVImportResult{
			RowsProcessed: 10,
			RowsInserted:  10,
			FileHash:      "abc123",
		}
		err = repo.UpdateStatus(txCtx, job.ID, domain.JobStatusCompleted, result, nil, new(int64(100)))
		require.NoError(t, err, "Failed to update job status")

		// Get the job and verify updates
		updatedJob, err := repo.GetByID(txCtx, job.ID)
		require.NoError(t, err, "Failed to get updated job")

		assert.Equal(t, domain.JobStatusCompleted, updatedJob.Status, "Expected job status COMPLETED")
		assert.NotNil(t, updatedJob.DurationMs, "Expected duration_ms to be set")
		assert.Equal(t, int64(100), *updatedJob.DurationMs, "Expected duration_ms to be 100")
		assert.NotNil(t, updatedJob.FinishedAt, "Expected finished_at to be set")

		// Verify result is stored
		var storedResult domain.CSVImportResult
		err = json.Unmarshal(updatedJob.Result, &storedResult)
		require.NoError(t, err, "Failed to unmarshal stored result")

		assert.Equal(t, 10, storedResult.RowsProcessed, "Expected rows processed to be 10")
		assert.Equal(t, 10, storedResult.RowsInserted, "Expected rows inserted to be 10")
		assert.Equal(t, "abc123", storedResult.FileHash, "Expected file hash to match")
	})

	t.Run("should update job status to FAILED", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create a job
		testTenantID := uuid.New().String()
		config := domain.CSVImportConfig{FilePath: "/path/to/file.csv"}

		job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
		require.NoError(t, err, "Failed to create job")

		// Update job status to FAILED with error
		err = repo.UpdateStatus(txCtx, job.ID, domain.JobStatusFailed, nil, new("test error"), nil)
		require.NoError(t, err, "Failed to update job status")

		// Get the job and verify updates
		updatedJob, err := repo.GetByID(txCtx, job.ID)
		require.NoError(t, err, "Failed to get updated job")

		assert.Equal(t, domain.JobStatusFailed, updatedJob.Status, "Expected job status FAILED")
		assert.NotNil(t, updatedJob.Error, "Expected error to be set")
		assert.Equal(t, "test error", *updatedJob.Error, "Expected error message to match")
	})

	t.Run("should update job status to COMPLETED_WITH_ERRORS", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create a job
		testTenantID := uuid.New().String()
		config := domain.CSVImportConfig{FilePath: "/path/to/file.csv"}

		job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
		require.NoError(t, err, "Failed to create job")

		// Update job status to COMPLETED_WITH_ERRORS
		result := domain.CSVImportResult{
			RowsProcessed: 10,
			RowsInserted:  5,
			Errors:        []string{"error1", "error2"},
		}
		err = repo.UpdateStatus(txCtx, job.ID, domain.JobStatusCompletedWithErr, result, nil, nil)
		require.NoError(t, err, "Failed to update job status")

		// Get the job and verify updates
		updatedJob, err := repo.GetByID(txCtx, job.ID)
		require.NoError(t, err, "Failed to get updated job")

		assert.Equal(t, domain.JobStatusCompletedWithErr, updatedJob.Status, "Expected job status COMPLETED_WITH_ERRORS")
	})
}

// TestJobRepository_UpdateToPending tests the UpdateToPending method
func (s *PostgresSuite) TestJobRepository_UpdateToPending() {
	t := s.T()
	t.Parallel()

	t.Run("should reset job to PENDING with backoff", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create a job
		testTenantID := uuid.New().String()
		config := domain.CSVImportConfig{FilePath: "/path/to/file.csv"}

		job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
		require.NoError(t, err, "Failed to create job")

		// Manually set attempts to 2 (simulate previous attempts)
		const updateAttemptsSQL = `UPDATE jobs SET attempts = 2 WHERE id = $1`
		_, err = s.pool.Exec(ctx, updateAttemptsSQL, job.ID)
		require.NoError(t, err, "Failed to update attempts")

		// Requeue the job
		err = repo.UpdateToPending(txCtx, job.ID, new("2025-01-01T00:00:00Z"), new("test error"))
		require.NoError(t, err, "Failed to requeue job")

		// Get the job and verify
		updatedJob, err := repo.GetByID(txCtx, job.ID)
		require.NoError(t, err, "Failed to get updated job")

		assert.Equal(t, domain.JobStatusPending, updatedJob.Status, "Expected job status PENDING")
		assert.NotNil(t, updatedJob.Error, "Expected error to be set")
		assert.Equal(t, "test error", *updatedJob.Error, "Expected error message to match")
	})
}

// TestJobRepository_GetQueueStats tests the GetQueueStats method
func (s *PostgresSuite) TestJobRepository_GetQueueStats() {
	t := s.T()
	t.Parallel()

	t.Run("should return correct queue statistics", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create jobs with different statuses
		testTenantID := uuid.New().String()

		// Create 3 pending jobs
		for i := 0; i < 3; i++ {
			config := domain.CSVImportConfig{FilePath: fmt.Sprintf("/path/to/pending%d.csv", i)}
			_, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
			require.NoError(t, err, "Failed to create pending job")
		}

		// Create 2 completed jobs
		for i := 0; i < 2; i++ {
			config := domain.CSVImportConfig{FilePath: fmt.Sprintf("/path/to/completed%d.csv", i)}
			job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
			require.NoError(t, err, "Failed to create job")

			err = repo.UpdateStatus(txCtx, job.ID, domain.JobStatusCompleted, nil, nil, new(int64(100)))
			require.NoError(t, err, "Failed to update job status")
		}

		// Get queue stats
		stats, err := repo.GetQueueStats(txCtx)
		require.NoError(t, err, "Failed to get queue stats")

		assert.Equal(t, 3, stats.TotalPending, "Expected total pending to be 3")
		assert.Equal(t, 0, stats.TotalRunning, "Expected total running to be 0")

		csvStats, exists := stats.ByType[domain.JobTypeCSVImport]
		require.True(t, exists, "Expected csv_import in stats")

		assert.Equal(t, 3, csvStats.Pending, "Expected csv_import pending to be 3")
		assert.Equal(t, 2, csvStats.Completed, "Expected csv_import completed to be 2")
	})

	t.Run("should handle empty queue", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Get queue stats when no jobs exist
		stats, err := repo.GetQueueStats(txCtx)
		require.NoError(t, err, "Failed to get queue stats")

		assert.Equal(t, 0, stats.TotalPending, "Expected total pending to be 0")
		assert.Equal(t, 0, stats.TotalRunning, "Expected total running to be 0")
		assert.Empty(t, stats.ByType, "Expected by type to be empty")
	})
}

// TestJobRepository_GetJobsByStatus tests the GetJobsByStatus method
func (s *PostgresSuite) TestJobRepository_GetJobsByStatus() {
	t := s.T()
	t.Parallel()

	t.Run("should return jobs filtered by status", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create some jobs
		testTenantID := uuid.New().String()
		for i := 0; i < 5; i++ {
			config := domain.CSVImportConfig{FilePath: fmt.Sprintf("/path/to/file%d.csv", i)}
			job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
			require.NoError(t, err, "Failed to create job")

			// Mark every other job as completed
			if i%2 == 0 {
				err = repo.UpdateStatus(txCtx, job.ID, domain.JobStatusCompleted, nil, nil, new(int64(100)))
				require.NoError(t, err, "Failed to update job status")
			}
		}

		// Get pending jobs
		pendingJobs, err := repo.GetJobsByStatus(txCtx, domain.JobStatusPending, 10)
		require.NoError(t, err, "Failed to get pending jobs")

		assert.Len(t, pendingJobs, 2, "Expected 2 pending jobs")

		// All pending jobs should have PENDING status
		for _, job := range pendingJobs {
			assert.Equal(t, domain.JobStatusPending, job.Status, "Expected job status to be PENDING")
		}

		// Get completed jobs
		completedJobs, err := repo.GetJobsByStatus(txCtx, domain.JobStatusCompleted, 10)
		require.NoError(t, err, "Failed to get completed jobs")

		assert.Len(t, completedJobs, 3, "Expected 3 completed jobs")

		// All completed jobs should have COMPLETED status
		for _, job := range completedJobs {
			assert.Equal(t, domain.JobStatusCompleted, job.Status, "Expected job status to be COMPLETED")
		}
	})

	t.Run("should respect limit parameter", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create 10 jobs
		testTenantID := uuid.New().String()
		for i := 0; i < 10; i++ {
			config := domain.CSVImportConfig{FilePath: fmt.Sprintf("/path/to/file%d.csv", i)}
			_, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
			require.NoError(t, err, "Failed to create job")
		}

		// Get jobs with limit of 5
		jobs, err := repo.GetJobsByStatus(txCtx, domain.JobStatusPending, 5)
		require.NoError(t, err, "Failed to get jobs")

		assert.Len(t, jobs, 5, "Expected 5 jobs (limited)")
	})
}

// TestJobRepository_ResultMerging tests that result JSONB is properly merged
func (s *PostgresSuite) TestJobRepository_ResultMerging() {
	t := s.T()
	t.Parallel()

	t.Run("should merge result JSONB on update", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		txCtx, rollback := s.prepareTx(t, ctx)
		defer rollback()

		repo := s.createJobRepository()

		// Create a job
		testTenantID := uuid.New().String()
		config := domain.CSVImportConfig{FilePath: "/path/to/file.csv"}

		job, err := repo.Create(txCtx, testTenantID, domain.JobTypeCSVImport, config)
		require.NoError(t, err, "Failed to create job")

		// Update with first result
		result1 := domain.CSVImportResult{
			RowsProcessed: 10,
		}
		err = repo.UpdateStatus(txCtx, job.ID, domain.JobStatusRunning, result1, nil, nil)
		require.NoError(t, err, "Failed to update job status")

		// Update with second result (should merge with first)
		result2 := domain.CSVImportResult{
			RowsInserted: 8,
			FileHash:     "hash123",
		}
		err = repo.UpdateStatus(txCtx, job.ID, domain.JobStatusCompleted, result2, nil, new(int64(200)))
		require.NoError(t, err, "Failed to update job status")

		// Get the job and verify merged result
		updatedJob, err := repo.GetByID(txCtx, job.ID)
		require.NoError(t, err, "Failed to get updated job")

		// The result should contain both sets of fields
		var storedResult domain.CSVImportResult
		err = json.Unmarshal(updatedJob.Result, &storedResult)
		require.NoError(t, err, "Failed to unmarshal stored result")

		// Note: JSONB merge at top level means we should have both fields
		assert.Equal(t, 8, storedResult.RowsInserted, "Expected rows inserted to be 8")
		assert.Equal(t, "hash123", storedResult.FileHash, "Expected file hash to be hash123")
	})
}
