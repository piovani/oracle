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

// Start loads the config and connects to the database, exiting on failure.
// The context is canceled on Ctrl+C.
func Start() (context.Context, *App) {
	cfg := config.Load()

	database, err := db.Connect(cfg.DB)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	return ctx, &App{Config: cfg, DB: database, stop: stop}
}

// Fatalf is log.Fatalf that runs Close first, which a plain log.Fatalf would skip.
func (a *App) Fatalf(format string, args ...any) {
	a.Close()
	log.Fatalf(format, args...)
}

func (a *App) Close() {
	a.stop()
	if sqlDB, err := a.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
