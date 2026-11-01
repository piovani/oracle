package mock

import (
	"context"

	"oracle/internal/b3"
	"oracle/internal/quote"
)

// Source serves B3 files from memory. A file missing from Files is not published yet, like a 404 from B3.
type Source struct {
	Files map[string][]b3.Quote
	// Err fails the download of a file after its quotes are sent, like a connection cut short.
	Err map[string]error

	Requested []string // files asked for, in order
}

var _ quote.Source = (*Source)(nil)

func (s *Source) Fetch(_ context.Context, file string, fn func(b3.Quote) error) error {
	s.Requested = append(s.Requested, file)
	quotes, ok := s.Files[file]
	if !ok && s.Err[file] == nil {
		return b3.ErrNotFound
	}
	for _, q := range quotes {
		if err := fn(q); err != nil {
			return err
		}
	}
	return s.Err[file]
}
