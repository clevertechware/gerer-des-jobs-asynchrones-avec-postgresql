package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
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
	type fields struct {
		repo func(t *testing.T, tenantID string) *mocks.JobRepository
	}
	type args struct {
		request func(t *testing.T, tenantID string) *http.Request
	}
	tests := []struct {
		name     string
		fields   fields
		args     args
		want     int
		wantBody string
		check    func(t *testing.T, w *httptest.ResponseRecorder, uploadDir string)
	}{
		{
			name: "non-multipart body returns 400",
			args: args{
				request: func(t *testing.T, tenantID string) *http.Request {
					req := httptest.NewRequest(http.MethodPost, "/jobs/csv", bytes.NewBufferString("not multipart"))
					req.Header.Set("Content-Type", "text/plain")
					return req
				},
			},
			want: http.StatusBadRequest,
		},
		{
			name: "missing file returns 400",
			args: args{
				request: func(t *testing.T, tenantID string) *http.Request {
					return buildUploadRequest(t, "/jobs/csv?tenant_id="+tenantID, false, "")
				},
			},
			want:     http.StatusBadRequest,
			wantBody: "no file provided",
		},
		{
			name: "missing tenant_id returns 400",
			args: args{
				request: func(t *testing.T, tenantID string) *http.Request {
					return buildUploadRequest(t, "/jobs/csv", true, "a,b\n1,2")
				},
			},
			want:     http.StatusBadRequest,
			wantBody: "tenant_id is required",
		},
		{
			name: "invalid tenant_id format returns 400",
			args: args{
				request: func(t *testing.T, tenantID string) *http.Request {
					return buildUploadRequest(t, "/jobs/csv?tenant_id=not-a-uuid", true, "a,b\n1,2")
				},
			},
			want:     http.StatusBadRequest,
			wantBody: "invalid tenant_id format",
		},
		{
			name: "valid upload creates job and saves file",
			fields: fields{
				repo: func(t *testing.T, tenantID string) *mocks.JobRepository {
					repo := mocks.NewJobRepository(t)
					createdJob := &domain.Job{ID: 42, Status: domain.JobStatusPending}
					repo.EXPECT().Create(mock.Anything, tenantID, domain.JobTypeCSVImport, mock.MatchedBy(func(cfg domain.CSVImportConfig) bool {
						return cfg.Delimiter == "," && cfg.HasHeader
					})).Return(createdJob, nil)
					return repo
				},
			},
			args: args{
				request: func(t *testing.T, tenantID string) *http.Request {
					return buildUploadRequest(t, "/jobs/csv?tenant_id="+tenantID, true, "name,age\nJohn,25")
				},
			},
			want: http.StatusAccepted,
			check: func(t *testing.T, w *httptest.ResponseRecorder, uploadDir string) {
				var resp CreateCSVJobResponse
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, int64(42), resp.JobID)
				assert.Equal(t, string(domain.JobStatusPending), resp.Status)

				// The file must have landed on disk under uploadDir.
				entries, err := os.ReadDir(uploadDir)
				require.NoError(t, err)
				assert.Len(t, entries, 1)
				if len(entries) == 1 {
					assert.Equal(t, ".csv", filepath.Ext(entries[0].Name()))
				}
			},
		},
		{
			name: "repository error returns 500",
			fields: fields{
				repo: func(t *testing.T, tenantID string) *mocks.JobRepository {
					repo := mocks.NewJobRepository(t)
					repo.EXPECT().Create(mock.Anything, tenantID, domain.JobTypeCSVImport, mock.Anything).
						Return(nil, assert.AnError)
					return repo
				},
			},
			args: args{
				request: func(t *testing.T, tenantID string) *http.Request {
					return buildUploadRequest(t, "/jobs/csv?tenant_id="+tenantID, true, "a,b\n1,2")
				},
			},
			want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenantID := uuid.New().String()

			var repo *mocks.JobRepository
			if tt.fields.repo != nil {
				repo = tt.fields.repo(t, tenantID)
			} else {
				repo = mocks.NewJobRepository(t)
			}
			uploadDir := t.TempDir()
			h := NewJobHandler(repo, uploadDir)
			router := newTestRouter(h)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, tt.args.request(t, tenantID))

			assert.Equal(t, tt.want, w.Code)
			if tt.wantBody != "" {
				assert.Contains(t, w.Body.String(), tt.wantBody)
			}
			if tt.check != nil {
				tt.check(t, w, uploadDir)
			}
		})
	}
}

func TestGetJob(t *testing.T) {
	type fields struct {
		repo func(t *testing.T) *mocks.JobRepository
	}
	type args struct {
		id string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
		check  func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name: "non-numeric id returns 400",
			args: args{id: "not-an-id"},
			want: http.StatusBadRequest,
		},
		{
			name: "repository error returns 404",
			fields: fields{
				repo: func(t *testing.T) *mocks.JobRepository {
					repo := mocks.NewJobRepository(t)
					repo.EXPECT().GetByID(mock.Anything, int64(7)).Return(nil, assert.AnError)
					return repo
				},
			},
			args: args{id: "7"},
			want: http.StatusNotFound,
		},
		{
			name: "success parses config and result",
			fields: fields{
				repo: func(t *testing.T) *mocks.JobRepository {
					repo := mocks.NewJobRepository(t)
					job := &domain.Job{
						ID:     7,
						Status: domain.JobStatusCompleted,
						Config: json.RawMessage(`{"file_path":"/tmp/x.csv","delimiter":",","has_header":true}`),
						Result: json.RawMessage(`{"rows_processed":3,"rows_inserted":3}`),
					}
					repo.EXPECT().GetByID(mock.Anything, int64(7)).Return(job, nil)
					return repo
				},
			},
			args: args{id: "7"},
			want: http.StatusOK,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp struct {
					ID     int64                  `json:"id"`
					Config domain.CSVImportConfig `json:"config"`
					Result domain.CSVImportResult `json:"result"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, int64(7), resp.ID)
				assert.Equal(t, ",", resp.Config.Delimiter)
				assert.True(t, resp.Config.HasHeader)
				assert.Equal(t, 3, resp.Result.RowsProcessed)
				assert.Equal(t, 3, resp.Result.RowsInserted)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var repo *mocks.JobRepository
			if tt.fields.repo != nil {
				repo = tt.fields.repo(t)
			} else {
				repo = mocks.NewJobRepository(t)
			}
			h := NewJobHandler(repo, t.TempDir())
			router := newTestRouter(h)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs/"+tt.args.id, nil))

			assert.Equal(t, tt.want, w.Code)
			if tt.check != nil {
				tt.check(t, w)
			}
		})
	}
}

func TestGetJobStats(t *testing.T) {
	type fields struct {
		repo func(t *testing.T) *mocks.JobRepository
	}
	tests := []struct {
		name   string
		fields fields
		want   int
		check  func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name: "success returns stats",
			fields: fields{
				repo: func(t *testing.T) *mocks.JobRepository {
					repo := mocks.NewJobRepository(t)
					stats := &domain.QueueStats{TotalPending: 2, TotalRunning: 1, ByType: map[domain.JobType]domain.JobStats{}}
					repo.EXPECT().GetQueueStats(mock.Anything).Return(stats, nil)
					return repo
				},
			},
			want: http.StatusOK,
			check: func(t *testing.T, w *httptest.ResponseRecorder) {
				var resp domain.QueueStats
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, 2, resp.TotalPending)
				assert.Equal(t, 1, resp.TotalRunning)
			},
		},
		{
			name: "repository error returns 500",
			fields: fields{
				repo: func(t *testing.T) *mocks.JobRepository {
					repo := mocks.NewJobRepository(t)
					repo.EXPECT().GetQueueStats(mock.Anything).Return(nil, assert.AnError)
					return repo
				},
			},
			want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := tt.fields.repo(t)
			h := NewJobHandler(repo, t.TempDir())
			router := newTestRouter(h)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jobs/stats", nil))

			assert.Equal(t, tt.want, w.Code)
			if tt.check != nil {
				tt.check(t, w)
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
