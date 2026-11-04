// Package command holds one function per subcommand of the oracle binary.
package command

import (
	"context"
	"flag"
	"log"
	"time"

	"oracle/internal/b3"
	"oracle/internal/bootstrap"
	"oracle/internal/cache"
	"oracle/internal/quote"
)

// Sync downloads the B3 quote files not yet in the database.
func Sync(args []string) {
	fs := flag.NewFlagSet("oracle sync", flag.ExitOnError)
	since := fs.Int("since", b3.FirstYear, "first year of history to load")
	_ = fs.Parse(args) // ExitOnError: a bad flag prints the usage and exits

	ctx, app := bootstrap.Start()
	defer app.Close()

	err := quote.SyncHistory(ctx, quote.NewPostgres(app.DB), b3.NewClient(), *since)
	// Files stored before a failure are new data too, so the cache is emptied either way.
	flushCache(app.Cache)
	if err != nil {
		app.Fatalf("sync failed: %v", err)
	}
	log.Println("quote history is up to date")
}

// flushCache drops every cached answer, so the API builds new ones from the new quotes. Ctrl+C
// during the sync must not skip it. A Redis that cannot be reached is only a warning: what the API
// still holds expires by itself within cacheTTL.
func flushCache(c cache.Cache) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		log.Printf("cache not emptied: %v", err)
		return
	}
	log.Println("cache emptied")
}
