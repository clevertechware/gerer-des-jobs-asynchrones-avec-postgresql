package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/domain"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/repository"
)

// JobHandler handles HTTP requests for job management
type JobHandler struct {
	repo      repository.JobRepository
	uploadDir string
}

// NewJobHandler creates a new JobHandler
func NewJobHandler(repo repository.JobRepository, uploadDir string) *JobHandler {
	return &JobHandler{
		repo:      repo,
		uploadDir: uploadDir,
	}
}

// CreateCSVJobRequest represents a request to create a CSV import job
type CreateCSVJobRequest struct {
	TenantID  string `json:"tenant_id"`
	FileName  string `json:"file_name"`
	Delimiter string `json:"delimiter"`
	HasHeader bool   `json:"has_header"`
}

// CreateCSVJobResponse represents the response for a created job
type CreateCSVJobResponse struct {
	JobID   int64  `json:"job_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// GetJobResponse represents the response for getting a job
type GetJobResponse struct {
	*domain.Job
	Config domain.CSVImportConfig `json:"config,omitempty"`
	Result domain.CSVImportResult `json:"result,omitempty"`
}

// UploadCSVFile handles CSV file uploads and creates a job
// POST /jobs/csv
func (h *JobHandler) UploadCSVFile(c *gin.Context) {
	// Parse multipart form
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse multipart form"})
		return
	}

	// Get file from form
	files := form.File["file"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no file provided"})
		return
	}

	file := files[0]

	// Validate tenant ID
	tenantID := c.DefaultQuery("tenant_id", "")
	if tenantID == "" {
		tenantID = c.GetHeader("X-Tenant-ID")
	}
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id is required"})
		return
	}

	// Validate tenant ID is a valid UUID
	if _, err := uuid.Parse(tenantID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant_id format"})
		return
	}

	// Parse additional parameters
	delimiter := c.DefaultQuery("delimiter", ",")
	hasHeader := c.DefaultQuery("has_header", "true") == "true"

	// Create upload directory if it doesn't exist
	if err := os.MkdirAll(h.uploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upload directory"})
		return
	}

	// Generate unique filename
	uniqueID := uuid.New().String()
	extension := filepath.Ext(file.Filename)
	uploadPath := filepath.Join(h.uploadDir, uniqueID+extension)

	// Save file
	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open uploaded file"})
		return
	}
	defer src.Close()

	dst, err := os.Create(uploadPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create destination file"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}

	// Create job configuration
	config := domain.CSVImportConfig{
		FilePath:    uploadPath,
		Delimiter:   delimiter,
		HasHeader:   hasHeader,
		TargetTable: c.DefaultQuery("target_table", ""),
	}

	// Create job
	job, err := h.repo.Create(c.Request.Context(), tenantID, domain.JobTypeCSVImport, config)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to create job: %v", err)})
		return
	}

	c.JSON(http.StatusAccepted, CreateCSVJobResponse{
		JobID:   job.ID,
		Status:  string(job.Status),
		Message: fmt.Sprintf("CSV import job created with ID %d", job.ID),
	})
}

// GetJob handles requests to get job status
// GET /jobs/:id
func (h *JobHandler) GetJob(c *gin.Context) {
	jobID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job ID"})
		return
	}

	job, err := h.repo.GetByID(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	response := GetJobResponse{
		Job: job,
	}

	// Parse config if present
	if len(job.Config) > 0 {
		var config domain.CSVImportConfig
		if err := json.Unmarshal(job.Config, &config); err == nil {
			response.Config = config
		}
	}

	// Parse result if present
	if len(job.Result) > 0 {
		var result domain.CSVImportResult
		if err := json.Unmarshal(job.Result, &result); err == nil {
			response.Result = result
		}
	}

	c.JSON(http.StatusOK, response)
}

// GetJobStats handles requests to get queue statistics
// GET /jobs/stats
func (h *JobHandler) GetJobStats(c *gin.Context) {
	stats, err := h.repo.GetQueueStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get stats: %v", err)})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// ListJobs handles requests to list jobs by status
// GET /jobs
func (h *JobHandler) ListJobs(c *gin.Context) {
	status := c.DefaultQuery("status", "PENDING")
	limit := c.DefaultQuery("limit", "50")

	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		limitInt = 50
	}

	knownStatuses := map[domain.JobStatus]bool{
		domain.JobStatusPending:          true,
		domain.JobStatusRunning:          true,
		domain.JobStatusCompleted:        true,
		domain.JobStatusCompletedWithErr: true,
		domain.JobStatusFailed:           true,
	}

	jobStatus := domain.JobStatus(status)
	if !knownStatuses[jobStatus] {
		jobStatus = domain.JobStatusPending
	}

	jobs, err := h.repo.GetJobsByStatus(c.Request.Context(), jobStatus, limitInt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to get jobs: %v", err)})
		return
	}

	c.JSON(http.StatusOK, jobs)
}

// RegisterRoutes registers the job handler routes
func (h *JobHandler) RegisterRoutes(router *gin.Engine) {
	jobGroup := router.Group("/jobs")
	{
		jobGroup.POST("/csv", h.UploadCSVFile)
		jobGroup.GET("/:id", h.GetJob)
		jobGroup.GET("/stats", h.GetJobStats)
		jobGroup.GET("", h.ListJobs)
	}
}
