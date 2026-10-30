package main

import (
	"flag"
	"log"

	"oracle/internal/b3"
	"oracle/internal/bootstrap"
	"oracle/internal/quote"
)

func main() {
	since := flag.Int("since", b3.FirstYear, "first year of history to load")
	flag.Parse()

	ctx, app := bootstrap.Start()
	defer app.Close()

	if err := quote.SyncHistory(ctx, app.DB, b3.NewClient(), *since); err != nil {
		app.Fatalf("sync failed: %v", err)
	}
	log.Println("quote history is up to date")
}
