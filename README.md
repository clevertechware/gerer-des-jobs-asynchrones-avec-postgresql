# Gérer des jobs asynchrones avec PostgreSQL

A functional example of asynchronous job processing for CSV files using PostgreSQL as the job queue, GIN for web handlers, and pgx for database access.

## Architecture

This implementation follows the architecture described in the blog post "Gérer des jobs asynchrones avec PostgreSQL" but with a clean, from-scratch Go implementation.

### Key Components

- **Domain Layer** (`internal/domain`): Defines job entities, status types, and DTOs
- **Repository Layer** (`internal/repository`): Interface and PostgreSQL implementation for job persistence
- **Usecase Layer** (`internal/usecase`): Business logic for CSV processing
- **Handler Layer** (`internal/handler`): 
  - `web/`: HTTP handlers using GIN framework
  - `worker/`: Background job worker with concurrent processing
- **Config Layer** (`internal/config`): Configuration management from environment variables

## Project Structure

```
gerer-ses-jobs-asynchrones-avec-postgresql/
├── go.mod                          # Go module definition
├── go.sum                          # Dependency checksums
├── main.go                        # Entry point
├── Dockerfile                     # Docker build configuration
├── docker-compose.yml             # Docker Compose for local development
├── README.md                      # This file
├── internal/
│   ├── domain/
│   │   └── job.go                 # Domain models
│   ├── repository/
│   │   ├── job_repository.go      # Repository interface
│   │   └── postgres/
│   │       ├── job_repository.go  # PostgreSQL implementation
│   │       └── job_repository_test.go # Integration tests
│   ├── usecase/
│   │   ├── import_csv.go          # CSV import business logic
│   │   └── import_csv_test.go     # Unit tests
│   ├── handler/
│   │   ├── web/
│   │   │   └── job_handler.go     # GIN HTTP handlers
│   │   └── worker/
│   │       └── worker.go          # Background job worker
│   ├── config/
│   │   └── config.go              # Configuration
│   └── database/
│       └── migrate.go             # golang-migrate runner
└── migrations/
    ├── 000001_create_jobs_table.up.sql   # Database schema
    └── 000001_create_jobs_table.down.sql # Rollback
```

## Database Schema

The implementation uses the same SQL model as the blog post:

- **`jobs` table** with:
  - `id` BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY
  - `tenant_id` UUID for multi-tenancy
  - `type` TEXT for job type discrimination
  - `status` TEXT with CHECK constraint (PENDING, RUNNING, COMPLETED, COMPLETED_WITH_ERRORS, FAILED)
  - `config` JSONB for immutable input configuration
  - `result` JSONB for mutable processing results
  - `error` TEXT for error messages
  - `trace_id` TEXT for distributed tracing
  - Timing columns: `started_at`, `finished_at`, `duration_ms`, `created_at`, `updated_at`
  - Retry columns: `run_after`, `attempts`, `max_attempts`

- **Indexes**:
  - Partial index on `created_at` WHERE status IN ('PENDING', 'RUNNING') for efficient dequeue
  - Index on `(type, tenant_id, created_at DESC)` for querying by type and tenant
  - Index on `status` for statistics queries

## Key Features

### Job Queue Mechanism

1. **FOR UPDATE SKIP LOCKED**: Ensures concurrent workers don't process the same job twice
2. **Atomic Dequeue**: Single SQL statement to mark jobs as RUNNING and retrieve them
3. **Exponential Backoff**: Jobs that fail are requeued with increasing delays (1, 2, 4, 8 minutes...)
4. **Idempotency**: CSV processing uses file hashes to prevent duplicate imports

### Worker Architecture

- Configurable batch size, poll interval, and concurrency
- Multiple worker goroutines for parallel processing
- Automatic retry with exponential backoff
- Reaper for stuck jobs (jobs running too long)
- Graceful shutdown

### API Endpoints

- `POST /jobs/csv` - Upload CSV file and create import job
- `GET /jobs/:id` - Get job status and results
- `GET /jobs/stats` - Get queue statistics
- `GET /jobs?status=PENDING&limit=10` - List jobs by status
- `GET /health` - Health check

## Getting Started

### Prerequisites

- Go 1.22+
- PostgreSQL 10+
- Docker (optional, for docker-compose)

### Local Development

1. **Start PostgreSQL**:
   ```bash
   docker-compose up -d postgres
   ```

2. **Run the application**:
   ```bash
   go run main.go
   ```
   Database migrations (`migrations/*.up.sql`) are applied automatically on startup via
   [golang-migrate](https://github.com/golang-migrate/migrate) — no manual `psql` step needed.

3. **Or use Docker Compose for everything**:
   ```bash
   docker-compose up -d
   ```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_HOST` | localhost | PostgreSQL host |
| `DB_PORT` | 5432 | PostgreSQL port |
| `DB_USER` | postgres | PostgreSQL user |
| `DB_PASSWORD` | postgres | PostgreSQL password |
| `DB_NAME` | gerer_ses_jobs_asynchrones_avec_postgresql | Database name |
| `DB_SSL_MODE` | disable | SSL mode for PostgreSQL |
| `SERVER_PORT` | 8080 | HTTP server port |
| `UPLOAD_DIR` | ./uploads | Directory for uploaded files |
| `WORKER_BATCH_SIZE` | 5 | Jobs to dequeue per batch |
| `WORKER_POLL_INTERVAL_MS` | 100 | Milliseconds between polls |
| `WORKER_CONCURRENCY` | 3 | Number of concurrent workers |

## Usage

### Upload a CSV File

```bash
curl -X POST \
  -H "X-Tenant-ID: 123e4567-e89b-12d3-a456-426614174000" \
  -F "file=@data.csv" \
  -F "delimiter=," \
  -F "has_header=true" \
  http://localhost:8080/jobs/csv
```

Response:
```json
{
  "job_id": 1,
  "status": "PENDING",
  "message": "CSV import job created with ID 1"
}
```

### Check Job Status

```bash
curl http://localhost:8080/jobs/1
```

Response:
```json
{
  "id": 1,
  "tenant_id": "123e4567-e89b-12d3-a456-426614174000",
  "type": "csv_import",
  "status": "COMPLETED",
  "config": {
    "file_path": "/app/uploads/abc123.csv",
    "delimiter": ",",
    "has_header": true,
    "target_table": ""
  },
  "result": {
    "rows_processed": 100,
    "rows_inserted": 100,
    "rows_skipped": 0,
    "errors": [],
    "file_hash": "abc123...",
    "file_name": "data.csv",
    "start_time": "2024-01-01T12:00:00Z",
    "end_time": "2024-01-01T12:00:01Z"
  },
  "created_at": "2024-01-01T12:00:00Z",
  "updated_at": "2024-01-01T12:00:01Z"
}
```

### Get Queue Statistics

```bash
curl http://localhost:8080/jobs/stats
```

Response:
```json
{
  "total_pending": 2,
  "total_running": 1,
  "by_type": {
    "csv_import": {
      "type": "csv_import",
      "pending": 2,
      "running": 1,
      "completed": 5,
      "failed": 0
    }
  }
}
```

## Testing

### Unit Tests

Run CSV usecase tests:
```bash
go test ./internal/usecase/...
```

### Integration Tests

Run repository integration tests (requires Docker):
```bash
go test ./internal/repository/postgres/...
```

### All Tests

```bash
go test ./...
```

## Implementation Details

### Job Processing Flow

1. **Upload**: Client uploads CSV file via `POST /jobs/csv`
2. **Job Creation**: Web handler saves file and creates PENDING job in database
3. **Dequeue**: Worker polls database using `FOR UPDATE SKIP LOCKED`
4. **Processing**: Worker calls CSV usecase to parse and process file
5. **Finalization**: Results are stored in job's `result` field, status set to COMPLETED or FAILED
6. **Retry**: Failed jobs are requeued with exponential backoff

### Key SQL Queries

**Dequeue**:
```sql
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
RETURNING ...
```

**Idempotency**: File SHA256 hash stored in result to prevent duplicate processing

**Concurrency**: Multiple worker goroutines safely dequeue jobs using PostgreSQL's row locking

## Clean Architecture Principles Applied

- **Separation of Concerns**: Clear layers with well-defined responsibilities
- **Dependency Inversion**: Repositories depend on interfaces, not implementations
- **Testability**: Easy to test each layer in isolation
- **Extensibility**: New job types can be added by implementing a usecase and registering a handler

## License

MIT License
