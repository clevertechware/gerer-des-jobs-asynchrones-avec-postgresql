package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// JobStatus represents the current state of a job
type JobStatus string

const (
	JobStatusPending          JobStatus = "PENDING"
	JobStatusRunning          JobStatus = "RUNNING"
	JobStatusCompleted        JobStatus = "COMPLETED"
	JobStatusCompletedWithErr JobStatus = "COMPLETED_WITH_ERRORS"
	JobStatusFailed           JobStatus = "FAILED"
)

// JobType represents the type of job to execute
type JobType string

const (
	JobTypeCSVImport JobType = "csv_import"
)

// Job represents a background job in the system
type Job struct {
	ID          int64           `json:"id"`
	TenantID    uuid.UUID       `json:"tenant_id"`
	Type        JobType         `json:"type"`
	Status      JobStatus       `json:"status"`
	OperatedBy  *uuid.UUID      `json:"operated_by,omitempty"`
	Config      json.RawMessage `json:"config"`
	Result      json.RawMessage `json:"result"`
	Error       *string         `json:"error,omitempty"`
	TraceID     *string         `json:"trace_id,omitempty"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	DurationMs  *int64          `json:"duration_ms,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	RunAfter    *time.Time      `json:"run_after,omitempty"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
}

// CSVImportConfig contains configuration for CSV import jobs
type CSVImportConfig struct {
	FilePath    string `json:"file_path"`
	Delimiter   string `json:"delimiter"`
	HasHeader   bool   `json:"has_header"`
	TargetTable string `json:"target_table,omitempty"`
}

// CSVImportResult contains the result of a CSV import job
type CSVImportResult struct {
	RowsProcessed int      `json:"rows_processed"`
	RowsInserted  int      `json:"rows_inserted"`
	RowsSkipped   int      `json:"rows_skipped"`
	Errors        []string `json:"errors,omitempty"`
	FileHash      string   `json:"file_hash"`
}

// JobStats contains statistics for a job type
type JobStats struct {
	Type      JobType `json:"type"`
	Pending   int     `json:"pending"`
	Running   int     `json:"running"`
	Completed int     `json:"completed"`
	Failed    int     `json:"failed"`
}

// QueueStats contains overall queue statistics
type QueueStats struct {
	TotalPending int                  `json:"total_pending"`
	TotalRunning int                  `json:"total_running"`
	ByType       map[JobType]JobStats `json:"by_type"`
}
