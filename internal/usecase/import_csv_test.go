package usecase

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/domain"
)

func TestCSVImportUsecase_Process(t *testing.T) {
	// Create temp directory for test files
	tempDir := t.TempDir()
	uc := NewCSVImportUsecase(tempDir)

	tests := []struct {
		name           string
		csvContent     string
		config         domain.CSVImportConfig
		expectedResult func(*domain.CSVImportResult) bool
		expectError    bool
	}{
		{
			name: "valid CSV with header",
			csvContent: `name,age,city
John,25,NYC
Jane,30,LA
Bob,35,Chicago`,
			config: domain.CSVImportConfig{
				Delimiter: ",",
				HasHeader: true,
			},
			expectedResult: func(r *domain.CSVImportResult) bool {
				// 4 lines total: header + 3 data rows
				return r.RowsProcessed == 4 && r.RowsInserted == 4 && len(r.Errors) == 0
			},
			expectError: false,
		},
		{
			name: "valid CSV without header",
			csvContent: `John,25,NYC
Jane,30,LA
Bob,35,Chicago`,
			config: domain.CSVImportConfig{
				Delimiter: ",",
				HasHeader: false,
			},
			expectedResult: func(r *domain.CSVImportResult) bool {
				return r.RowsProcessed == 3 && r.RowsInserted == 3 && len(r.Errors) == 0
			},
			expectError: false,
		},
		{
			name: "CSV with semicolon delimiter",
			csvContent: `name;age;city
John;25;NYC
Jane;30;LA`,
			config: domain.CSVImportConfig{
				Delimiter: ";",
				HasHeader: true,
			},
			expectedResult: func(r *domain.CSVImportResult) bool {
				// 3 lines total: header + 2 data rows
				return r.RowsProcessed == 3 && r.RowsInserted == 3 && len(r.Errors) == 0
			},
			expectError: false,
		},
		{
			name: "CSV with empty lines",
			csvContent: `name,age,city
John,25,NYC

Jane,30,LA

Bob,35,Chicago`,
			config: domain.CSVImportConfig{
				Delimiter: ",",
				HasHeader: true,
			},
			expectedResult: func(r *domain.CSVImportResult) bool {
				// Go's encoding/csv reader skips empty lines by default
				// So we get 4 records: header + 3 data rows (empty lines are ignored)
				// All valid rows are inserted
				return r.RowsProcessed == 4 && r.RowsInserted == 4 && len(r.Errors) == 0
			},
			expectError: false,
		},
		{
			name: "CSV with all empty fields",
			csvContent: `,,,
,,,`,
			config: domain.CSVImportConfig{
				Delimiter: ",",
				HasHeader: false,
			},
			expectedResult: func(r *domain.CSVImportResult) bool {
				// Should skip all-empty records
				return r.RowsSkipped == 2 && r.RowsInserted == 0
			},
			expectError: false,
		},
		{
			name:       "empty CSV file",
			csvContent: "",
			config: domain.CSVImportConfig{
				Delimiter: ",",
				HasHeader: true,
			},
			expectedResult: func(r *domain.CSVImportResult) bool {
				return r.RowsProcessed == 0 && r.RowsInserted == 0
			},
			expectError: false,
		},
		{
			name:       "missing file path",
			csvContent: "test",
			config: domain.CSVImportConfig{
				FilePath:  "",
				Delimiter: ",",
				HasHeader: true,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip file creation for missing file path test
			if tt.config.FilePath == "" && tt.expectError {
				// Marshal config to JSON (with empty file path)
				configJSON, err := json.Marshal(tt.config)
				if err != nil {
					t.Fatalf("Failed to marshal config: %v", err)
				}

				// Process the job - should fail
				_, err = uc.Process(context.Background(), configJSON)
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			// Create CSV file
			filePath := filepath.Join(tempDir, "test_"+tt.name+".csv")
			if err := os.WriteFile(filePath, []byte(tt.csvContent), 0644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Set file path in config
			config := tt.config
			config.FilePath = filePath

			// Marshal config to JSON
			configJSON, err := json.Marshal(config)
			if err != nil {
				t.Fatalf("Failed to marshal config: %v", err)
			}

			// Process the job
			resultJSON, err := uc.Process(context.Background(), configJSON)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Unmarshal result
			var result domain.CSVImportResult
			if err := json.Unmarshal(resultJSON, &result); err != nil {
				t.Fatalf("Failed to unmarshal result: %v", err)
			}

			if !tt.expectedResult(&result) {
				t.Errorf("Unexpected result: %+v", result)
			}
		})
	}
}

func TestCSVImportUsecase_Idempotency(t *testing.T) {
	// Create temp directory for test files
	tempDir := t.TempDir()
	uc := NewCSVImportUsecase(tempDir)

	// Create CSV file
	csvContent := `name,age,city
John,25,NYC
Jane,30,LA`

	filePath := filepath.Join(tempDir, "idempotency_test.csv")
	if err := os.WriteFile(filePath, []byte(csvContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	config := domain.CSVImportConfig{
		FilePath:  filePath,
		Delimiter: ",",
		HasHeader: true,
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	// Process the job first time
	firstResultJSON, err := uc.Process(context.Background(), configJSON)
	if err != nil {
		t.Fatalf("First processing failed: %v", err)
	}

	var firstResult domain.CSVImportResult
	if err := json.Unmarshal(firstResultJSON, &firstResult); err != nil {
		t.Fatalf("Failed to unmarshal first result: %v", err)
	}

	// Process the same job again (should be idempotent)
	secondResultJSON, err := uc.Process(context.Background(), configJSON)
	if err != nil {
		t.Fatalf("Second processing failed: %v", err)
	}

	var secondResult domain.CSVImportResult
	if err := json.Unmarshal(secondResultJSON, &secondResult); err != nil {
		t.Fatalf("Failed to unmarshal second result: %v", err)
	}

	// Second processing should indicate file already processed
	if len(secondResult.Errors) == 0 || secondResult.Errors[0] != "file already processed" {
		t.Errorf("Expected idempotency error, got: %v", secondResult.Errors)
	}

	// Both should have the same file hash
	if firstResult.FileHash != secondResult.FileHash {
		t.Errorf("File hashes don't match: %s vs %s", firstResult.FileHash, secondResult.FileHash)
	}
}

func TestCSVImportUsecase_DifferentFiles(t *testing.T) {
	tempDir := t.TempDir()
	// Create separate usecase instances for each test to avoid idempotency issues
	uc1 := NewCSVImportUsecase(tempDir)

	// Create two different CSV files
	csv1 := `name,age
John,25`
	csv2 := `name,age
Jane,30`

	file1 := filepath.Join(tempDir, "file1.csv")
	file2 := filepath.Join(tempDir, "file2.csv")

	if err := os.WriteFile(file1, []byte(csv1), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte(csv2), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}

	// Process both files with separate usecase instances
	config1 := domain.CSVImportConfig{FilePath: file1, Delimiter: ",", HasHeader: true}
	config2 := domain.CSVImportConfig{FilePath: file2, Delimiter: ",", HasHeader: true}

	config1JSON, _ := json.Marshal(config1)
	config2JSON, _ := json.Marshal(config2)

	result1JSON, err := uc1.Process(context.Background(), config1JSON)
	if err != nil {
		t.Fatalf("Failed to process file1: %v", err)
	}

	// Use a new usecase instance for the second file to avoid idempotency
	uc2Second := NewCSVImportUsecase(tempDir)
	result2JSON, err := uc2Second.Process(context.Background(), config2JSON)
	if err != nil {
		t.Fatalf("Failed to process file2: %v", err)
	}

	var result1, result2 domain.CSVImportResult
	if err := json.Unmarshal(result1JSON, &result1); err != nil {
		t.Fatalf("Failed to unmarshal result1: %v", err)
	}
	if err := json.Unmarshal(result2JSON, &result2); err != nil {
		t.Fatalf("Failed to unmarshal result2: %v", err)
	}

	// Should have different file hashes
	if result1.FileHash == result2.FileHash {
		t.Errorf("Different files should have different hashes")
	}

	// Both should succeed (2 lines each: header + 1 data row)
	if result1.RowsInserted != 2 {
		t.Errorf("File1 should have 2 rows inserted, got %d", result1.RowsInserted)
	}
	if result2.RowsInserted != 2 {
		t.Errorf("File2 should have 2 rows inserted, got %d", result2.RowsInserted)
	}
}
