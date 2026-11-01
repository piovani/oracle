package quote

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const batchSize = 1000

// Postgres is the Store in the database the commands connect to.
type Postgres struct {
	db *gorm.DB
}

func NewPostgres(db *gorm.DB) *Postgres {
	return &Postgres{db: db}
}

func (p *Postgres) Migrate(ctx context.Context) error {
	return p.db.WithContext(ctx).AutoMigrate(&Quote{}, &SyncedFile{})
}

func (p *Postgres) SyncedFiles(ctx context.Context) ([]string, error) {
	var names []string
	err := p.db.WithContext(ctx).Model(&SyncedFile{}).Pluck("name", &names).Error
	return names, err
}

// SaveFile writes the quotes in batches inside one transaction per file.
func (p *Postgres) SaveFile(ctx context.Context, name string, load func(save func(Quote) error) error) (int, error) {
	total := 0
	err := p.quiet(ctx).Transaction(func(tx *gorm.DB) error {
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

		err := load(func(q Quote) error {
			batch = append(batch, q)
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
		return tx.Create(&SyncedFile{Name: name, Quotes: total, SyncedAt: time.Now()}).Error
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func (p *Postgres) History(ctx context.Context, since time.Time) (map[string][]Quote, error) {
	var rows []Quote
	err := p.quiet(ctx).
		Where("date >= ? AND (spec LIKE 'ON%' OR spec LIKE 'PN%' OR spec LIKE 'UNT%')", since).
		Order("ticker, date").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}

	history := make(map[string][]Quote)
	for _, q := range rows {
		history[q.Ticker] = append(history[q.Ticker], q)
	}
	return history, nil
}

// quiet skips GORM's query log, which would print every batch of a sync.
func (p *Postgres) quiet(ctx context.Context) *gorm.DB {
	return p.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
}
