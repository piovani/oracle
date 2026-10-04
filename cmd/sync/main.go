package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"oracle/config"
	"oracle/internal/b3"
	"oracle/internal/quote"
	"oracle/pkg/db"
)

func main() {
	since := flag.Int("since", b3.FirstYear, "first year of history to load")
	flag.Parse()

	cfg := config.Load()

	database, err := db.Connect(cfg.DB)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := quote.SyncHistory(ctx, database, b3.NewClient(), *since); err != nil {
		log.Fatalf("sync failed: %v", err)
	}
	log.Println("quote history is up to date")
}
