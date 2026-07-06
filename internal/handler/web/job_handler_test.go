package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

func (m *mockJobRepository) Create(ctx context.Context, tenantID string, jobType domain.JobType, config interface{}) (*domain.Job, error) {
	args := m.Called(ctx, tenantID, jobType, config)
	job, _ := args.Get(0).(*domain.Job)
	return job, args.Error(1)
}

func (m *mockJobRepository) GetByID(ctx context.Context, id int64) (*domain.Job, error) {
	args := m.Called(ctx, id)
	job, _ := args.Get(0).(*domain.Job)
	return job, args.Error(1)
}

func (m *mockJobRepository) GetQueueStats(ctx context.Context) (*domain.QueueStats, error) {
	args := m.Called(ctx)
	stats, _ := args.Get(0).(*domain.QueueStats)
	return stats, args.Error(1)
}

func (m *mockJobRepository) GetJobsByStatus(ctx context.Context, status domain.JobStatus, limit int) ([]*domain.Job, error) {
	args := m.Called(ctx, status, limit)
	jobs, _ := args.Get(0).([]*domain.Job)
	return jobs, args.Error(1)
}

func newTestRouter(h *JobHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h.RegisterRoutes(router)
	return router
}

// buildUploadRequest builds a multipart POST request for /jobs/csv.
// If includeFile is false, no "file" part is added (to exercise the missing-file branch).
func buildUploadRequest(t *testing.T, url string, includeFile bool, fileContent string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if includeFile {
		part, err := writer.CreateFormFile("file", "data.csv")
		require.NoError(t, err)
		_, err = part.Write([]byte(fileContent))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestUploadCSVFile(t *testing.T) {
	t.Run("non-multipart body returns 400", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		req := httptest.NewRequest(http.MethodPost, "/jobs/csv", bytes.NewBufferString("not multipart"))
		req.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing file returns 400", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		uuidVal := uuid.New().String()
		req := buildUploadRequest(t, "/jobs/csv?tenant_id="+uuidVal, false, "")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "no file provided")
	})

	t.Run("missing tenant_id returns 400", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		req := buildUploadRequest(t, "/jobs/csv", true, "a,b\n1,2")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "tenant_id is required")
	})

	t.Run("invalid tenant_id format returns 400", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		req := buildUploadRequest(t, "/jobs/csv?tenant_id=not-a-uuid", true, "a,b\n1,2")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Contains(t, w.Body.String(), "invalid tenant_id format")
	})

	t.Run("valid upload creates job and saves file", func(t *testing.T) {
		repo := newMockJobRepository(t)
		uploadDir := t.TempDir()
		h := NewJobHandler(repo, uploadDir)
		router := newTestRouter(h)

		tenantID := uuid.New().String()
		createdJob := &domain.Job{ID: 42, Status: domain.JobStatusPending}
		repo.On("Create", mock.Anything, tenantID, domain.JobTypeCSVImport, mock.MatchedBy(func(cfg domain.CSVImportConfig) bool {
			return cfg.Delimiter == "," && cfg.HasHeader
		})).Return(createdJob, nil)

		req := buildUploadRequest(t, "/jobs/csv?tenant_id="+tenantID, true, "name,age\nJohn,25")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusAccepted, w.Code)

		var resp CreateCSVJobResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, int64(42), resp.JobID)
		require.Equal(t, string(domain.JobStatusPending), resp.Status)

		// The file must have landed on disk under uploadDir.
		entries, err := os.ReadDir(uploadDir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, ".csv", filepath.Ext(entries[0].Name()))

		repo.AssertExpectations(t)
	})

	t.Run("repository error returns 500", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		tenantID := uuid.New().String()
		repo.On("Create", mock.Anything, tenantID, domain.JobTypeCSVImport, mock.Anything).
			Return(nil, errors.New("db down"))

		req := buildUploadRequest(t, "/jobs/csv?tenant_id="+tenantID, true, "a,b\n1,2")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusInternalServerError, w.Code)
		repo.AssertExpectations(t)
	})
}

func TestGetJob(t *testing.T) {
	t.Run("non-numeric id returns 400", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs/not-an-id", nil))

		require.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("repository error returns 404", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		repo.On("GetByID", mock.Anything, int64(7)).Return(nil, errors.New("not found"))

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs/7", nil))

		require.Equal(t, http.StatusNotFound, w.Code)
		repo.AssertExpectations(t)
	})

	t.Run("success parses config and result", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		job := &domain.Job{
			ID:     7,
			Status: domain.JobStatusCompleted,
			Config: json.RawMessage(`{"file_path":"/tmp/x.csv","delimiter":",","has_header":true}`),
			Result: json.RawMessage(`{"rows_processed":3,"rows_inserted":3}`),
		}
		repo.On("GetByID", mock.Anything, int64(7)).Return(job, nil)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs/7", nil))

		require.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			ID     int64                   `json:"id"`
			Config domain.CSVImportConfig  `json:"config"`
			Result domain.CSVImportResult  `json:"result"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, int64(7), resp.ID)
		require.Equal(t, ",", resp.Config.Delimiter)
		require.True(t, resp.Config.HasHeader)
		require.Equal(t, 3, resp.Result.RowsProcessed)
		require.Equal(t, 3, resp.Result.RowsInserted)

		repo.AssertExpectations(t)
	})
}

func TestGetJobStats(t *testing.T) {
	t.Run("success returns stats", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		stats := &domain.QueueStats{TotalPending: 2, TotalRunning: 1, ByType: map[domain.JobType]domain.JobStats{}}
		repo.On("GetQueueStats", mock.Anything).Return(stats, nil)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs/stats", nil))

		require.Equal(t, http.StatusOK, w.Code)

		var resp domain.QueueStats
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, 2, resp.TotalPending)
		require.Equal(t, 1, resp.TotalRunning)

		repo.AssertExpectations(t)
	})

	t.Run("repository error returns 500", func(t *testing.T) {
		repo := newMockJobRepository(t)
		h := NewJobHandler(repo, t.TempDir())
		router := newTestRouter(h)

		repo.On("GetQueueStats", mock.Anything).Return(nil, errors.New("boom"))

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs/stats", nil))

		require.Equal(t, http.StatusInternalServerError, w.Code)
		repo.AssertExpectations(t)
	})
}

func TestListJobs(t *testing.T) {
	tests := []struct {
		name           string
		queryString    string
		expectedStatus domain.JobStatus
		expectedLimit  int
	}{
		{name: "defaults to pending with limit 50", queryString: "", expectedStatus: domain.JobStatusPending, expectedLimit: 50},
		{name: "unknown status falls back to pending", queryString: "?status=BOGUS", expectedStatus: domain.JobStatusPending, expectedLimit: 50},
		{name: "known status is passed through", queryString: "?status=RUNNING", expectedStatus: domain.JobStatusRunning, expectedLimit: 50},
		{name: "non-numeric limit defaults to 50", queryString: "?limit=abc", expectedStatus: domain.JobStatusPending, expectedLimit: 50},
		{name: "numeric limit is passed through", queryString: "?limit=5", expectedStatus: domain.JobStatusPending, expectedLimit: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockJobRepository(t)
			h := NewJobHandler(repo, t.TempDir())
			router := newTestRouter(h)

			repo.On("GetJobsByStatus", mock.Anything, tt.expectedStatus, tt.expectedLimit).
				Return([]*domain.Job{}, nil)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs"+tt.queryString, nil))

			require.Equal(t, http.StatusOK, w.Code)
			repo.AssertExpectations(t)
		})
	}
}
