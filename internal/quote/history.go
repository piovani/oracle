package quote

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func History(ctx context.Context, db *gorm.DB, since time.Time) (map[string][]Quote, error) {
	var rows []Quote
	err := db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard}).
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
