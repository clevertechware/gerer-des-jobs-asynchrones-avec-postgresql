package postgres

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"csv-job-processor/internal/testutil"
)

// PostgresSuite is the test suite for PostgreSQL repository tests
type PostgresSuite struct {
	suite.Suite
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// TestPostgresSuite runs all tests in the PostgresSuite
func TestPostgresSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(PostgresSuite))
}

// SetupSuite sets up the test suite with a PostgreSQL container
func (s *PostgresSuite) SetupSuite() {
	t := s.T()
	pgpool, cleanup := testutil.SetupPostgresContainer(t)
	s.pool = pgpool
	s.logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	t.Cleanup(cleanup)
}

// prepareTx creates a transaction and returns a context with the transaction embedded.
// The transaction is automatically rolled back when the cleanup function is called.
func (s *PostgresSuite) prepareTx(t *testing.T, ctx context.Context) (context.Context, func()) {
	tx, err := s.pool.Begin(ctx)
	require.NoError(t, err, "Failed to begin transaction")
	txCtx := ContextWithTx(ctx, tx)
	return txCtx, func() {
		err := tx.Rollback(ctx)
		require.NoError(t, err, "Failed to rollback transaction")
	}
}

// createTxManager creates a PGTxManager for use in tests
func (s *PostgresSuite) createTxManager() *PGTxManager {
	return NewPGTxManager(s.logger, s.pool)
}

// createJobRepository creates a JobRepository for use in tests
func (s *PostgresSuite) createJobRepository() *JobRepository {
	return &JobRepository{txManager: s.createTxManager()}
}
