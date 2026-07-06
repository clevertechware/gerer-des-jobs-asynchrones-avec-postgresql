package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/config"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/database"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/domain"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/handler/web"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/handler/worker"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/repository/postgres"
	"github.com/clevertechware/gerer-ses-jobs-asynchrones-avec-postgresql/internal/usecase"
)

func main() {
	log.Println("Starting CSV Job Processor...")

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create upload directory
	if err := os.MkdirAll(cfg.UploadDir, 0755); err != nil {
		log.Fatalf("Failed to create upload directory: %v", err)
	}

	// Run database migrations
	log.Println("Running database migrations...")
	if err := database.RunMigrations(cfg, "migrations"); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Create database connection pool
	dbConfig, err := pgxpool.ParseConfig(cfg.GetDSN())
	if err != nil {
		log.Fatalf("Failed to parse database config: %v", err)
	}

	dbConfig.MaxConns = 10
	dbConfig.MinConns = 2
	dbConfig.HealthCheckPeriod = 30 * time.Second

	db, err := pgxpool.NewWithConfig(context.Background(), dbConfig)
	if err != nil {
		log.Fatalf("Failed to create database connection pool: %v", err)
	}
	defer db.Close()

	// Test database connection
	if err := db.Ping(context.Background()); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	log.Println("Database connection established")

	// Create transaction manager
	txManager := postgres.NewPGTxManager(slog.Default(), db)

	// Create repositories
	jobRepo := postgres.NewJobRepository(txManager)

	// Create use cases
	csvUsecase := usecase.NewCSVImportUsecase(cfg.UploadDir)

	// Create web handlers
	jobHandler := web.NewJobHandler(jobRepo, cfg.UploadDir)

	// Create worker
	w := worker.NewWorker(
		db,
		jobRepo,
		worker.WithBatchSize(cfg.WorkerBatchSize),
		worker.WithPollInterval(cfg.WorkerPollInterval),
		worker.WithConcurrency(cfg.WorkerConcurrency),
	)

	// Register handlers
	w.RegisterHandler(domain.JobTypeCSVImport, csvUsecase.GetJobHandler())

	// Create HTTP server
	router := gin.Default()
	router.Use(gin.Recovery(), gin.Logger())

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Register job routes
	jobHandler.RegisterRoutes(router)

	// Start HTTP server
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.ServerPort),
		Handler: router,
	}

	// Context for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start worker in goroutine
	go func() {
		if err := w.Run(ctx); err != nil {
			log.Printf("Worker stopped with error: %v", err)
		}
	}()

	// Start reaper in goroutine
	go w.Reaper(ctx, 5*time.Minute, 15*time.Minute)

	// Start HTTP server
	go func() {
		log.Printf("HTTP server listening on port %d", cfg.ServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()

	// Graceful shutdown
	log.Println("Shutting down...")

	// Shutdown HTTP server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	log.Println("Shutdown complete")
}
