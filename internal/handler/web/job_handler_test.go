package web

import (
	"bytes"
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
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/handler/web/mocks"
)

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
	tests := []struct {
		name             string
		buildRequest     func(t *testing.T, tenantID string) *http.Request
		setupMock        func(repo *mocks.JobRepository, tenantID string)
		wantStatus       int
		wantBodyContains string
		checkResponse    func(t *testing.T, w *httptest.ResponseRecorder, uploadDir string)
	}{
		{
			name: "non-multipart body returns 400",
			buildRequest: func(t *testing.T, tenantID string) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "/jobs/csv", bytes.NewBufferString("not multipart"))
				req.Header.Set("Content-Type", "text/plain")
				return req
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing file returns 400",
			buildRequest: func(t *testing.T, tenantID string) *http.Request {
				return buildUploadRequest(t, "/jobs/csv?tenant_id="+tenantID, false, "")
			},
			wantStatus:       http.StatusBadRequest,
			wantBodyContains: "no file provided",
		},
		{
			name: "missing tenant_id returns 400",
			buildRequest: func(t *testing.T, tenantID string) *http.Request {
				return buildUploadRequest(t, "/jobs/csv", true, "a,b\n1,2")
			},
			wantStatus:       http.StatusBadRequest,
			wantBodyContains: "tenant_id is required",
		},
		{
			name: "invalid tenant_id format returns 400",
			buildRequest: func(t *testing.T, tenantID string) *http.Request {
				return buildUploadRequest(t, "/jobs/csv?tenant_id=not-a-uuid", true, "a,b\n1,2")
			},
			wantStatus:       http.StatusBadRequest,
			wantBodyContains: "invalid tenant_id format",
		},
		{
			name: "valid upload creates job and saves file",
			buildRequest: func(t *testing.T, tenantID string) *http.Request {
				return buildUploadRequest(t, "/jobs/csv?tenant_id="+tenantID, true, "name,age\nJohn,25")
			},
			setupMock: func(repo *mocks.JobRepository, tenantID string) {
				createdJob := &domain.Job{ID: 42, Status: domain.JobStatusPending}
				repo.EXPECT().Create(mock.Anything, tenantID, domain.JobTypeCSVImport, mock.MatchedBy(func(cfg domain.CSVImportConfig) bool {
					return cfg.Delimiter == "," && cfg.HasHeader
				})).Return(createdJob, nil)
			},
			wantStatus: http.StatusAccepted,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder, uploadDir string) {
				var resp CreateCSVJobResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				require.Equal(t, int64(42), resp.JobID)
				require.Equal(t, string(domain.JobStatusPending), resp.Status)

				// The file must have landed on disk under uploadDir.
				entries, err := os.ReadDir(uploadDir)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				require.Equal(t, ".csv", filepath.Ext(entries[0].Name()))
			},
		},
		{
			name: "repository error returns 500",
			buildRequest: func(t *testing.T, tenantID string) *http.Request {
				return buildUploadRequest(t, "/jobs/csv?tenant_id="+tenantID, true, "a,b\n1,2")
			},
			setupMock: func(repo *mocks.JobRepository, tenantID string) {
				repo.EXPECT().Create(mock.Anything, tenantID, domain.JobTypeCSVImport, mock.Anything).
					Return(nil, errors.New("db down"))
			},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := mocks.NewJobRepository(t)
			uploadDir := t.TempDir()
			h := NewJobHandler(repo, uploadDir)
			router := newTestRouter(h)

			tenantID := uuid.New().String()
			if tt.setupMock != nil {
				tt.setupMock(repo, tenantID)
			}

			w := httptest.NewRecorder()
			router.ServeHTTP(w, tt.buildRequest(t, tenantID))

			require.Equal(t, tt.wantStatus, w.Code)
			if tt.wantBodyContains != "" {
				require.Contains(t, w.Body.String(), tt.wantBodyContains)
			}
			if tt.checkResponse != nil {
				tt.checkResponse(t, w, uploadDir)
			}
		})
	}
}

func TestGetJob(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		setupMock     func(repo *mocks.JobRepository)
		wantStatus    int
		checkResponse func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name:       "non-numeric id returns 400",
			path:       "/jobs/not-an-id",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "repository error returns 404",
			path: "/jobs/7",
			setupMock: func(repo *mocks.JobRepository) {
				repo.EXPECT().GetByID(mock.Anything, int64(7)).Return(nil, errors.New("not found"))
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "success parses config and result",
			path: "/jobs/7",
			setupMock: func(repo *mocks.JobRepository) {
				job := &domain.Job{
					ID:     7,
					Status: domain.JobStatusCompleted,
					Config: json.RawMessage(`{"file_path":"/tmp/x.csv","delimiter":",","has_header":true}`),
					Result: json.RawMessage(`{"rows_processed":3,"rows_inserted":3}`),
				}
				repo.EXPECT().GetByID(mock.Anything, int64(7)).Return(job, nil)
			},
			wantStatus: http.StatusOK,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp struct {
					ID     int64                  `json:"id"`
					Config domain.CSVImportConfig `json:"config"`
					Result domain.CSVImportResult `json:"result"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				require.Equal(t, int64(7), resp.ID)
				require.Equal(t, ",", resp.Config.Delimiter)
				require.True(t, resp.Config.HasHeader)
				require.Equal(t, 3, resp.Result.RowsProcessed)
				require.Equal(t, 3, resp.Result.RowsInserted)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := mocks.NewJobRepository(t)
			h := NewJobHandler(repo, t.TempDir())
			router := newTestRouter(h)

			if tt.setupMock != nil {
				tt.setupMock(repo)
			}

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))

			require.Equal(t, tt.wantStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
}

func TestGetJobStats(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(repo *mocks.JobRepository)
		wantStatus    int
		checkResponse func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name: "success returns stats",
			setupMock: func(repo *mocks.JobRepository) {
				stats := &domain.QueueStats{TotalPending: 2, TotalRunning: 1, ByType: map[domain.JobType]domain.JobStats{}}
				repo.EXPECT().GetQueueStats(mock.Anything).Return(stats, nil)
			},
			wantStatus: http.StatusOK,
			checkResponse: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp domain.QueueStats
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				require.Equal(t, 2, resp.TotalPending)
				require.Equal(t, 1, resp.TotalRunning)
			},
		},
		{
			name: "repository error returns 500",
			setupMock: func(repo *mocks.JobRepository) {
				repo.EXPECT().GetQueueStats(mock.Anything).Return(nil, errors.New("boom"))
			},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := mocks.NewJobRepository(t)
			h := NewJobHandler(repo, t.TempDir())
			router := newTestRouter(h)

			tt.setupMock(repo)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs/stats", nil))

			require.Equal(t, tt.wantStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w)
			}
		})
	}
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
			repo := mocks.NewJobRepository(t)
			h := NewJobHandler(repo, t.TempDir())
			router := newTestRouter(h)

			repo.EXPECT().GetJobsByStatus(mock.Anything, tt.expectedStatus, tt.expectedLimit).
				Return([]*domain.Job{}, nil)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs"+tt.queryString, nil))

			require.Equal(t, http.StatusOK, w.Code)
		})
	}
}
