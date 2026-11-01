//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"oracle/internal/quote"

	"gorm.io/gorm"
)

const since = 2020

const (
	a2020 = "COTAHIST_A2020.ZIP"
	a2021 = "COTAHIST_A2021.ZIP"
)

// stored reads every quote back, ordered by ticker and date.
func stored(t *testing.T, db *gorm.DB) []quote.Quote {
	t.Helper()
	var rows []quote.Quote
	if err := db.Order("ticker, date").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func syncedFiles(t *testing.T, db *gorm.DB) map[string]int {
	t.Helper()
	var rows []quote.SyncedFile
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	files := make(map[string]int, len(rows))
	for _, f := range rows {
		files[f.Name] = f.Quotes
	}
	return files
}

func sameQuote(a, b quote.Quote) bool {
	a.Date, b.Date = a.Date.UTC(), b.Date.UTC()
	return a.Date.Equal(b.Date) && a.Ticker == b.Ticker && a.Name == b.Name && a.Spec == b.Spec && a.ISIN == b.ISIN &&
		a.Open == b.Open && a.High == b.High && a.Low == b.Low && a.Average == b.Average && a.Close == b.Close &&
		a.Trades == b.Trades && a.Quantity == b.Quantity && a.Volume == b.Volume
}

func TestSyncStoresRoundLotCashQuotes(t *testing.T) {
	db, _ := newDatabase(t)
	cogn := quote.Quote{
		Ticker: "COGN3", Date: day(2020, 10, 2), Name: "COGNA ON", Spec: "ON      NM", ISIN: "BRCOGNACNOR2",
		Open: 12345.67, High: 12399.99, Low: 12300.01, Average: 12350.5, Close: 12388.88,
		Trades: 29896, Quantity: 44274400, Volume: 123456789012.34,
	}
	fund := quote.Quote{Ticker: "XPML11", Date: day(2020, 10, 2), Name: "XP MALLS", Spec: "CI", ISIN: "BRXPMLCTF000", Open: 1, High: 1, Low: 1, Average: 1, Close: 1, Volume: 100}
	oddLot := cogn
	oddLot.Ticker = "COGN3F"
	option := cogn
	option.Ticker = "COGNJ250"

	fake, client := newFakeB3(t, map[string]file{a2020: {records: []string{
		stock(cogn), stock(fund), cotahist(oddLot, "96", "020"), cotahist(option, "82", "070"),
	}}})

	if err := quote.SyncHistory(t.Context(), db, client, since); err != nil {
		t.Fatal(err)
	}

	rows := stored(t, db)
	if len(rows) != 2 || !sameQuote(rows[0], cogn) || !sameQuote(rows[1], fund) {
		t.Fatalf("stored\n%+v\nwant COGN3 and XPML11 exactly as published", rows)
	}
	if got := syncedFiles(t, db); len(got) != 1 || got[a2020] != 2 {
		t.Errorf("synced files %v, want %s with 2 quotes", got, a2020)
	}
	if got := fake.fetched(); !slices.Equal(got, []string{a2020}) {
		t.Errorf("fetched %v", got)
	}
}

func TestSyncIsIncremental(t *testing.T) {
	db, _ := newDatabase(t)
	q := func(ticker string, d time.Time) quote.Quote {
		return quote.Quote{Ticker: ticker, Date: d, Spec: "ON", Open: 10, High: 11, Low: 9, Close: 10, Volume: 1000}
	}
	fake, client := newFakeB3(t, map[string]file{a2020: {records: []string{stock(q("AAA3", day(2020, 1, 2)))}}})

	for range 2 {
		if err := quote.SyncHistory(t.Context(), db, client, since); err != nil {
			t.Fatal(err)
		}
	}
	if got := fake.fetched(); !slices.Equal(got, []string{a2020}) {
		t.Fatalf("fetched %v, want %s once", got, a2020)
	}

	// B3 publishes a file that was missing: only that one is downloaded.
	fake.set(a2021, file{records: []string{stock(q("AAA3", day(2021, 1, 4)))}})
	if err := quote.SyncHistory(t.Context(), db, client, since); err != nil {
		t.Fatal(err)
	}
	if got := fake.fetched(); !slices.Equal(got, []string{a2020, a2021}) {
		t.Errorf("fetched %v, want %s only once more", got, a2021)
	}
	if rows := stored(t, db); len(rows) != 2 {
		t.Errorf("stored %d quotes, want 2", len(rows))
	}
}

func TestSyncKeepsTheFirstCopyOfAQuote(t *testing.T) {
	db, _ := newDatabase(t)
	first := quote.Quote{Ticker: "AAA3", Date: day(2020, 12, 30), Spec: "ON", Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 1000}
	again := first
	again.Close = 99

	_, client := newFakeB3(t, map[string]file{
		a2020: {records: []string{stock(first)}},
		a2021: {records: []string{stock(again)}},
	})
	if err := quote.SyncHistory(t.Context(), db, client, since); err != nil {
		t.Fatal(err)
	}
	if rows := stored(t, db); len(rows) != 1 || rows[0].Close != 10.5 {
		t.Errorf("stored %+v, want only the first copy", rows)
	}
}

func TestSyncLoadsLargeFiles(t *testing.T) {
	db, _ := newDatabase(t)
	bars := trend("BIG3", 2500, day(2020, 12, 31), nil, false)
	_, client := newFakeB3(t, map[string]file{a2020: {records: records(bars)}})

	if err := quote.SyncHistory(t.Context(), db, client, since); err != nil {
		t.Fatal(err)
	}
	rows := stored(t, db)
	if len(rows) != len(bars) || !sameQuote(rows[0], bars[0]) || !sameQuote(rows[len(rows)-1], bars[len(bars)-1]) {
		t.Errorf("stored %d quotes, want %d in order", len(rows), len(bars))
	}
}

func TestSyncRollsBackAFailedFile(t *testing.T) {
	db, _ := newDatabase(t)
	good := trend("AAA3", 10, day(2020, 6, 30), nil, false)
	// More than one batch is inserted before the corrupt record, so the rollback must undo inserts already sent.
	partial := records(trend("BBB3", 1500, day(2021, 6, 30), nil, false))
	corrupt := stock(good[0])
	corrupt = corrupt[:2] + "2021AB02" + corrupt[10:]

	fake, client := newFakeB3(t, map[string]file{
		a2020: {records: records(good)},
		a2021: {records: append(slices.Clone(partial), corrupt)},
	})
	err := quote.SyncHistory(t.Context(), db, client, since)
	if err == nil || !strings.Contains(err.Error(), a2021) {
		t.Fatalf("got %v, want an error on %s", err, a2021)
	}
	if rows := stored(t, db); len(rows) != len(good) {
		t.Errorf("stored %d quotes, want only the %d of the file that loaded", len(rows), len(good))
	}
	if got := syncedFiles(t, db); len(got) != 1 || got[a2020] == 0 {
		t.Errorf("synced files %v, want only %s", got, a2020)
	}

	// Once B3 fixes the file, the next sync loads it.
	fake.set(a2021, file{records: partial})
	if err := quote.SyncHistory(t.Context(), db, client, since); err != nil {
		t.Fatal(err)
	}
	if rows := stored(t, db); len(rows) != len(good)+len(partial) {
		t.Errorf("stored %d quotes after the retry, want %d", len(rows), len(good)+len(partial))
	}
}

func TestSyncStopsWhenB3Fails(t *testing.T) {
	db, _ := newDatabase(t)
	fake, client := newFakeB3(t, map[string]file{
		a2020: {status: http.StatusServiceUnavailable},
		a2021: {records: records(trend("AAA3", 5, day(2021, 1, 8), nil, false))},
	})
	if err := quote.SyncHistory(t.Context(), db, client, since); err == nil {
		t.Fatal("got no error")
	}
	if got := fake.fetched(); !slices.Equal(got, []string{a2020}) {
		t.Errorf("fetched %v, want to stop at the failure", got)
	}
	if rows := stored(t, db); len(rows) != 0 {
		t.Errorf("stored %d quotes, want none", len(rows))
	}
}

func TestHistoryLoadsStocksSinceADate(t *testing.T) {
	db, _ := newDatabase(t)
	if err := db.AutoMigrate(&quote.Quote{}, &quote.SyncedFile{}); err != nil {
		t.Fatal(err)
	}
	q := func(ticker, spec string, d time.Time) quote.Quote {
		return quote.Quote{Ticker: ticker, Spec: spec, Date: d, Open: 1, High: 1, Low: 1, Close: 1}
	}
	cutoff := day(2024, 1, 2)
	rows := []quote.Quote{
		q("AAA3", "ON      NM", cutoff.AddDate(0, 0, 2)), // inserted out of order on purpose
		q("AAA3", "ON      NM", cutoff),
		q("AAA3", "ON      NM", cutoff.AddDate(0, 0, -1)), // before the cutoff
		q("BBB4", "PN      N1", cutoff),
		q("CCC5", "PNA     N1", cutoff),
		q("DDD11", "UNT     N2", cutoff),
		q("XPML11", "CI", cutoff),     // real estate fund
		q("AAPL34", "DRN", cutoff),    // BDR
		q("BOVA11", "CI  ER", cutoff), // ETF
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	history, err := quote.History(t.Context(), db, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	tickers := make([]string, 0, len(history))
	for ticker := range history {
		tickers = append(tickers, ticker)
	}
	slices.Sort(tickers)
	if want := []string{"AAA3", "BBB4", "CCC5", "DDD11"}; !slices.Equal(tickers, want) {
		t.Errorf("tickers %v, want %v", tickers, want)
	}
	aaa := history["AAA3"]
	if len(aaa) != 2 || !aaa[0].Date.Equal(cutoff) || !aaa[1].Date.Equal(cutoff.AddDate(0, 0, 2)) {
		t.Errorf("AAA3 bars %+v, want the 2 on or after the cutoff, in date order", aaa)
	}
}
