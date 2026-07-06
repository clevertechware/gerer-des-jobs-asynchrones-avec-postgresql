package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/domain"
)

// CSVImportUsecase handles CSV import business logic
type CSVImportUsecase struct {
	uploadDir string
	// In a real app, you would have a database repository here for idempotency
	processedFiles map[string]bool
}

// NewCSVImportUsecase creates a new CSVImportUsecase
func NewCSVImportUsecase(uploadDir string) *CSVImportUsecase {
	return &CSVImportUsecase{
		uploadDir:      uploadDir,
		processedFiles: make(map[string]bool),
	}
}

// Process processes a CSV import job
// It reads the CSV file, parses it, and returns processing results
func (uc *CSVImportUsecase) Process(ctx context.Context, configJSON json.RawMessage) (json.RawMessage, error) {
	var config domain.CSVImportConfig
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate config
	if config.FilePath == "" {
		return nil, fmt.Errorf("file path is required")
	}

	// Ensure delimiter is set
	if config.Delimiter == "" {
		config.Delimiter = ","
	}

	startTime := time.Now()

	// Open the file once: hash it for idempotency, then reuse the handle to parse the CSV
	// if it turns out to be new (avoids a second os.Open + full read on the common path).
	file, err := os.Open(config.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, fmt.Errorf("failed to calculate file hash: %w", err)
	}
	fileHash := hex.EncodeToString(hasher.Sum(nil))

	// Check if file already processed (idempotency)
	if uc.processedFiles[fileHash] {
		return json.Marshal(domain.CSVImportResult{
			FileHash:  fileHash,
			FileName:  filepath.Base(config.FilePath),
			StartTime: startTime.Format(time.RFC3339),
			EndTime:   time.Now().Format(time.RFC3339),
			Errors:    []string{"file already processed"},
		})
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to rewind CSV file: %w", err)
	}

	reader := csv.NewReader(file)
	reader.Comma = rune(config.Delimiter[0])
	if !config.HasHeader {
		reader.FieldsPerRecord = -1 // Allow variable number of fields
	}

	var result domain.CSVImportResult
	result.FileHash = fileHash
	result.FileName = filepath.Base(config.FilePath)
	result.StartTime = startTime.Format(time.RFC3339)

	// Process records one at a time (simulate inserting into database)
	var errors []string
	for i := 0; ; i++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read CSV: %w", err)
		}

		result.RowsProcessed++

		// Skip empty records
		if len(record) == 0 {
			result.RowsSkipped++
			continue
		}

		// Validate record (example validation)
		if err := uc.validateRecord(record, i+1); err != nil {
			errors = append(errors, err.Error())
			result.RowsSkipped++
			continue
		}

		// Simulate database insert
		result.RowsInserted++
	}

	result.Errors = errors
	result.EndTime = time.Now().Format(time.RFC3339)

	// Mark file as processed
	uc.processedFiles[fileHash] = true
	slog.InfoContext(ctx, "Processed file", "file_hash", fileHash)

	return json.Marshal(result)
}

// validateRecord validates a CSV record
func (uc *CSVImportUsecase) validateRecord(record []string, lineNum int) error {
	// Basic validation: ensure record is not empty and fields are not all empty
	if len(record) == 0 {
		return fmt.Errorf("line %d: empty record", lineNum)
	}

	// Check if all fields are empty
	allEmpty := true
	for _, field := range record {
		if strings.TrimSpace(field) != "" {
			allEmpty = false
			break
		}
	}
	if allEmpty {
		return fmt.Errorf("line %d: all fields are empty", lineNum)
	}

	return nil
}

// GetJobHandler returns a JobHandler function for CSV import jobs
func (uc *CSVImportUsecase) GetJobHandler() func(ctx context.Context, config json.RawMessage) (json.RawMessage, error) {
	return uc.Process
}
