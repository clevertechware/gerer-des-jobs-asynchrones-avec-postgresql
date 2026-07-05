package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds the application configuration
type Config struct {
	// Database configuration
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// Server configuration
	ServerPort int

	// Worker configuration
	WorkerBatchSize    int
	WorkerPollInterval time.Duration
	WorkerConcurrency  int

	// File storage configuration
	UploadDir string
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		DBHost:             "localhost",
		DBPort:             5432,
		DBUser:             "postgres",
		DBPassword:         "postgres",
		DBName:             "csv_job_processor",
		DBSSLMode:          "disable",
		ServerPort:         8080,
		WorkerBatchSize:    5,
		WorkerPollInterval: 100 * time.Millisecond,
		WorkerConcurrency:  3,
		UploadDir:          "./uploads",
	}
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	// Database
	if v := os.Getenv("DB_HOST"); v != "" {
		cfg.DBHost = v
	}
	if v := os.Getenv("DB_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid DB_PORT: %w", err)
		}
		cfg.DBPort = port
	}
	if v := os.Getenv("DB_USER"); v != "" {
		cfg.DBUser = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.DBPassword = v
	}
	if v := os.Getenv("DB_NAME"); v != "" {
		cfg.DBName = v
	}
	if v := os.Getenv("DB_SSL_MODE"); v != "" {
		cfg.DBSSLMode = v
	}

	// Server
	if v := os.Getenv("SERVER_PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid SERVER_PORT: %w", err)
		}
		cfg.ServerPort = port
	}

	// Worker
	if v := os.Getenv("WORKER_BATCH_SIZE"); v != "" {
		batchSize, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid WORKER_BATCH_SIZE: %w", err)
		}
		cfg.WorkerBatchSize = batchSize
	}
	if v := os.Getenv("WORKER_POLL_INTERVAL_MS"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid WORKER_POLL_INTERVAL_MS: %w", err)
		}
		cfg.WorkerPollInterval = time.Duration(ms) * time.Millisecond
	}
	if v := os.Getenv("WORKER_CONCURRENCY"); v != "" {
		concurrency, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid WORKER_CONCURRENCY: %w", err)
		}
		cfg.WorkerConcurrency = concurrency
	}

	// Upload directory
	if v := os.Getenv("UPLOAD_DIR"); v != "" {
		cfg.UploadDir = v
	}

	return cfg, nil
}

// GetDSN returns the PostgreSQL connection string
func (c *Config) GetDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode)
}
