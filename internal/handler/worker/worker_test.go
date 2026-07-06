package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/domain"
)

// mockJobRepository is a testify mock implementing JobRepository.
type mockJobRepository struct {
	mock.Mock
}

var _ JobRepository = (*mockJobRepository)(nil)

func newMockJobRepository(t *testing.T) *mockJobRepository {
	m := &mockJobRepository{}
	m.Test(t)
	return m
}

func (m *mockJobRepository) Dequeue(ctx context.Context, batchSize int) ([]*domain.Job, error) {
	args := m.Called(ctx, batchSize)
	jobs, _ := args.Get(0).([]*domain.Job)
	return jobs, args.Error(1)
}

func (m *mockJobRepository) UpdateStatus(ctx context.Context, id int64, status domain.JobStatus, result interface{}, errMsg *string, durationMs *int64) error {
	args := m.Called(ctx, id, status, result, errMsg, durationMs)
	return args.Error(0)
}

func (m *mockJobRepository) UpdateToPending(ctx context.Context, id int64, runAfter *string, errorMsg *string) error {
	args := m.Called(ctx, id, runAfter, errorMsg)
	return args.Error(0)
}

// emptyStringPtr matches a non-nil *string pointing at "".
func emptyStringPtr(s *string) bool {
	return s != nil && *s == ""
}

const testJobType domain.JobType = "test_type"

func TestProcessJob(t *testing.T) {
	t.Run("unknown job type fails the job", func(t *testing.T) {
		repo := newMockJobRepository(t)
		w := NewWorker(nil, repo)

		job := &domain.Job{ID: 1, Type: "unregistered_type"}
		repo.On("UpdateStatus", mock.Anything, int64(1), domain.JobStatusFailed, nil,
			mock.MatchedBy(func(s *string) bool { return s != nil && *s == "unknown job type: unregistered_type" }),
			mock.Anything).Return(nil)

		w.processJob(context.Background(), job)

		repo.AssertExpectations(t)
	})

	t.Run("handler success completes the job", func(t *testing.T) {
		repo := newMockJobRepository(t)
		w := NewWorker(nil, repo)
		w.RegisterHandler(testJobType, func(ctx context.Context, config json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"ok":true}`), nil
		})

		job := &domain.Job{ID: 2, Type: testJobType}
		repo.On("UpdateStatus", mock.Anything, int64(2), domain.JobStatusCompleted, json.RawMessage(`{"ok":true}`),
			mock.MatchedBy(emptyStringPtr), mock.Anything).Return(nil)

		w.processJob(context.Background(), job)

		repo.AssertExpectations(t)
	})

	t.Run("handler error with retries left requeues instead of failing", func(t *testing.T) {
		repo := newMockJobRepository(t)
		w := NewWorker(nil, repo)
		w.RegisterHandler(testJobType, func(ctx context.Context, config json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("boom")
		})

		job := &domain.Job{ID: 3, Type: testJobType, Attempts: 1, MaxAttempts: 3}
		repo.On("UpdateToPending", mock.Anything, int64(3), mock.Anything,
			mock.MatchedBy(func(s *string) bool { return s != nil && *s == "boom" })).Return(nil)

		w.processJob(context.Background(), job)

		repo.AssertExpectations(t)
		repo.AssertNotCalled(t, "UpdateStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("handler error with attempts exhausted fails the job", func(t *testing.T) {
		repo := newMockJobRepository(t)
		w := NewWorker(nil, repo)
		w.RegisterHandler(testJobType, func(ctx context.Context, config json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("boom")
		})

		job := &domain.Job{ID: 4, Type: testJobType, Attempts: 3, MaxAttempts: 3}
		repo.On("UpdateStatus", mock.Anything, int64(4), domain.JobStatusFailed, nil,
			mock.MatchedBy(func(s *string) bool { return s != nil && *s == "boom" }), mock.Anything).Return(nil)

		w.processJob(context.Background(), job)

		repo.AssertExpectations(t)
		repo.AssertNotCalled(t, "UpdateToPending", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestShouldRetry(t *testing.T) {
	w := NewWorker(nil, nil)

	tests := []struct {
		name        string
		attempts    int
		maxAttempts int
		want        bool
	}{
		{name: "attempts reached max", attempts: 5, maxAttempts: 5, want: false},
		{name: "attempts exceed max", attempts: 6, maxAttempts: 5, want: false},
		{name: "attempts below max", attempts: 2, maxAttempts: 5, want: true},
		{name: "max attempts of zero means unlimited", attempts: 100, maxAttempts: 0, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &domain.Job{Attempts: tt.attempts, MaxAttempts: tt.maxAttempts}
			require.Equal(t, tt.want, w.shouldRetry(job))
		})
	}
}

func TestRequeueJobBackoff(t *testing.T) {
	tests := []struct {
		name            string
		attempts        int
		expectedMinutes int
	}{
		{name: "first attempt backs off one minute", attempts: 1, expectedMinutes: 1},
		{name: "third attempt backs off four minutes", attempts: 3, expectedMinutes: 4},
		{name: "high attempt count caps at sixty minutes", attempts: 7, expectedMinutes: 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockJobRepository(t)
			w := NewWorker(nil, repo)

			var capturedRunAfter string
			repo.On("UpdateToPending", mock.Anything, int64(9), mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					capturedRunAfter = *(args.Get(2).(*string))
				}).
				Return(nil)

			job := &domain.Job{ID: 9, Attempts: tt.attempts}
			before := time.Now()
			w.requeueJob(context.Background(), job, errors.New("boom"), time.Now())

			got, err := time.Parse(time.RFC3339, capturedRunAfter)
			require.NoError(t, err)

			expected := before.Add(time.Duration(tt.expectedMinutes) * time.Minute)
			require.WithinDuration(t, expected, got, 5*time.Second)

			repo.AssertExpectations(t)
		})
	}

	t.Run("falls back to failing the job when requeue itself fails", func(t *testing.T) {
		repo := newMockJobRepository(t)
		w := NewWorker(nil, repo)

		repo.On("UpdateToPending", mock.Anything, int64(10), mock.Anything, mock.Anything).
			Return(errors.New("update failed"))
		repo.On("UpdateStatus", mock.Anything, int64(10), domain.JobStatusFailed, nil,
			mock.MatchedBy(func(s *string) bool { return s != nil && *s == "boom" }), mock.Anything).
			Return(nil)

		job := &domain.Job{ID: 10, Attempts: 1}
		w.requeueJob(context.Background(), job, errors.New("boom"), time.Now())

		repo.AssertExpectations(t)
	})
}
