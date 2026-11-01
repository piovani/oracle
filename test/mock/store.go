package mock

import (
	"context"
	"slices"
	"strings"
	"time"

	"oracle/internal/quote"
)

// Store is an in-memory quote.Store that honors its contract: a file is saved all or nothing,
// and a quote already stored is kept.
type Store struct {
	Quotes []quote.Quote // stored, in the order they were saved
	Synced []string      // files recorded as synced

	MigrateErr     error
	SyncedFilesErr error
	HistoryErr     error
	// SaveErr fails SaveFile for a file after load has run, like a failed commit.
	SaveErr map[string]error

	Calls []string // methods called, in order; SaveFile also has the file name, as in "SaveFile COTAHIST_A2020.ZIP"
}

var _ quote.Store = (*Store)(nil)

func (s *Store) Migrate(context.Context) error {
	s.Calls = append(s.Calls, "Migrate")
	return s.MigrateErr
}

func (s *Store) SyncedFiles(context.Context) ([]string, error) {
	s.Calls = append(s.Calls, "SyncedFiles")
	if s.SyncedFilesErr != nil {
		return nil, s.SyncedFilesErr
	}
	return slices.Clone(s.Synced), nil
}

func (s *Store) SaveFile(_ context.Context, name string, load func(save func(quote.Quote) error) error) (int, error) {
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
	s.Calls = append(s.Calls, "History")
	if s.HistoryErr != nil {
		return nil, s.HistoryErr
	}
	history := make(map[string][]quote.Quote)
	for _, q := range s.Quotes {
		if !q.Date.Before(since) && isStock(q.Spec) {
			history[q.Ticker] = append(history[q.Ticker], q)
		}
	}
	for _, bars := range history {
		slices.SortFunc(bars, func(a, b quote.Quote) int { return a.Date.Compare(b.Date) })
	}
	return history, nil
}

func (s *Store) has(q quote.Quote) bool {
	return slices.ContainsFunc(s.Quotes, func(o quote.Quote) bool { return o.Ticker == q.Ticker && o.Date.Equal(q.Date) })
}

func isStock(spec string) bool {
	return strings.HasPrefix(spec, "ON") || strings.HasPrefix(spec, "PN") || strings.HasPrefix(spec, "UNT")
}
