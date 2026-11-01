package quote

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"oracle/internal/b3"
)

// SyncHistory loads into the store every B3 file since sinceYear that it does not have yet.
// Files B3 has not published (holidays, weekends, today before the close) are skipped and tried again next time.
func SyncHistory(ctx context.Context, store Store, source Source, sinceYear int) error {
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	synced, err := store.SyncedFiles(ctx)
	if err != nil {
		return err
	}
	done := make(map[string]bool, len(synced))
	for _, name := range synced {
		done[name] = true
	}

	for _, file := range filesUntil(sinceYear, today()) {
		if done[file] {
			continue
		}
		n, err := store.SaveFile(ctx, file, func(save func(Quote) error) error {
			return source.Fetch(ctx, file, func(q b3.Quote) error { return save(fromB3(q)) })
		})
		if errors.Is(err, b3.ErrNotFound) {
			log.Printf("%s: not published by B3, skipping", file)
			continue
		}
		if err != nil {
			return fmt.Errorf("sync %s: %w", file, err)
		}
		log.Printf("%s: %d quotes", file, n)
	}
	return nil
}

func fromB3(q b3.Quote) Quote {
	return Quote{
		Ticker:   q.Ticker,
		Date:     q.Date,
		Name:     q.Name,
		Spec:     q.Spec,
		ISIN:     q.ISIN,
		Open:     q.Open,
		High:     q.High,
		Low:      q.Low,
		Average:  q.Average,
		Close:    q.Close,
		Trades:   q.Trades,
		Quantity: q.Quantity,
		Volume:   q.Volume,
	}
}

func filesUntil(sinceYear int, day time.Time) []string {
	if sinceYear < b3.FirstYear {
		sinceYear = b3.FirstYear
	}

	var files []string
	for y := sinceYear; y < day.Year(); y++ {
		files = append(files, b3.AnnualFile(y))
	}
	if sinceYear > day.Year() {
		return files
	}
	for m := time.January; m < day.Month(); m++ {
		files = append(files, b3.MonthlyFile(day.Year(), m))
	}
	for d := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC); !d.After(day); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		files = append(files, b3.DailyFile(d))
	}
	return files
}

// now is the clock, replaced in tests.
var now = time.Now

func today() time.Time {
	now := now()
	if loc, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		now = now.In(loc)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
