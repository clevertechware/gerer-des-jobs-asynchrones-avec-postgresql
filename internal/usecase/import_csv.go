package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// ImportCSVConfig contains configuration for CSV import
type ImportCSVConfig struct {
	FilePath    string `json:"file_path"`
	Delimiter   string `json:"delimiter"`
	HasHeader   bool   `json:"has_header"`
	TargetTable string `json:"target_table,omitempty"`
}

// ImportCSVResult contains the result of a CSV import
type ImportCSVResult struct {
	RowsProcessed int      `json:"rows_processed"`
	RowsInserted  int      `json:"rows_inserted"`
	RowsSkipped   int      `json:"rows_skipped"`
	Errors        []string `json:"errors,omitempty"`
	FileHash      string   `json:"file_hash"`
	FileName      string   `json:"file_name"`
	StartTime     string   `json:"start_time"`
	EndTime       string   `json:"end_time"`
}

// Process processes a CSV import job
// It reads the CSV file, parses it, and returns processing results
func (uc *CSVImportUsecase) Process(ctx context.Context, configJSON json.RawMessage) (json.RawMessage, error) {
	var config ImportCSVConfig
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

	// Calculate file hash for idempotency
	fileHash, err := uc.calculateFileHash(config.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate file hash: %w", err)
	}

	// Check if file already processed (idempotency)
	if uc.processedFiles[fileHash] {
		result := ImportCSVResult{
			FileHash:  fileHash,
			FileName:  filepath.Base(config.FilePath),
			StartTime: startTime.Format(time.RFC3339),
			EndTime:   time.Now().Format(time.RFC3339),
			Errors:    []string{"file already processed"},
		}
		return json.Marshal(result)
	}

	// Open the CSV file
	file, err := os.Open(config.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer file.Close()

	// Parse the CSV
	reader := csv.NewReader(file)
	reader.Comma = rune(config.Delimiter[0])
	if !config.HasHeader {
		reader.FieldsPerRecord = -1 // Allow variable number of fields
	}

	// Read all records
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV: %w", err)
	}

	var result ImportCSVResult
	result.FileHash = fileHash
	result.FileName = filepath.Base(config.FilePath)
	result.StartTime = startTime.Format(time.RFC3339)
	result.RowsProcessed = len(records)

	// Process records (simulate inserting into database)
	var errors []string
	for i, record := range records {
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

	return json.Marshal(result)
}

// calculateFileHash calculates SHA256 hash of a file for idempotency
func (uc *CSVImportUsecase) calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
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
