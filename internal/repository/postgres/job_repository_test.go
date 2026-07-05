package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"csv-job-processor/internal/domain"
)

var (
	testDB  *pgxpool.Pool
	testCtx = context.Background()
)

func setupTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	ctx := testCtx

	// Disable ryuk to prevent premature container cleanup
	// This must be set before any testcontainers operations
	_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	// Request a container
	req := testcontainers.ContainerRequest{
		Image:        "postgres:15-alpine",
		ExposedPorts: []string{"5432/tcp"},
		WaitingFor:   wait.ForLog("database system is ready to accept connections").WithStartupTimeout(120 * time.Second),
		Env: map[string]string{
			"POSTGRES_USER":     "testuser",
			"POSTGRES_PASSWORD": "testpass",
			"POSTGRES_DB":       "testdb",
		},
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})

	// Clean up container
	cleanup := func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}

	// Get the mapped port
	mappedPort, err := container.MappedPort(ctx, "5432")
	if err != nil {
		cleanup()
		t.Fatalf("Failed to get mapped port: %v", err)
	}

	// On macOS with Docker Desktop, use host.docker.internal
	// On Linux, use localhost
	host := "localhost"
	// Check if we're on macOS by trying to resolve host.docker.internal
	if _, err := net.LookupHost("host.docker.internal"); err == nil {
		host = "host.docker.internal"
	}

	// Create connection string
	connString := fmt.Sprintf("host=%s port=%s user=testuser password=testpass dbname=testdb sslmode=disable",
		host, mappedPort.Port())

	// Create connection pool
	dbConfig, err := pgxpool.ParseConfig(connString)
	if err != nil {
		cleanup()
		t.Fatalf("Failed to parse config: %v", err)
	}

	db, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		cleanup()
		t.Fatalf("Failed to create connection pool: %v", err)
	}

	// Wait a bit after container is ready to ensure ryuk doesn't kill it
	time.Sleep(2 * time.Second)

	// Test connection
	if err := db.Ping(ctx); err != nil {
		cleanup()
		t.Fatalf("Failed to ping database: %v", err)
	}

	// Create the jobs table
	if err := createTestTable(ctx, db); err != nil {
		cleanup()
		t.Fatalf("Failed to create test table: %v", err)
	}

	return db, cleanup
}

func createTestTable(ctx context.Context, db *pgxpool.Pool) error {
	const createTableSQL = `
		CREATE TABLE IF NOT EXISTS jobs (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			tenant_id UUID NOT NULL,
			type TEXT NOT NULL,
			operated_by UUID,
			status TEXT NOT NULL DEFAULT 'PENDING'
				CHECK (status IN ('PENDING', 'RUNNING', 'COMPLETED', 'COMPLETED_WITH_ERRORS', 'FAILED')),
			config JSONB NOT NULL DEFAULT '{}'::jsonb,
			result JSONB NOT NULL DEFAULT '{}'::jsonb,
			error TEXT,
			trace_id TEXT,
			started_at TIMESTAMPTZ,
			finished_at TIMESTAMPTZ,
			duration_ms BIGINT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			run_after TIMESTAMPTZ DEFAULT now(),
			attempts INT NOT NULL DEFAULT 0,
			max_attempts INT NOT NULL DEFAULT 3
		);
		
		CREATE INDEX IF NOT EXISTS jobs_pending_idx ON jobs (created_at) WHERE status IN ('PENDING', 'RUNNING');
		CREATE INDEX IF NOT EXISTS jobs_type_tenant_created_idx ON jobs (type, tenant_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs (status);
	`

	_, err := db.Exec(ctx, createTableSQL)
	return err
}

func TestJobRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewJobRepository(db)

	testTenantID := uuid.New().String()
	config := domain.CSVImportConfig{
		FilePath:    "/path/to/file.csv",
		Delimiter:   ",",
		HasHeader:   true,
		TargetTable: "users",
	}

	job, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	if job.ID == 0 {
		t.Error("Expected job ID to be set")
	}

	if job.TenantID.String() == "" {
		t.Error("Expected tenant ID to be set")
	}

	if job.Type != domain.JobTypeCSVImport {
		t.Errorf("Expected job type %s, got %s", domain.JobTypeCSVImport, job.Type)
	}

	if job.Status != domain.JobStatusPending {
		t.Errorf("Expected job status PENDING, got %s", job.Status)
	}

	// Verify config is stored
	var storedConfig domain.CSVImportConfig
	if err := json.Unmarshal(job.Config, &storedConfig); err != nil {
		t.Fatalf("Failed to unmarshal stored config: %v", err)
	}

	if storedConfig.FilePath != config.FilePath {
		t.Errorf("Expected file path %s, got %s", config.FilePath, storedConfig.FilePath)
	}
}

func TestJobRepository_GetByID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewJobRepository(db)

	// Create a job first
	testTenantID := uuid.New().String()
	config := domain.CSVImportConfig{
		FilePath: "/path/to/file.csv",
	}

	createdJob, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	// Get the job by ID
	job, err := repo.GetByID(testCtx, createdJob.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if job.ID != createdJob.ID {
		t.Errorf("Expected job ID %d, got %d", createdJob.ID, job.ID)
	}

	if job.Status != domain.JobStatusPending {
		t.Errorf("Expected job status PENDING, got %s", job.Status)
	}

	// Test getting non-existent job
	_, err = repo.GetByID(testCtx, 999999)
	if err == nil {
		t.Error("Expected error for non-existent job")
	}
}

func TestJobRepository_Dequeue(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewJobRepository(db)

	// Create multiple jobs
	testTenantID := uuid.New().String()
	for i := 0; i < 5; i++ {
		config := domain.CSVImportConfig{
			FilePath: fmt.Sprintf("/path/to/file%d.csv", i),
		}
		_, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
		if err != nil {
			t.Fatalf("Failed to create job %d: %v", i, err)
		}
	}

	// Dequeue jobs
	jobs, err := repo.Dequeue(testCtx, 3)
	if err != nil {
		t.Fatalf("Failed to dequeue jobs: %v", err)
	}

	if len(jobs) != 3 {
		t.Errorf("Expected 3 jobs, got %d", len(jobs))
	}

	// All dequeued jobs should be RUNNING
	for _, job := range jobs {
		if job.Status != domain.JobStatusRunning {
			t.Errorf("Expected job status RUNNING, got %s", job.Status)
		}
		if job.StartedAt == nil {
			t.Error("Expected started_at to be set")
		}
		if job.Attempts != 1 {
			t.Errorf("Expected attempts to be 1, got %d", job.Attempts)
		}
	}

	// Dequeue again (should get remaining 2 jobs)
	jobs2, err := repo.Dequeue(testCtx, 3)
	if err != nil {
		t.Fatalf("Failed to dequeue jobs second time: %v", err)
	}

	if len(jobs2) != 2 {
		t.Errorf("Expected 2 jobs second time, got %d", len(jobs2))
	}

	// Dequeue again (should get 0 jobs)
	jobs3, err := repo.Dequeue(testCtx, 3)
	if err != nil {
		t.Fatalf("Failed to dequeue jobs third time: %v", err)
	}

	if len(jobs3) != 0 {
		t.Errorf("Expected 0 jobs third time, got %d", len(jobs3))
	}
}

func TestJobRepository_UpdateStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewJobRepository(db)

	// Create a job
	testTenantID := uuid.New().String()
	config := domain.CSVImportConfig{FilePath: "/path/to/file.csv"}

	job, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	// Update job status to COMPLETED
	result := domain.CSVImportResult{
		RowsProcessed: 10,
		RowsInserted:  10,
		FileHash:      "abc123",
	}
	durationMs := int64(100)

	err = repo.UpdateStatus(testCtx, job.ID, domain.JobStatusCompleted, result, nil, &durationMs)
	if err != nil {
		t.Fatalf("Failed to update job status: %v", err)
	}

	// Get the job and verify updates
	updatedJob, err := repo.GetByID(testCtx, job.ID)
	if err != nil {
		t.Fatalf("Failed to get updated job: %v", err)
	}

	if updatedJob.Status != domain.JobStatusCompleted {
		t.Errorf("Expected job status COMPLETED, got %s", updatedJob.Status)
	}

	if updatedJob.DurationMs == nil || *updatedJob.DurationMs != 100 {
		t.Errorf("Expected duration_ms 100, got %v", updatedJob.DurationMs)
	}

	if updatedJob.FinishedAt == nil {
		t.Error("Expected finished_at to be set")
	}

	// Verify result is stored
	var storedResult domain.CSVImportResult
	if err := json.Unmarshal(updatedJob.Result, &storedResult); err != nil {
		t.Fatalf("Failed to unmarshal stored result: %v", err)
	}

	if storedResult.RowsProcessed != 10 {
		t.Errorf("Expected rows processed 10, got %d", storedResult.RowsProcessed)
	}
}

func TestJobRepository_ConcurrentDequeue(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewJobRepository(db)

	// Create 10 jobs
	testTenantID := uuid.New().String()
	for i := 0; i < 10; i++ {
		config := domain.CSVImportConfig{
			FilePath: fmt.Sprintf("/path/to/file%d.csv", i),
		}
		_, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
		if err != nil {
			t.Fatalf("Failed to create job %d: %v", i, err)
		}
	}

	// Run multiple dequeues concurrently
	const numWorkers = 5
	const batchSize = 3

	allJobs := make(chan []*domain.Job, numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func() {
			jobs, err := repo.Dequeue(testCtx, batchSize)
			if err != nil {
				t.Errorf("Worker error: %v", err)
				return
			}
			allJobs <- jobs
		}()
	}

	// Collect all dequeued jobs
	var dequeuedJobIDs []int64
	for i := 0; i < numWorkers; i++ {
		jobs := <-allJobs
		for _, job := range jobs {
			dequeuedJobIDs = append(dequeuedJobIDs, job.ID)
		}
	}

	// Verify no duplicates
	seen := make(map[int64]bool)
	for _, id := range dequeuedJobIDs {
		if seen[id] {
			t.Errorf("Duplicate job ID found: %d", id)
		}
		seen[id] = true
	}

	// All dequeued jobs should be RUNNING
	for _, id := range dequeuedJobIDs {
		job, err := repo.GetByID(testCtx, id)
		if err != nil {
			t.Fatalf("Failed to get job %d: %v", id, err)
		}
		if job.Status != domain.JobStatusRunning {
			t.Errorf("Expected job %d status RUNNING, got %s", id, job.Status)
		}
	}
}

func TestJobRepository_GetQueueStats(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewJobRepository(db)

	// Create jobs with different statuses
	testTenantID := uuid.New().String()
	for i := 0; i < 3; i++ {
		config := domain.CSVImportConfig{FilePath: fmt.Sprintf("/path/to/pending%d.csv", i)}
		_, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
		if err != nil {
			t.Fatalf("Failed to create pending job: %v", err)
		}
	}

	// Create some jobs and mark them as completed
	for i := 0; i < 2; i++ {
		config := domain.CSVImportConfig{FilePath: fmt.Sprintf("/path/to/completed%d.csv", i)}
		job, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
		if err != nil {
			t.Fatalf("Failed to create job: %v", err)
		}

		// Mark as completed
		durationMs := int64(100)
		err = repo.UpdateStatus(testCtx, job.ID, domain.JobStatusCompleted, nil, nil, &durationMs)
		if err != nil {
			t.Fatalf("Failed to update job status: %v", err)
		}
	}

	// Get queue stats
	stats, err := repo.GetQueueStats(testCtx)
	if err != nil {
		t.Fatalf("Failed to get queue stats: %v", err)
	}

	if stats.TotalPending != 3 {
		t.Errorf("Expected total pending 3, got %d", stats.TotalPending)
	}

	if stats.TotalRunning != 0 {
		t.Errorf("Expected total running 0, got %d", stats.TotalRunning)
	}

	if len(stats.ByType) != 1 {
		t.Errorf("Expected 1 job type, got %d", len(stats.ByType))
	}

	csvStats, exists := stats.ByType[domain.JobTypeCSVImport]
	if !exists {
		t.Fatal("Expected csv_import in stats")
	}

	if csvStats.Pending != 3 {
		t.Errorf("Expected csv_import pending 3, got %d", csvStats.Pending)
	}

	if csvStats.Completed != 2 {
		t.Errorf("Expected csv_import completed 2, got %d", csvStats.Completed)
	}
}

func TestJobRepository_GetJobsByStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewJobRepository(db)

	// Create some jobs
	testTenantID := uuid.New().String()
	for i := 0; i < 5; i++ {
		config := domain.CSVImportConfig{FilePath: fmt.Sprintf("/path/to/file%d.csv", i)}
		job, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
		if err != nil {
			t.Fatalf("Failed to create job: %v", err)
		}

		// Mark every other job as completed
		if i%2 == 0 {
			durationMs := int64(100)
			err = repo.UpdateStatus(testCtx, job.ID, domain.JobStatusCompleted, nil, nil, &durationMs)
			if err != nil {
				t.Fatalf("Failed to update job status: %v", err)
			}
		}
	}

	// Get pending jobs
	pendingJobs, err := repo.GetJobsByStatus(testCtx, domain.JobStatusPending, 10)
	if err != nil {
		t.Fatalf("Failed to get pending jobs: %v", err)
	}

	if len(pendingJobs) != 2 {
		t.Errorf("Expected 2 pending jobs, got %d", len(pendingJobs))
	}

	// Get completed jobs
	completedJobs, err := repo.GetJobsByStatus(testCtx, domain.JobStatusCompleted, 10)
	if err != nil {
		t.Fatalf("Failed to get completed jobs: %v", err)
	}

	if len(completedJobs) != 3 {
		t.Errorf("Expected 3 completed jobs, got %d", len(completedJobs))
	}
}

func TestJobRepository_RetryWithBackoff(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewJobRepository(db)

	// Create a job
	testTenantID := uuid.New().String()
	config := domain.CSVImportConfig{FilePath: "/path/to/file.csv"}

	job, err := repo.Create(testCtx, testTenantID, domain.JobTypeCSVImport, config)
	if err != nil {
		t.Fatalf("Failed to create job: %v", err)
	}

	// Manually set attempts to 2 (simulate previous attempts)
	// We need to do this via direct SQL since UpdateToPending will increment attempts
	const updateAttemptsSQL = `UPDATE jobs SET attempts = 2 WHERE id = $1`
	_, err = db.Exec(testCtx, updateAttemptsSQL, job.ID)
	if err != nil {
		t.Fatalf("Failed to update attempts: %v", err)
	}

	// Requeue the job
	runAfter := "2025-01-01T00:00:00Z"
	errMsg := "test error"

	err = repo.UpdateToPending(testCtx, job.ID, &runAfter, &errMsg)
	if err != nil {
		t.Fatalf("Failed to requeue job: %v", err)
	}

	// Get the job and verify
	updatedJob, err := repo.GetByID(testCtx, job.ID)
	if err != nil {
		t.Fatalf("Failed to get updated job: %v", err)
	}

	if updatedJob.Status != domain.JobStatusPending {
		t.Errorf("Expected job status PENDING, got %s", updatedJob.Status)
	}

	if updatedJob.Error == nil || *updatedJob.Error != "test error" {
		t.Errorf("Expected error message 'test error', got %v", updatedJob.Error)
	}
}
