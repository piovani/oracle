//go:build integration

package integration

import (
	"slices"
	"testing"
	"time"

	"oracle/internal/quote"

	"gorm.io/gorm"
)

// legacyQuote is the model as it was when GORM's AutoMigrate created the schema.
type legacyQuote struct {
	Ticker   string    `gorm:"primaryKey;size:12"`
	Date     time.Time `gorm:"primaryKey;type:date;index"`
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

func (legacyQuote) TableName() string { return "quotes" }

func indexes(t *testing.T, conn *gorm.DB) []string {
	t.Helper()
	var names []string
	if err := conn.Raw("SELECT indexname FROM pg_indexes WHERE schemaname = 'public' ORDER BY indexname").Scan(&names).Error; err != nil {
		t.Fatal(err)
	}
	return names
}

func versions(t *testing.T, conn *gorm.DB) []string {
	t.Helper()
	var v []string
	if err := conn.Raw("SELECT version FROM schema_migrations ORDER BY version").Scan(&v).Error; err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrateBuildsTheSchemaFromScratch(t *testing.T) {
	t.Parallel()
	conn, _ := emptyDatabase(t)

	if err := quote.NewPostgres(conn).Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{"idx_quotes_date", "quotes_pkey", "schema_migrations_pkey", "synced_files_pkey"}
	if got := indexes(t, conn); !slices.Equal(got, want) {
		t.Errorf("indexes = %v, want %v", got, want)
	}
	if got := versions(t, conn); !slices.Equal(got, []string{"0001_create_tables", "0002_index_quotes_date"}) {
		t.Errorf("recorded versions = %v", got)
	}
}

func TestMigrateRunsOnlyOnce(t *testing.T) {
	t.Parallel()
	conn, _ := emptyDatabase(t)
	store := quote.NewPostgres(conn)

	for range 3 {
		if err := store.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if got := versions(t, conn); len(got) != 2 {
		t.Errorf("recorded versions = %v, want each migration once", got)
	}
}

// A database created before migrations existed (by GORM's AutoMigrate) has the tables and the
// date index but no schema_migrations: the first Migrate must adopt it and keep its data.
func TestMigrateAdoptsADatabaseWithoutMigrations(t *testing.T) {
	t.Parallel()
	conn, _ := emptyDatabase(t)
	if err := conn.AutoMigrate(&legacyQuote{}, &quote.SyncedFile{}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&legacyQuote{Ticker: "PETR4", Date: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), Close: 38.2}).Error; err != nil {
		t.Fatal(err)
	}

	if err := quote.NewPostgres(conn).Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, conn); len(got) != 2 {
		t.Errorf("recorded versions = %v", got)
	}
	if rows := stored(t, conn); len(rows) != 1 || rows[0].Ticker != "PETR4" {
		t.Errorf("data after adopting = %+v", rows)
	}
}

func TestMigrateSurvivesConcurrentStarts(t *testing.T) {
	t.Parallel()
	conn, _ := emptyDatabase(t)
	store := quote.NewPostgres(conn)

	errs := make(chan error, 5)
	for range cap(errs) {
		go func() { errs <- store.Migrate(t.Context()) }()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
