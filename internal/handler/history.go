package handler

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"oracle/internal/quote"
	"oracle/internal/telemetry"
)

const (
	historyEntries     = 3 // 3 years of every stock is ~600k quotes
	historyLoadTimeout = 30 * time.Second
)

type HistoryReader interface {
	History(ctx context.Context, since time.Time) (map[string][]quote.Quote, error)
	LatestSession(ctx context.Context) (time.Time, error)
}

// Histories keeps the stock history until a new session is stored, because loading it takes
// seconds. The maps are shared between requests, so callers must not change them.
type Histories struct {
	store HistoryReader
	now   func() time.Time

	mu      sync.Mutex
	entries map[time.Time]*history // by first day loaded
	order   []time.Time            // oldest use first
}

type history struct {
	stored time.Time // latest session in the database when loaded
	done   chan struct{}
	quotes map[string][]quote.Quote
	latest time.Time
	err    error
}

func NewHistories(store HistoryReader, now func() time.Time) *Histories {
	return &Histories{store: store, now: now, entries: make(map[time.Time]*history)}
}

func (c *Histories) Load(ctx context.Context, years int) (map[string][]quote.Quote, time.Time, error) {
	stored, err := c.store.LatestSession(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	y, m, d := c.now().AddDate(-years, 0, 0).Date()
	since := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)

	h := c.entry(ctx, since, stored)
	select {
	case <-h.done:
		return h.quotes, h.latest, h.err
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}
}

func (c *Histories) entry(ctx context.Context, since, stored time.Time) *history {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.entries[since]; ok && h.stored.Equal(stored) {
		c.touch(since)
		return h
	}

	h := &history{stored: stored, done: make(chan struct{})}
	c.entries[since] = h
	c.touch(since)
	if len(c.order) > historyEntries {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	go c.load(context.WithoutCancel(ctx), h, since)
	return h
}

// load doesn't use the request's cancellation: other requests may be waiting for it.
func (c *Histories) load(ctx context.Context, h *history, since time.Time) {
	ctx, cancel := context.WithTimeout(ctx, historyLoadTimeout)
	defer cancel()
	ctx, done := telemetry.Operation(ctx, "history_load")
	h.quotes, h.err = c.store.History(ctx, since)
	done(h.err)
	h.latest = quote.LastSession(h.quotes)
	close(h.done)

	if h.err != nil {
		c.mu.Lock()
		if c.entries[since] == h {
			delete(c.entries, since)
			c.order = slices.DeleteFunc(c.order, func(d time.Time) bool { return d.Equal(since) })
		}
		c.mu.Unlock()
	}
}

func (c *Histories) touch(since time.Time) {
	c.order = slices.DeleteFunc(c.order, func(d time.Time) bool { return d.Equal(since) })
	c.order = append(c.order, since)
}

func loadHistory(w http.ResponseWriter, r *http.Request, histories *Histories, years int) (map[string][]quote.Quote, time.Time, bool) {
	history, latest, err := histories.Load(r.Context(), years)
	if err != nil {
		writeInternal(w, "load history", err)
		return nil, time.Time{}, false
	}
	if latest.IsZero() {
		writeError(w, http.StatusNotFound, "no stock quotes stored in the period, run oracle sync")
		return nil, time.Time{}, false
	}
	return history, latest, true
}
