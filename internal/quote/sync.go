package quote

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"oracle/internal/b3"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const batchSize = 1000

func SyncHistory(ctx context.Context, db *gorm.DB, client *b3.Client, sinceYear int) error {
	if err := db.AutoMigrate(&Quote{}, &SyncedFile{}); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	var synced []string
	if err := db.WithContext(ctx).Model(&SyncedFile{}).Pluck("name", &synced).Error; err != nil {
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
		n, err := syncFile(ctx, db, client, file)
		if errors.Is(err, b3.ErrNotFound) {
			log.Printf("%s: not published by B3, skipping", file)
			continue
		}
		if err != nil {
			return err
		}
		log.Printf("%s: %d quotes", file, n)
	}
	return nil
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

func syncFile(ctx context.Context, db *gorm.DB, client *b3.Client, file string) (int, error) {
	total := 0
	err := db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		insert := tx.Clauses(clause.OnConflict{DoNothing: true})
		batch := make([]Quote, 0, batchSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if err := insert.Create(&batch).Error; err != nil {
				return err
			}
			total += len(batch)
			batch = batch[:0]
			return nil
		}

		err := client.Fetch(ctx, file, func(q b3.Quote) error {
			batch = append(batch, Quote{
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
			})
			if len(batch) == batchSize {
				return flush()
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := flush(); err != nil {
			return err
		}
		return tx.Create(&SyncedFile{Name: file, Quotes: total, SyncedAt: time.Now()}).Error
	})
	if err != nil {
		return 0, fmt.Errorf("sync %s: %w", file, err)
	}
	return total, nil
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
