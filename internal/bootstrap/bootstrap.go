// Package bootstrap starts what every command needs: configuration, database, the cache client and a context canceled on Ctrl+C.
package bootstrap

import (
	"context"
	"log"
	"os"
	"os/signal"

	"oracle/config"
	"oracle/internal/cache"
	"oracle/internal/telemetry"
	"oracle/pkg/db"

	"gorm.io/gorm"
)

type App struct {
	Config *config.Config
	DB     *gorm.DB
	Cache  *cache.Redis
	// telemetry flushes the traces and logs still buffered when the command ends.
	telemetry *telemetry.Telemetry
	stop      context.CancelFunc
}

// Start loads the config and connects to the database, exiting on failure.
// The context is canceled on Ctrl+C.
func Start() (context.Context, *App) {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	// First, so the database and Redis clients, and the first log lines, are already traced.
	tel := telemetry.Setup(ctx, telemetry.Options{Service: service(), Environment: cfg.Env, JSONLogs: telemetry.JSONLogs()})

	database, err := db.Connect(cfg.DB)
	if err != nil {
		log.Print("failed to connect to database: ", err)
		tel.Shutdown()
		os.Exit(1)
	}
	if sqlDB, err := database.DB(); err == nil {
		telemetry.RegisterDB(sqlDB)
	}
	return ctx, &App{Config: cfg, DB: database, Cache: cache.NewRedis(cfg.Redis), telemetry: tel, stop: stop}
}

// service names the process in traces and logs after its command: oracle-api, oracle-sync...
func service() string {
	if len(os.Args) > 1 {
		return "oracle-" + os.Args[1]
	}
	return "oracle"
}

// Fatalf is log.Fatalf that runs Close first, which a plain log.Fatalf would skip. The line is
// logged before Close, so it is among what the telemetry flushes.
func (a *App) Fatalf(format string, args ...any) {
	log.Printf(format, args...)
	a.Close()
	os.Exit(1)
}

func (a *App) Close() {
	a.stop()
	_ = a.Cache.Close()
	if sqlDB, err := a.DB.DB(); err == nil {
		_ = sqlDB.Close()
	}
	a.telemetry.Shutdown()
}
