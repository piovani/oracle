package quote

import (
	"context"
	"database/sql"
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

func (p *Postgres) Tickers(ctx context.Context) ([]Ticker, error) {
	return p.tickers(ctx, "")
}

func (p *Postgres) Ticker(ctx context.Context, ticker string) (Ticker, error) {
	found, err := p.tickers(ctx, ticker)
	if err != nil {
		return Ticker{}, err
	}
	if len(found) == 0 {
		return Ticker{}, ErrUnknownTicker
	}
	return found[0], nil
}

// tickers summarizes every ticker, or only the one given, taking the descriptive columns from its latest quote.
func (p *Postgres) tickers(ctx context.Context, only string) ([]Ticker, error) {
	span := p.quiet(ctx).Model(&Quote{}).
		Select("ticker, MIN(date) AS first, MAX(date) AS last, COUNT(*) AS sessions").
		Group("ticker")
	if only != "" {
		span = span.Where("ticker = ?", only)
	}
	tickers := []Ticker{}
	err := p.quiet(ctx).Table("(?) AS s", span).
		Select("q.ticker, q.name, q.spec, q.isin, s.first, s.last, s.sessions").
		Joins("JOIN quotes q ON q.ticker = s.ticker AND q.date = s.last").
		Order("q.ticker").
		Scan(&tickers).Error
	return tickers, err
}

// Days are passed as YYYY-MM-DD text so Postgres compares them as dates, whatever the session time zone.
func (p *Postgres) TickerQuotes(ctx context.Context, ticker string, from, to time.Time) ([]Quote, error) {
	query := p.quiet(ctx).Where("ticker = ?", ticker)
	if !from.IsZero() {
		query = query.Where("date >= ?", from.Format(time.DateOnly))
	}
	if !to.IsZero() {
		query = query.Where("date <= ?", to.Format(time.DateOnly))
	}
	quotes := []Quote{}
	err := query.Order("date").Find(&quotes).Error
	return quotes, err
}

func (p *Postgres) Session(ctx context.Context, day time.Time) ([]Quote, error) {
	quotes := []Quote{}
	err := p.quiet(ctx).Where("date = ?", day.Format(time.DateOnly)).Order("ticker").Find(&quotes).Error
	return quotes, err
}

func (p *Postgres) LatestSession(ctx context.Context) (time.Time, error) {
	var latest sql.NullTime
	if err := p.quiet(ctx).Model(&Quote{}).Select("MAX(date)").Row().Scan(&latest); err != nil {
		return time.Time{}, err
	}
	return latest.Time, nil
}

// quiet skips GORM's query log, which would print every batch of a sync.
func (p *Postgres) quiet(ctx context.Context) *gorm.DB {
	return p.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
}
