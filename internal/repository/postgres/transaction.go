package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var TxNotFoundErr = errors.New("transaction not found")

// PGClient is the interface for a PostgreSQL client.
type PGClient interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// AdvancedTransactionalPGClient represents an advanced PostgreSQL client extending the PGClient interface.
// It provides additional support for starting transactions with specific options.
type AdvancedTransactionalPGClient interface {
	PGClient
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}
type txKey struct{}

// PGTxManager provides helper methods for managing database transactions using a PostgreSQL client.
type PGTxManager struct {
	logger   *slog.Logger
	pgClient AdvancedTransactionalPGClient
}

// NewPGTxManager creates a new PGTxManager instance with the provided PostgreSQL client.
func NewPGTxManager(logger *slog.Logger, pgClient AdvancedTransactionalPGClient) *PGTxManager {
	return &PGTxManager{logger: logger, pgClient: pgClient}
}

// unitOfWork represents a function that performs a transactional operation using a database transaction.
type unitOfWork func(ctx context.Context) error

// Execute performs a transactional operation using the provided UnitOfWork and manages commit,
// rollback, and error handling.
func (t *PGTxManager) Execute(ctx context.Context, unitOfWork unitOfWork) error {
	if txExists(ctx) {
		return unitOfWork(ctx)
	}

	tx, err := t.pgClient.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func(ctx context.Context, tx pgx.Tx) {
		if p := recover(); p != nil {
			err = tx.Rollback(ctx)
			if err != nil {
				t.logger.ErrorContext(ctx, "failed to rollback transaction", slog.Any("err", err))
			}
			panic(p)
		}
	}(ctx, tx)

	if err = unitOfWork(contextWithTx(ctx, tx)); err != nil {
		if rollBackErr := tx.Rollback(ctx); rollBackErr != nil {
			return fmt.Errorf("failed to rollback transaction: %w", rollBackErr)
		}
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// contextWithTx embeds the given database transaction into the provided context for use in downstream operations.
func contextWithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// GetClient retrieves the current transactional PostgreSQL client from the context
// or returns the default non-transactional one.
func (t *PGTxManager) GetClient(ctx context.Context) PGClient {
	if openedTx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return openedTx
	}
	return t.pgClient
}

// txExists checks if the provided context contains an active pgx.Tx transaction and returns true if it does.
func txExists(ctx context.Context) bool {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return true
	}
	return false
}
