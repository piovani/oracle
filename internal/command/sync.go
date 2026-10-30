// Package command holds one function per subcommand of the oracle binary.
package command

import (
	"flag"
	"log"

	"oracle/internal/b3"
	"oracle/internal/bootstrap"
	"oracle/internal/quote"
)

// Sync downloads the B3 quote files not yet in the database.
func Sync(args []string) {
	fs := flag.NewFlagSet("oracle sync", flag.ExitOnError)
	since := fs.Int("since", b3.FirstYear, "first year of history to load")
	_ = fs.Parse(args) // ExitOnError: a bad flag prints the usage and exits

	ctx, app := bootstrap.Start()
	defer app.Close()

	if err := quote.SyncHistory(ctx, app.DB, b3.NewClient(), *since); err != nil {
		app.Fatalf("sync failed: %v", err)
	}
	log.Println("quote history is up to date")
}
