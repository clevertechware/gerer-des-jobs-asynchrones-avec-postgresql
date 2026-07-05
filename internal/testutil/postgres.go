package testutil

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgresModule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/config"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/database"
)

// testSQLLogger adapts stdlib log.Logger to tracelog.Logger interface
type testSQLLogger struct {
	logger *log.Logger
}

func (l *testSQLLogger) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	// Format the log level prefix
	levelStr := ""
	switch level {
	case tracelog.LogLevelTrace:
		levelStr = "[TRACE] "
	case tracelog.LogLevelDebug:
		levelStr = "[DEBUG] "
	case tracelog.LogLevelInfo:
		levelStr = "[INFO] "
	case tracelog.LogLevelWarn:
		levelStr = "[WARN] "
	case tracelog.LogLevelError:
		levelStr = "[ERROR] "
	}

	// Format data if present
	dataStr := ""
	if len(data) > 0 {
		dataStr = fmt.Sprintf(" %+v", data)
	}

	l.logger.Printf("%s%s%s", levelStr, msg, dataStr)
}

// PostgreSQL container instance and connection pool
var (
	pgContainer *postgresModule.PostgresContainer
	pgPool      *pgxpool.Pool
	setupOnce   sync.Once
	setupErr    error
	poolMutex   sync.Mutex
)

// SetupPostgresContainer initializes a PostgreSQL test container and returns a connection pool.
// It uses sync.Once to ensure only one container is created per test run.
// Call this in individual tests and defer the cleanup function.
func SetupPostgresContainer(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	// Disable ryuk to prevent premature container cleanup
	// Set this early, before any testcontainers operations
	_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	setupOnce.Do(func() {
		ctx := t.Context()

		// Create PostgreSQL container using the postgres module
		container, err := postgresModule.Run(ctx,
			"postgres:15-alpine",
			postgresModule.WithDatabase("gerer_ses_jobs_asynchrones_avec_postgresql_test"),
			postgresModule.WithUsername("testuser"),
			postgresModule.WithPassword("testpass"),
		)
		if err != nil {
			setupErr = fmt.Errorf("failed to start PostgreSQL container: %w", err)
			return
		}

		pgContainer = container

		// Wait a bit to ensure container is fully ready (ryuk might have been disabled but still)
		time.Sleep(3 * time.Second)

		// Get connection string from container (handles host/port automatically)
		connStr, err := container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			setupErr = fmt.Errorf("failed to get connection string: %w", err)
			return
		}

		// Parse and configure connection pool for testing
		config, err := pgxpool.ParseConfig(connStr)
		if err != nil {
			setupErr = fmt.Errorf("failed to parse connection string: %w", err)
			return
		}

		// Enable query logging for debugging using tracelog
		// Create a logger that adapts stdlib log.Logger to tracelog.Logger
		testLogger := &testSQLLogger{
			logger: log.New(os.Stdout, "[TEST-SQL] ", log.LstdFlags),
		}
		config.ConnConfig.Tracer = &tracelog.TraceLog{
			Logger:   testLogger,
			LogLevel: tracelog.LogLevelDebug,
			Config:   tracelog.DefaultTraceLogConfig(),
		}

		// Configure pool with smaller values for testing
		config.MaxConns = 5
		config.MinConns = 1
		config.MaxConnLifetime = 5 * time.Minute
		config.MaxConnIdleTime = 1 * time.Minute
		config.HealthCheckPeriod = 10 * time.Second

		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			setupErr = fmt.Errorf("failed to create connection pool: %w", err)
			return
		}

		// Verify connection with retries
		var pingErr error
		for i := 0; i < 5; i++ {
			pingErr = pool.Ping(ctx)
			if pingErr == nil {
				break
			}
			time.Sleep(1 * time.Second)
		}
		if pingErr != nil {
			setupErr = fmt.Errorf("failed to ping database after 5 retries: %w", pingErr)
			return
		}

		// Run migrations against test database
		if err = runMigrations(ctx, pool); err != nil {
			setupErr = fmt.Errorf("failed to run migrations: %w", err)
			return
		}

		// Set the global pool atomically
		poolMutex.Lock()
		pgPool = pool
		poolMutex.Unlock()
	})

	if setupErr != nil {
		t.Fatal(setupErr)
	}

	return pgPool, func() {
		// Note: We don't close pgPool here because it's a shared global resource.
		// The pool will be cleaned up when the container is terminated.
		// Individual tests should not close the shared pool.
		err := CleanupPostgresContainer(t)
		require.NoError(t, err)
	}
}

// CleanupPostgresContainer terminates the PostgreSQL container.
// This should be called at the end of the test run, typically via t.Cleanup.
// It returns any errors encountered during cleanup, joined together.
func CleanupPostgresContainer(t *testing.T) error {
	t.Helper()

	poolMutex.Lock()
	defer poolMutex.Unlock()

	var cleanupErrs []error

	if pgContainer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := pgContainer.Terminate(ctx); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("terminating container: %w", err))
		}
	}

	return errors.Join(cleanupErrs...)
}

// BeginTx starts a transaction on the test database pool.
// Use this for individual tests that need transaction isolation.
// The transaction is automatically rolled back when the test completes (via t.Cleanup).
// Returns pgx.Tx which can be used with query methods.
func BeginTx(t *testing.T) pgx.Tx {
	t.Helper()

	poolMutex.Lock()
	defer poolMutex.Unlock()

	if pgPool == nil {
		t.Fatal("PostgreSQL pool not initialized, call SetupPostgresContainer first")
	}

	ctx := context.Background()
	tx, err := pgPool.Begin(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Register rollback on cleanup
	t.Cleanup(func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("Warning: transaction rollback failed: %v", err)
		}
	})

	return tx
}

// runMigrations applies all migrations from the migrations directory to the test database
// via database.RunMigrations (golang-migrate), the same path used by the application.
func runMigrations(_ context.Context, pool *pgxpool.Pool) error {
	migrationsDir, err := findMigrationsDir()
	if err != nil {
		return err
	}

	connCfg := pool.Config().ConnConfig
	cfg := &config.Config{
		DBHost:     connCfg.Host,
		DBPort:     int(connCfg.Port),
		DBUser:     connCfg.User,
		DBPassword: connCfg.Password,
		DBName:     connCfg.Database,
		DBSSLMode:  "disable",
	}

	return database.RunMigrations(cfg, migrationsDir)
}

// findMigrationsDir locates the migrations directory starting from the current working
// directory and moving up to 3 parent directories.
func findMigrationsDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	for i := 0; i <= 3; i++ {
		migrationsDir := filepath.Join(wd, "migrations")
		if entries, err := os.ReadDir(migrationsDir); err == nil && len(entries) > 0 {
			return migrationsDir, nil
		}
		wd = filepath.Dir(wd)
	}

	return "", fmt.Errorf("no migrations directory found")
}

// CleanupJobsTable truncates the jobs table.
// Use this between tests to ensure a clean state.
func CleanupJobsTable(t *testing.T) error {
	t.Helper()

	poolMutex.Lock()
	pool := pgPool
	poolMutex.Unlock()

	if pool == nil {
		return fmt.Errorf("PostgreSQL pool not initialized, call SetupPostgresContainer first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, "TRUNCATE TABLE jobs RESTART IDENTITY CASCADE")
	return err
}

// EnsureTestcontainers ensures Testcontainers is properly set up.
// Returns an error if Docker is not available.
func EnsureTestcontainers() error {
	ctx := context.Background()
	_, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:      "alpine:latest",
				Cmd:        []string{"true"},
				WaitingFor: wait.ForExit().WithPollInterval(1 * time.Second),
			},
			Started: true,
		},
	)
	return err
}
