package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pcristin/golang_contest/internal/api"
	"github.com/pcristin/golang_contest/internal/config"
	"github.com/pcristin/golang_contest/internal/database"
	myLogger "github.com/pcristin/golang_contest/internal/logger"
)

func main() {
	// Initialize context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := config.NewConfig()
	config.ParseFlags()

	// Parse log level
	var logLevel slog.Level
	switch strings.ToLower(config.GetLogLevel()) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	// Set up slog with JSON handler and level
	opts := slog.HandlerOptions{
		Level: logLevel,
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &opts))
	slog.SetDefault(logger)

	logger.Info("config | config initialized", "config", config)

	// Initialize Redis
	redis := database.NewRedisClient(ctx, config.RedisURL)
	// Fail fast if Redis is not connected
	if err := redis.HealthCheck(ctx); err != nil {
		logger.Error("redis | failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	// Initialize Postgres
	postgres, err := database.NewPostgresClient(ctx, config.PostgresURL)
	if err != nil {
		logger.Error("postgres | failed to connect to Postgres", "error", err)
		os.Exit(1)
	}
	defer postgres.Close()

	// Fail fast if Postgres is not connected
	if err := postgres.HealthCheck(); err != nil {
		logger.Error("postgres | failed to connect to Postgres", "error", err)
		os.Exit(1)
	}

	// Create schema
	if err := postgres.CreateTables(); err != nil {
		logger.Error("postgres | failed to create tables", "error", err)
		os.Exit(1)
	}

	// Initialize router
	router := chi.NewRouter()

	// Initialize handler
	handler := api.NewHandler(config, redis, postgres)

	// Start background workers
	wg := sync.WaitGroup{}
	wg.Add(4)
	go func() {
		defer wg.Done()
		workerCtx := context.WithValue(ctx, myLogger.SourceKey, "checkout_worker")
		handler.ProcessCheckoutAttempts(workerCtx)
	}()

	go func() {
		defer wg.Done()
		workerCtx := context.WithValue(ctx, myLogger.SourceKey, "expired_checkouts_worker")
		handler.ProcessExpiredCheckouts(workerCtx)
	}()

	go func() {
		defer wg.Done()
		workerCtx := context.WithValue(ctx, myLogger.SourceKey, "sale_scheduler")
		handler.StartSaleScheduler(workerCtx)
	}()

	go func() {
		defer wg.Done()
		workerCtx := context.WithValue(ctx, myLogger.SourceKey, "purchase_worker")
		handler.ProcessPurchaseInserts(workerCtx)
	}()

	// Add routes
	router.Get("/health", handler.Health)
	router.Post("/checkout", handler.Checkout)
	router.Post("/purchase", handler.Purchase)

	// Graceful shutdown
	// Initialize server
	server := &http.Server{
		Addr:           ":" + config.GetPort(),
		Handler:        router,
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}

	// Channel for notification the main goroutine that connections are closed
	idleConnsClosed := make(chan struct{})

	// Channel to notify about server shutdown
	sigint := make(chan os.Signal, 1)

	// Register the channel to receive SIGINT, SIGTERM and SIGQUIT signals
	signal.Notify(sigint, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Start a separate goroutine to handle the signal
	go func() {
		<-sigint
		logger.Info("Shutting down server...")

		// Create channel to signal when shutdown is complete
		shutdownComplete := make(chan struct{})

		go func() {
			// Step 1
			cancel() // Stop workers

			// Step 2 - Wait for workers to finish
			wg.Wait()
			logger.Info("server | workers finished")

			// Step 3 - Shutdown server
			if err := server.Shutdown(context.Background()); err != nil {
				logger.Error("server error | could not shutdown server", "error", err)
			}
			logger.Info("server | HTTP server shutdown completed")

			// Step 4 - Close shutdown complete channel
			close(shutdownComplete)
		}()

		select {
		case <-shutdownComplete:
			logger.Info("server | graceful shutdown completed")
		case <-time.After(30 * time.Second):
			logger.Warn("server | graceful shutdown timed out (30 seconds)")
			logger.Warn("server | WARNING: some operations may not been completed cleanly")
		}

		close(idleConnsClosed)
	}()

	// Start the server in a goroutine to allow graceful shutdown
	go func() {
		logger.Info("server | running on port", "port", config.GetPort())
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error | could not listen on port", "port", config.GetPort(), "error", err)
			// Signal shutdown if server fails to start
			sigint <- syscall.SIGTERM
		}
	}()

	// Wait for idle connections to be closed
	<-idleConnsClosed

	logger.Info("server | server stopped")
}
