package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/Masralai/web-gobbler/internal/store"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL must be set")
		os.Exit(1)
	}
	ctx := context.Background()
	db, err := store.New(ctx, dbURL)
	if err != nil {
		slog.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		slog.Error("migrate", "error", err)
		os.Exit(1)
	}
	slog.Info("migrations applied")
}
