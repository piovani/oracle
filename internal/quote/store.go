package quote

import (
	"context"
	"time"

	"oracle/internal/b3"
)

// Store keeps the quotes and the record of the B3 files already loaded.
// Postgres is the implementation the commands use; tests use the in-memory one in test/mock.
type Store interface {
	// Migrate creates or updates the tables.
	Migrate(ctx context.Context) error
	// SyncedFiles lists the B3 files already loaded.
	SyncedFiles(ctx context.Context) ([]string, error)
	// SaveFile runs load, storing every quote it passes to save, and records the file as synced.
	// It is all or nothing: when load or a write fails, nothing of the file is kept.
	// A quote already stored (same ticker and date) is left as it was. It returns how many quotes load passed.
	SaveFile(ctx context.Context, name string, load func(save func(Quote) error) error) (int, error)
	// History loads the stocks (specs ON, PN and UNT) quoted since the given day, by ticker and in date order.
	History(ctx context.Context, since time.Time) (map[string][]Quote, error)
}

// Source is where the B3 files come from; *b3.Client in production.
type Source interface {
	Fetch(ctx context.Context, file string, fn func(b3.Quote) error) error
}
