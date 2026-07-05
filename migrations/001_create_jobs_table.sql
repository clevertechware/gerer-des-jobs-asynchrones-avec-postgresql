-- Migration: Create jobs table for async job processing
-- Up

CREATE TABLE IF NOT EXISTS jobs (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    type                TEXT NOT NULL,
    operated_by         UUID,
    status              TEXT NOT NULL DEFAULT 'PENDING'
                        CHECK (status IN ('PENDING', 'RUNNING', 'COMPLETED',
                                          'COMPLETED_WITH_ERRORS', 'FAILED')),

    -- Input configuration (immutable after creation)
    config              JSONB NOT NULL DEFAULT '{}'::jsonb,
    
    -- Results and mutable data
    result              JSONB NOT NULL DEFAULT '{}'::jsonb,

    error               TEXT,
    trace_id            TEXT,

    -- Timing
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    duration_ms         BIGINT,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for querying pending jobs (most important for performance)
CREATE INDEX jobs_pending_idx
    ON jobs (created_at)
    WHERE status IN ('PENDING', 'RUNNING');

-- Index for querying by type and tenant
CREATE INDEX jobs_type_tenant_created_idx
    ON jobs (type, tenant_id, created_at DESC);

-- Index for statistics queries
CREATE INDEX jobs_status_idx
    ON jobs (status);

-- Index for finished_at queries (for statistics)
CREATE INDEX jobs_finished_at_idx
    ON jobs (finished_at)
    WHERE finished_at IS NOT NULL;

-- Add run_after column for retry backoff
ALTER TABLE jobs ADD COLUMN run_after TIMESTAMPTZ;

-- Add attempt tracking columns
ALTER TABLE jobs ADD COLUMN attempts INT NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN max_attempts INT NOT NULL DEFAULT 3;

-- Update the default for run_after
ALTER TABLE jobs ALTER COLUMN run_after SET DEFAULT now();

-- Comments
COMMENT ON TABLE jobs IS 'Async job processing table for background tasks like CSV imports, bulk operations, etc.';
COMMENT ON COLUMN jobs.config IS 'Immutable input configuration for the job (file path, delimiter, etc.)';
COMMENT ON COLUMN jobs.result IS 'Mutable results of job execution (rows processed, inserted, errors, etc.)';
COMMENT ON COLUMN jobs.run_after IS 'Timestamp when the job can be retried (for exponential backoff)';
COMMENT ON COLUMN jobs.attempts IS 'Number of times this job has been attempted';
COMMENT ON COLUMN jobs.max_attempts IS 'Maximum number of retry attempts before marking as failed';

-- Create a function to update updated_at timestamp automatically
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Add trigger to update updated_at on any change
DROP TRIGGER IF EXISTS update_jobs_updated_at ON jobs;
CREATE TRIGGER update_jobs_updated_at
    BEFORE UPDATE ON jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
