package mock

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"oracle/internal/quote"
)

// Store is an in-memory quote.Store that honors its contract: a file is saved all or nothing,
// and a quote already stored is kept. It is safe for concurrent use, like Postgres.
type Store struct {
	mu sync.Mutex

	Quotes []quote.Quote // stored, in the order they were saved
	Synced []string      // files recorded as synced

	MigrateErr     error
	SyncedFilesErr error
	HistoryErr     error
	// ReadErr fails every read but History and SyncedFiles.
	ReadErr error
	// SaveErr fails SaveFile for a file after load has run, like a failed commit.
	SaveErr map[string]error

	Calls []string // methods called, in order; SaveFile also has the file name, as in "SaveFile COTAHIST_A2020.ZIP"
}

var _ quote.Store = (*Store)(nil)

func (s *Store) Migrate(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "Migrate")
	return s.MigrateErr
}

func (s *Store) SyncedFiles(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "SyncedFiles")
	if s.SyncedFilesErr != nil {
		return nil, s.SyncedFilesErr
	}
	return slices.Clone(s.Synced), nil
}

func (s *Store) SaveFile(_ context.Context, name string, load func(save func(quote.Quote) error) error) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "SaveFile "+name)
	var pending []quote.Quote
	err := load(func(q quote.Quote) error {
		pending = append(pending, q)
		return nil
	})
	if err == nil {
		err = s.SaveErr[name]
	}
	if err != nil {
		return 0, err
	}
	for _, q := range pending {
		if !s.has(q) {
			s.Quotes = append(s.Quotes, q)
		}
	}
	s.Synced = append(s.Synced, name)
	return len(pending), nil
}

func (s *Store) History(_ context.Context, since time.Time) (map[string][]quote.Quote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "History")
	if s.HistoryErr != nil {
		return nil, s.HistoryErr
	}
	history := make(map[string][]quote.Quote)
	for _, q := range s.Quotes {
		if !q.Date.Before(since) && quote.IsStock(q.Spec) {
			history[q.Ticker] = append(history[q.Ticker], q)
		}
	}
	for _, bars := range history {
		slices.SortFunc(bars, func(a, b quote.Quote) int { return a.Date.Compare(b.Date) })
	}
	return history, nil
}

func (s *Store) Tickers(context.Context) ([]quote.Ticker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "Tickers")
	return s.tickers()
}

func (s *Store) tickers() ([]quote.Ticker, error) {
	if s.ReadErr != nil {
		return nil, s.ReadErr
	}
	byTicker := make(map[string]*quote.Ticker)
	latest := make(map[string]quote.Quote)
	for _, q := range s.Quotes {
		t, ok := byTicker[q.Ticker]
		if !ok {
			t = &quote.Ticker{Ticker: q.Ticker, First: q.Date, Last: q.Date}
			byTicker[q.Ticker] = t
		}
		t.Sessions++
		if q.Date.Before(t.First) {
			t.First = q.Date
		}
		if l, seen := latest[q.Ticker]; !seen || q.Date.After(l.Date) {
			latest[q.Ticker] = q
			t.Last = q.Date
		}
	}
	tickers := make([]quote.Ticker, 0, len(byTicker))
	for ticker, t := range byTicker {
		l := latest[ticker]
		t.Name, t.Spec, t.ISIN = l.Name, l.Spec, l.ISIN
		tickers = append(tickers, *t)
	}
	slices.SortFunc(tickers, func(a, b quote.Ticker) int { return strings.Compare(a.Ticker, b.Ticker) })
	return tickers, nil
}

func (s *Store) Ticker(_ context.Context, ticker string) (quote.Ticker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "Ticker")
	tickers, err := s.tickers()
	if err != nil {
		return quote.Ticker{}, err
	}
	for _, t := range tickers {
		if t.Ticker == ticker {
			return t, nil
		}
	}
	return quote.Ticker{}, quote.ErrUnknownTicker
}

func (s *Store) TickerQuotes(_ context.Context, ticker string, from, to time.Time) ([]quote.Quote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "TickerQuotes")
	if s.ReadErr != nil {
		return nil, s.ReadErr
	}
	quotes := []quote.Quote{}
	for _, q := range s.Quotes {
		if q.Ticker == ticker && (from.IsZero() || !q.Date.Before(from)) && (to.IsZero() || !q.Date.After(to)) {
			quotes = append(quotes, q)
		}
	}
	slices.SortFunc(quotes, func(a, b quote.Quote) int { return a.Date.Compare(b.Date) })
	return quotes, nil
}

func (s *Store) Session(_ context.Context, day time.Time) ([]quote.Quote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "Session")
	if s.ReadErr != nil {
		return nil, s.ReadErr
	}
	quotes := []quote.Quote{}
	for _, q := range s.Quotes {
		if q.Date.Equal(day) {
			quotes = append(quotes, q)
		}
	}
	slices.SortFunc(quotes, func(a, b quote.Quote) int { return strings.Compare(a.Ticker, b.Ticker) })
	return quotes, nil
}

func (s *Store) LatestSession(context.Context) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, "LatestSession")
	if s.ReadErr != nil {
		return time.Time{}, s.ReadErr
	}
	var latest time.Time
	for _, q := range s.Quotes {
		if q.Date.After(latest) {
			latest = q.Date
		}
	}
	return latest, nil
}

func (s *Store) has(q quote.Quote) bool {
	return slices.ContainsFunc(s.Quotes, func(o quote.Quote) bool { return o.Ticker == q.Ticker && o.Date.Equal(q.Date) })
}
