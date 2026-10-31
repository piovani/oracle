package quote

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newMock opens GORM with the real Postgres dialect on top of a mocked connection.
// Every expectation must be met by the end of the test.
func newMock(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return db, mock
}

// sql matches a statement that starts with the given text.
func sql(prefix string) string { return "^" + regexp.QuoteMeta(prefix) }

var quoteColumns = []string{"ticker", "date", "name", "spec", "isin", "open", "high", "low", "average", "close", "trades", "quantity", "volume"}

const historySQL = `SELECT * FROM "quotes" WHERE date >= $1 AND (spec LIKE 'ON%' OR spec LIKE 'PN%' OR spec LIKE 'UNT%') ORDER BY ticker, date`

func TestHistory(t *testing.T) {
	db, mock := newMock(t)
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	d1, d2 := since, since.AddDate(0, 0, 1)
	mock.ExpectQuery(sql(historySQL)).WithArgs(since).WillReturnRows(sqlmock.NewRows(quoteColumns).
		AddRow("AAA3", d1, "AAA ON", "ON NM", "BRAAAACNOR1", 10, 11, 9, 10.5, 10.8, 100, 1000, 10800).
		AddRow("AAA3", d2, "AAA ON", "ON NM", "BRAAAACNOR1", 10.8, 12, 10, 11, 11.5, 120, 1100, 12650).
		AddRow("BBB4", d1, "BBB PN", "PN N1", "BRBBBBACNPR1", 5, 5, 5, 5, 5, 1, 10, 50))

	got, err := History(t.Context(), db, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got["AAA3"]) != 2 || len(got["BBB4"]) != 1 {
		t.Fatalf("got %+v", got)
	}
	if a := got["AAA3"]; !a[0].Date.Equal(d1) || !a[1].Date.Equal(d2) || a[1].Close != 11.5 || a[1].Volume != 12650 {
		t.Errorf("AAA3 bars out of order or wrong: %+v", a)
	}
	if b := got["BBB4"][0]; b.Ticker != "BBB4" || b.Spec != "PN N1" || b.ISIN != "BRBBBBACNPR1" || b.Trades != 1 || b.Quantity != 10 {
		t.Errorf("BBB4 bar %+v", b)
	}
}

func TestHistoryEmpty(t *testing.T) {
	db, mock := newMock(t)
	mock.ExpectQuery(sql(historySQL)).WillReturnRows(sqlmock.NewRows(quoteColumns))

	got, err := History(t.Context(), db, time.Now())
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %v, %v, want an empty map", got, err)
	}
}

func TestHistoryError(t *testing.T) {
	db, mock := newMock(t)
	fail := errors.New("connection lost")
	mock.ExpectQuery(sql(historySQL)).WillReturnError(fail)

	got, err := History(t.Context(), db, time.Now())
	if !errors.Is(err, fail) || got != nil {
		t.Errorf("got %v, %v, want the query error", got, err)
	}
}
