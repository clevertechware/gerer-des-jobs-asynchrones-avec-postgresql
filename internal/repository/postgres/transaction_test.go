package postgres

import (
	"context"
	"fmt"
	"testing"

	mockPgx "github.com/clevertechware/rezotons/mocks/github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5"

	"github.com/clevertechware/rezotons/internal/logger"
	"github.com/clevertechware/rezotons/internal/postgres/mocks"
	"github.com/clevertechware/rezotons/pkg/transaction"
	"github.com/stretchr/testify/assert"
)

func TestPGTxManager_Execute(t *testing.T) {
	t.Parallel()

	type fields struct {
		client func(t *testing.T) PGClient
	}
	type args struct {
		ctx        context.Context
		unitOfWork transaction.UnitOfWork
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "should commit on success without err",
			fields: fields{
				client: func(t *testing.T) PGClient {
					mock := mocks.NewClient(t)
					mockTx := mockPgx.NewTx(t)
					ctx := t.Context()
					mockTx.EXPECT().Commit(ctx).Return(nil)
					mock.EXPECT().Begin(ctx).Return(mockTx, nil)
					return mock
				},
			},
			args: args{
				ctx:        t.Context(),
				unitOfWork: func(_ context.Context) error { return nil },
			},
			wantErr: assert.NoError,
		},
		{
			name: "should rollback on error",
			fields: fields{
				client: func(t *testing.T) PGClient {
					mock := mocks.NewClient(t)
					mockTx := mockPgx.NewTx(t)
					ctx := t.Context()
					mockTx.EXPECT().Rollback(ctx).Return(nil)
					mock.EXPECT().Begin(ctx).Return(mockTx, nil)
					return mock
				},
			},
			args: args{
				ctx:        t.Context(),
				unitOfWork: func(_ context.Context) error { return assert.AnError },
			},
			wantErr: assert.Error,
		},
	}
	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			noOpLogger := logger.NewNoOpLogger()
			pgTxManager := &PGTxManager{logger: noOpLogger, client: tt.fields.client(t)}

			err := pgTxManager.Execute(tt.args.ctx, tt.args.unitOfWork)

			tt.wantErr(t, err, fmt.Sprintf("Execute(%v, %v)", tt.args.ctx, tt.args.unitOfWork))
		})
	}
}

func TestPGTxManager_ExecuteReadOnly(t *testing.T) {
	t.Parallel()

	type fields struct {
		client func(t *testing.T) PGClient
	}
	type args struct {
		ctx        context.Context
		unitOfWork transaction.UnitOfWork
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			name: "should commit read-only transaction on success",
			fields: fields{
				client: func(t *testing.T) PGClient {
					mock := mocks.NewClient(t)
					mockTx := mockPgx.NewTx(t)
					ctx := t.Context()
					expectedOpts := pgx.TxOptions{
						IsoLevel:   pgx.ReadCommitted,
						AccessMode: pgx.ReadOnly,
					}
					mockTx.EXPECT().Commit(ctx).Return(nil)
					mock.EXPECT().BeginTx(ctx, expectedOpts).Return(mockTx, nil)
					return mock
				},
			},
			args: args{
				ctx:        t.Context(),
				unitOfWork: func(_ context.Context) error { return nil },
			},
			wantErr: assert.NoError,
		},
		{
			name: "should rollback on unit of work error",
			fields: fields{
				client: func(t *testing.T) PGClient {
					mock := mocks.NewClient(t)
					mockTx := mockPgx.NewTx(t)
					ctx := t.Context()
					expectedOpts := pgx.TxOptions{
						IsoLevel:   pgx.ReadCommitted,
						AccessMode: pgx.ReadOnly,
					}
					mockTx.EXPECT().Rollback(ctx).Return(nil)
					mock.EXPECT().BeginTx(ctx, expectedOpts).Return(mockTx, nil)
					return mock
				},
			},
			args: args{
				ctx:        t.Context(),
				unitOfWork: func(_ context.Context) error { return assert.AnError },
			},
			wantErr: assert.Error,
		},
		{
			name: "should execute unit of work directly when tx already exists in context",
			fields: fields{
				client: func(t *testing.T) PGClient {
					mock := mocks.NewClient(t)
					// No BeginTx call expected — tx already exists
					return mock
				},
			},
			args: args{
				ctx: func() context.Context {
					mock := mockPgx.NewTx(t)
					return context.WithValue(t.Context(), txKey{}, mock)
				}(),
				unitOfWork: func(_ context.Context) error { return nil },
			},
			wantErr: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			noOpLogger := logger.NewNoOpLogger()
			pgTxManager := &PGTxManager{logger: noOpLogger, client: tt.fields.client(t)}

			err := pgTxManager.ExecuteReadOnly(tt.args.ctx, tt.args.unitOfWork)

			tt.wantErr(t, err, fmt.Sprintf("ExecuteReadOnly(%v, %v)", tt.args.ctx, tt.args.unitOfWork))
		})
	}
}
