// Command api starts the Gin HTTP server, connects to PostgreSQL and Redis,
// wires middleware (recovery, security headers, body size limit, logging, rate limiting),
// registers all REST API routes, and blocks until SIGINT/SIGTERM.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Masralai/web-gobbler/internal/api"
	"github.com/Masralai/web-gobbler/internal/extract"
	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
)

func main() {
	ctx := context.Background()

	port := getEnv("PORT", "8080")
	databaseURL := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")
	defaultTimeout := getEnvInt("DEFAULT_TIMEOUT_SEC", 10)
	defaultRetries := getEnvInt("DEFAULT_MAX_RETRIES", 3)
	logLevel := getEnv("LOG_LEVEL", "INFO")

	if databaseURL == "" || redisURL == "" {
		slog.Error("DATABASE_URL and REDIS_URL must be set")
		os.Exit(1)
	}

	setupLogger(logLevel)

	slog.Info("starting API server", "port", port, "log_level", logLevel)

	db, err := store.New(ctx, databaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	slog.Info("connected to PostgreSQL")

	if err := db.Migrate(ctx); err != nil {
		slog.Error("failed to migrate database", "error", err)
		os.Exit(1)
	}
	slog.Info("database schema ready")

	q, err := queue.New(ctx, redisURL)
	if err != nil {
		slog.Error("failed to connect to redis", "error", err)
		os.Exit(1)
	}
	defer q.Close()
	slog.Info("connected to Redis")

	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(api.SecurityHeadersMiddleware())
	r.Use(api.BodySizeLimitMiddleware())
	r.Use(loggingMiddleware())

	ratePerSec := getEnvFloat("API_RATE_LIMIT", 1)
	rateBurst := getEnvInt("API_RATE_BURST", 10)
	rateLimiter := api.NewIPRateLimiter(rate.Limit(ratePerSec), rateBurst, 10*time.Minute)
	r.Use(api.RateLimitMiddleware(rateLimiter))

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	handler := api.NewHandler(db, q, defaultTimeout, defaultRetries)
	llmCfg := extract.FromEnv()
	if llmCfg.Enabled() {
		handler.WithExtractor(extract.NewClient(llmCfg, nil), true)
		slog.Info("LLM extract enabled", "model", llmCfg.Model, "base_url", llmCfg.BaseURL)
	}
	// ponytail: root /health for ALB/ECS/CI; /api/v1/health kept for API clients
	r.GET("/health", handler.HandleHealth)
	v1 := r.Group("/api/v1")
	handler.RegisterRoutes(v1)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("server exited")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var i int
		if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
			return f
		}
	}
	return fallback
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "DEBUG":
		l = slog.LevelDebug
	case "INFO":
		l = slog.LevelInfo
	case "WARN":
		l = slog.LevelWarn
	case "ERROR":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: l,
	})))
}

func loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()

		slog.Info("request",
			"method", method,
			"path", path,
			"status", status,
			"duration_ms", duration.Milliseconds(),
		)
	}
}
