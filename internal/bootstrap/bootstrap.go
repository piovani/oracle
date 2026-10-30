// Package bootstrap starts what every command needs: configuration, database and a context canceled on Ctrl+C.
package bootstrap

import (
	"context"
	"log"
	"os"
	"os/signal"

	"oracle/config"
	"oracle/pkg/db"

	"gorm.io/gorm"
)

type App struct {
	Config *config.Config
	DB     *gorm.DB
	stop   context.CancelFunc
}

// Start loads the configuration and connects to the database, exiting on failure.
// The returned context is canceled when the user presses Ctrl+C.
func Start() (context.Context, *App) {
	cfg := config.Load()

	database, err := db.Connect(cfg.DB)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	return ctx, &App{Config: cfg, DB: database, stop: stop}
}

// Close releases the database connection and the Ctrl+C handler.
func (a *App) Close() {
	a.stop()
	if sqlDB, err := a.DB.DB(); err == nil {
		sqlDB.Close()
	}
}
