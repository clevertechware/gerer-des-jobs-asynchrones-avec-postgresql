package testutil

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestUtilSuite tests the testutil package itself
type TestUtilSuite struct {
	suite.Suite
	pool *pgxpool.Pool
}

func TestTestUtil(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(TestUtilSuite))
}

func (s *TestUtilSuite) SetupSuite() {
	t := s.T()

	pool, cleanup := SetupPostgresContainer(t)
	t.Cleanup(cleanup)
	s.pool = pool
}

// TestPostgresContainerSetup verifies that the PostgreSQL test container
// can be started and a connection can be established.
func (s *TestUtilSuite) TestPostgresContainerSetup() {
	t := s.T()

	ctx := t.Context()

	// Verify connection
	err := s.pool.Ping(ctx)
	require.NoError(t, err, "Failed to ping database")

	// Test simple query to verify PostgreSQL is working
	var version string
	err = s.pool.QueryRow(ctx, "SELECT version()").Scan(&version)
	require.NoError(t, err, "Failed to query PostgreSQL version")

	t.Logf("PostgreSQL version: %s", version)
	assert.Contains(t, version, "PostgreSQL", "Expected PostgreSQL version string")
}

// TestMigrationsRan verifies that migrations have been executed
// by checking if the jobs table exists.
func (s *TestUtilSuite) TestMigrationsRan() {
	t := s.T()

	ctx := t.Context()

	// Check if jobs table exists
	var tableExists bool
	query := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables 
			WHERE table_schema = 'public' 
			AND table_name = 'jobs'
		)
	`
	err := s.pool.QueryRow(ctx, query).Scan(&tableExists)
	require.NoError(t, err, "Failed to check if jobs table exists")
	assert.True(t, tableExists, "Jobs table should exist after migrations")
}

// TestBeginTx verifies that transactions can be started
func (s *TestUtilSuite) TestBeginTx() {
	t := s.T()

	// This will automatically rollback via t.Cleanup
	tx := BeginTx(t)
	assert.NotNil(t, tx, "Expected transaction to be created")
}
