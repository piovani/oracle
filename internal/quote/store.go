package quote

import (
	"context"
	"errors"
	"strings"
	"time"

	"oracle/internal/b3"
)

// Store keeps the quotes and the record of the B3 files already loaded.
// Postgres is the implementation the commands use; tests use the in-memory one in test/mock.
type Store interface {
	Migrate(ctx context.Context) error
	SyncedFiles(ctx context.Context) ([]string, error)
	// SaveFile stores every quote load passes to save and records the file as synced.
	// All or nothing. A quote already stored (same ticker and date) is kept. Returns how many quotes load passed.
	SaveFile(ctx context.Context, name string, load func(save func(Quote) error) error) (int, error)
	// History loads stocks only (ON, PN, UNT) since the given day, in date order.
	History(ctx context.Context, since time.Time) (map[string][]Quote, error)
	// Tickers, Ticker, TickerQuotes and Session cover every spec, not only stocks.
	Tickers(ctx context.Context) ([]Ticker, error)
	Ticker(ctx context.Context, ticker string) (Ticker, error)
	// TickerQuotes includes both days; a zero day leaves that end open.
	TickerQuotes(ctx context.Context, ticker string, from, to time.Time) ([]Quote, error)
	Session(ctx context.Context, day time.Time) ([]Quote, error)
	LatestSession(ctx context.Context) (time.Time, error)
}

// Ticker sums up a ticker; Name, Spec and ISIN come from its latest quote.
type Ticker struct {
	Ticker   string
	Name     string
	Spec     string
	ISIN     string `gorm:"column:isin"`
	First    time.Time
	Last     time.Time
	Sessions int
}

var ErrUnknownTicker = errors.New("unknown ticker")

func IsStock(spec string) bool {
	return strings.HasPrefix(spec, "ON") || strings.HasPrefix(spec, "PN") || strings.HasPrefix(spec, "UNT")
}

func LastSession(history map[string][]Quote) time.Time {
	var latest time.Time
	for _, bars := range history {
		if len(bars) > 0 && bars[len(bars)-1].Date.After(latest) {
			latest = bars[len(bars)-1].Date
		}
	}
	return latest
}

// StaleAfter: older than a weekend plus a holiday means a sync is missing.
const StaleAfter = 4 * 24 * time.Hour

// Source is where the B3 files come from; *b3.Client in production.
type Source interface {
	Fetch(ctx context.Context, file string, fn func(b3.Quote) error) error
}
