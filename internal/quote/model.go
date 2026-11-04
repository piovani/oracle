package quote

import "time"

// Quote is one trading day of one ticker. The schema is not derived from these tags: it lives in
// the SQL files of migrations/, and the tags only tell GORM how to read and write the columns.
type Quote struct {
	Ticker   string    `gorm:"primaryKey;size:12"`
	Date     time.Time `gorm:"primaryKey;type:date"`
	Name     string    `gorm:"size:12"`
	Spec     string    `gorm:"size:10"`
	ISIN     string    `gorm:"column:isin;size:12"`
	Open     float64   `gorm:"type:numeric(15,2)"`
	High     float64   `gorm:"type:numeric(15,2)"`
	Low      float64   `gorm:"type:numeric(15,2)"`
	Average  float64   `gorm:"type:numeric(15,2)"`
	Close    float64   `gorm:"type:numeric(15,2)"`
	Trades   int64
	Quantity int64
	Volume   float64 `gorm:"type:numeric(20,2)"`
}

// SyncedFile records a B3 file whose quotes are already in the database.
type SyncedFile struct {
	Name     string `gorm:"primaryKey;size:32"`
	Quotes   int
	SyncedAt time.Time
}
