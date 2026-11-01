package quote_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"oracle/internal/b3"
	"oracle/internal/quote"
	"oracle/test/mock"
)

// Today is 2026-01-02, a Friday, so a sync since 2026 needs two daily files.
const (
	jan1 = "COTAHIST_D01012026.ZIP"
	jan2 = "COTAHIST_D02012026.ZIP"
)

func fixedClock(t *testing.T) {
	t.Helper()
	quote.SetNow(t, func() time.Time { return time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC) })
}

var cogn3 = b3.Quote{
	Date: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Ticker: "COGN3", Name: "COGNA ON", Spec: "ON      NM", ISIN: "BRCOGNACNOR2",
	Open: 2.38, High: 2.45, Low: 2.35, Average: 2.40, Close: 2.43, Trades: 29896, Quantity: 44274400, Volume: 106261836,
}

func TestSyncHistory(t *testing.T) {
	fixedClock(t)
	store := &mock.Store{}
	source := &mock.Source{Files: map[string][]b3.Quote{jan2: {cogn3}}} // jan1 is a holiday: not published

	if err := quote.SyncHistory(t.Context(), store, source, 2026); err != nil {
		t.Fatal(err)
	}

	if want := []string{"Migrate", "SyncedFiles", "SaveFile " + jan1, "SaveFile " + jan2}; !slices.Equal(store.Calls, want) {
		t.Errorf("calls %v, want %v", store.Calls, want)
	}
	if !slices.Equal(source.Requested, []string{jan1, jan2}) {
		t.Errorf("requested %v", source.Requested)
	}
	if !slices.Equal(store.Synced, []string{jan2}) {
		t.Errorf("synced %v, want only the published file", store.Synced)
	}
	want := quote.Quote{
		Ticker: "COGN3", Date: cogn3.Date, Name: "COGNA ON", Spec: "ON      NM", ISIN: "BRCOGNACNOR2",
		Open: 2.38, High: 2.45, Low: 2.35, Average: 2.40, Close: 2.43, Trades: 29896, Quantity: 44274400, Volume: 106261836,
	}
	if len(store.Quotes) != 1 || store.Quotes[0] != want {
		t.Errorf("stored %+v, want %+v", store.Quotes, want)
	}
}

func TestSyncHistorySkipsSyncedFiles(t *testing.T) {
	fixedClock(t)
	store := &mock.Store{Synced: []string{jan1}}
	source := &mock.Source{Files: map[string][]b3.Quote{jan1: {cogn3}, jan2: {cogn3}}}

	if err := quote.SyncHistory(t.Context(), store, source, 2026); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(source.Requested, []string{jan2}) {
		t.Errorf("requested %v, want only the missing file", source.Requested)
	}
}

func TestSyncHistoryUpToDate(t *testing.T) {
	fixedClock(t)
	store := &mock.Store{Synced: []string{jan1, jan2}}
	source := &mock.Source{}

	if err := quote.SyncHistory(t.Context(), store, source, 2026); err != nil {
		t.Fatal(err)
	}
	if len(source.Requested) != 0 || slices.ContainsFunc(store.Calls, func(c string) bool { return c != "Migrate" && c != "SyncedFiles" }) {
		t.Errorf("requested %v, calls %v, want nothing to do", source.Requested, store.Calls)
	}
}

func TestSyncHistoryRecordsEmptyFiles(t *testing.T) {
	fixedClock(t)
	store := &mock.Store{}
	// A published file without stocks is still recorded, so it is not downloaded again.
	source := &mock.Source{Files: map[string][]b3.Quote{jan1: {}, jan2: {}}}

	if err := quote.SyncHistory(t.Context(), store, source, 2026); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(store.Synced, []string{jan1, jan2}) {
		t.Errorf("synced %v", store.Synced)
	}
}

func TestSyncHistoryFutureYear(t *testing.T) {
	fixedClock(t)
	store := &mock.Store{}
	source := &mock.Source{}

	if err := quote.SyncHistory(t.Context(), store, source, 2027); err != nil {
		t.Fatal(err)
	}
	if len(source.Requested) != 0 {
		t.Errorf("requested %v, want nothing", source.Requested)
	}
}

func TestSyncHistoryErrors(t *testing.T) {
	fail := errors.New("boom")
	published := map[string][]b3.Quote{jan1: {cogn3}, jan2: {cogn3}}
	tests := []struct {
		name   string
		store  *mock.Store
		source *mock.Source
		want   string
	}{
		{"migration fails", &mock.Store{MigrateErr: fail}, &mock.Source{Files: published}, "migrate: boom"},
		{"synced files cannot be listed", &mock.Store{SyncedFilesErr: fail}, &mock.Source{Files: published}, "boom"},
		{"download fails", &mock.Store{}, &mock.Source{Err: map[string]error{jan1: fail}}, "sync " + jan1 + ": boom"},
		{"download cut short", &mock.Store{}, &mock.Source{Files: published, Err: map[string]error{jan1: fail}}, "sync " + jan1 + ": boom"},
		{"file cannot be saved", &mock.Store{SaveErr: map[string]error{jan1: fail}}, &mock.Source{Files: published}, "sync " + jan1 + ": boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixedClock(t)
			err := quote.SyncHistory(t.Context(), tt.store, tt.source, 2026)
			if !errors.Is(err, fail) || err.Error() != tt.want {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
			// The sync stops at the first failure and keeps nothing of the failed file.
			if slices.Contains(tt.source.Requested, jan2) {
				t.Errorf("requested %v after the failure", tt.source.Requested)
			}
			if len(tt.store.Quotes) != 0 || len(tt.store.Synced) != 0 {
				t.Errorf("stored %d quotes and synced %v, want nothing", len(tt.store.Quotes), tt.store.Synced)
			}
		})
	}
}
