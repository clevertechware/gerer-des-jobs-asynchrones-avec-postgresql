# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A reference implementation of async job processing on top of PostgreSQL, built for the blog post
"Gérer des jobs asynchrones avec PostgreSQL". CSV import is the one implemented job type, used to
demonstrate the pattern end-to-end (upload → enqueue → dequeue → process → complete/retry).

## Commands

```bash
# Run the app (requires Postgres reachable via env vars, see below)
# Migrations under migrations/*.up.sql run automatically on startup
go run main.go

# Start Postgres + app via Docker Compose
docker-compose up -d

# Start only Postgres for local dev
docker-compose up -d postgres

# Unit tests (no external dependencies)
go test ./internal/usecase/...

# Integration tests (spin up a real Postgres via testcontainers; requires Docker running)
go test ./internal/repository/postgres/...

# Single test
go test ./internal/repository/postgres/... -run TestPostgresSuite/TestCreate

# Everything
go test ./...
```

Config is entirely env-var driven (`internal/config/config.go`), with defaults matching
`docker-compose.yml`. See the README's environment variable table before changing defaults.

## Architecture

Clean/layered architecture, one-way dependency flow: `handler` → `usecase` / `repository interface`
→ `postgres` (impl). `domain` has no dependencies and is imported by everything.

- **`internal/domain`**: `Job`, `JobStatus`, `JobType`, and the CSV-import config/result DTOs that get
  marshaled into the `jobs.config`/`jobs.result` JSONB columns.
- **`internal/repository`**: `JobRepository` interface — the contract `handler/worker` and `handler/web`
  code against, never the concrete `postgres` struct.
- **`internal/repository/postgres`**: the only place that knows SQL.
  - `transaction.go` — `PGTxManager` is the transaction boundary. `Execute(ctx, fn)` begins a tx (or
    reuses one already in `ctx`, so nested calls compose without double-begin), and runs `fn` inside it.
    `GetClient(ctx)` returns the tx-scoped client if present in context, otherwise the pool — this is
    how `JobRepository` methods transparently work both inside and outside a transaction. Any new
    repository method must fetch its client via `txManager.GetClient(ctx)`, not hold its own pool ref.
  - `job_repository.go` — all SQL lives here as inline `const query` strings; `Dequeue` is the core
    mechanic (`UPDATE ... FOR UPDATE SKIP LOCKED` in a subselect, atomically claiming + returning rows).
- **`internal/usecase`**: business logic, decoupled from HTTP/worker plumbing. `CSVImportUsecase.Process`
  takes/returns `json.RawMessage` so it can be registered directly as a `worker.JobHandler` via
  `GetJobHandler()` — this is the shape any new job-type usecase should follow to plug into the worker.
- **`internal/handler/web`**: Gin HTTP handlers; thin, no SQL, delegates to `repository.JobRepository`.
- **`internal/handler/worker`**: `Worker` polls `Dequeue` on an interval, fans batches out to N
  goroutines (`concurrency`), and dispatches by `job.Type` to a registered `JobHandler`. Failure handling
  lives here: `shouldRetry`/`requeueJob` (exponential backoff, capped at 60 min, via `run_after`) vs
  `failJob` (attempts exhausted). `Reaper` runs on its own interval, separately, to un-stick jobs that
  have been `RUNNING` too long — it queries the pool directly rather than through `JobRepository`.
- **`internal/database`**: `RunMigrations(cfg, path)` applies `migrations/*.up.sql` via
  [golang-migrate](https://github.com/golang-migrate/migrate) (pgx/v5 + file source drivers). Treats
  `ErrNoChange` as success and fails loudly on a dirty schema version.
- **`main.go`**: composition root — wires config → migrations → pgxpool → `PGTxManager` →
  `JobRepository` → usecases → web handlers + worker, then runs the HTTP server, worker loop, and
  reaper concurrently, shutting down on SIGINT/SIGTERM.

### Adding a new job type

1. Add config/result types + a `Process(ctx, json.RawMessage) (json.RawMessage, error)` method in a new
   `internal/usecase` package (see `import_csv.go`).
2. Add the `JobType` constant in `internal/domain/job.go`.
3. In `main.go`, call `w.RegisterHandler(domain.JobTypeX, xUsecase.GetJobHandler())`.
4. Add an HTTP handler in `internal/handler/web` if jobs of this type need to be created via API.

### Testing conventions

- Repository tests use `testify/suite` (`PostgresSuite` in `suite_test.go`) against a real Postgres
  started with `testcontainers-go` (`internal/testutil/postgres.go`). The container is started once per
  test binary run (`sync.Once`) and reused across the suite for speed. Migrations are applied via
  `database.RunMigrations` (`internal/database/migrate.go`) — the same golang-migrate path the app uses,
  not a hand-rolled SQL runner.
- Each test gets isolation via `prepareTx`/`testutil.BeginTx`, which opens a transaction, embeds it in
  `ctx` via `ContextWithTx`, and rolls it back in cleanup — never commits, so tests don't need manual
  row cleanup or `TRUNCATE` between runs (though `CleanupJobsTable` exists if a test needs a hard reset).
- `TESTCONTAINERS_RYUK_DISABLED` is set from within the test helper — the container isn't
  auto-reaped by Ryuk, so it can persist across the `sync.Once` guard within a run.
- Integration tests require Docker to be running locally; they will hang/fail otherwise.
