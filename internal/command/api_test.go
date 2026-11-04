package command

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"oracle/internal/quote"
	"oracle/test/fixture"
	"oracle/test/mock"
)

func stored() *mock.Store {
	day := fixture.Day(2026, 10, 29)
	q := func(ticker, name, spec string, d time.Time, closePrice float64) quote.Quote {
		return quote.Quote{
			Ticker: ticker, Name: name, Spec: spec, ISIN: "BR" + ticker, Date: d,
			Open: closePrice - 1, High: closePrice + 1, Low: closePrice - 2, Average: closePrice, Close: closePrice,
			Trades: 10, Quantity: 1000, Volume: 1000 * closePrice,
		}
	}
	return &mock.Store{Quotes: []quote.Quote{
		q("PETR4", "PETROBRAS", "PN      N2", day, 38),
		q("PETR4", "PETROBRAS", "PN      N2", day.AddDate(0, 0, 1), 38.5),
		q("VALE3", "VALE", "ON      NM", day, 60),
	}}
}

// now is 5 days after the last session in stored().
func now() time.Time { return fixture.Day(2026, 11, 4) }

func serve(t *testing.T, store *mock.Store, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	newMux(store, now, nil).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

const (
	petr4Day1 = `{"ticker":"PETR4","name":"PETROBRAS","date":"2026-10-29","open":37,"high":39,"low":36,"average":38,"close":38,"trades":10,"quantity":1000,"volume":38000}`
	petr4Day2 = `{"ticker":"PETR4","name":"PETROBRAS","date":"2026-10-30","open":37.5,"high":39.5,"low":36.5,"average":38.5,"close":38.5,"trades":10,"quantity":1000,"volume":38500}`
	vale3Day1 = `{"ticker":"VALE3","name":"VALE","date":"2026-10-29","open":59,"high":61,"low":58,"average":60,"close":60,"trades":10,"quantity":1000,"volume":60000}`
	petr4     = `"ticker":"PETR4","name":"PETROBRAS","spec":"PN N2","isin":"BRPETR4","first_session":"2026-10-29","last_session":"2026-10-30","sessions":2`
)

func TestQuoteRoutes(t *testing.T) {
	cases := []struct {
		name   string
		store  *mock.Store
		path   string
		status int
		body   string
	}{
		{"tickers", stored(), "/tickers", 200,
			`[{` + petr4 + `},{"ticker":"VALE3","name":"VALE","spec":"ON NM","isin":"BRVALE3","first_session":"2026-10-29","last_session":"2026-10-29","sessions":1}]`},
		{"tickers of an empty store", &mock.Store{}, "/tickers", 200, `[]`},
		{"ticker, case-insensitive", stored(), "/tickers/petr4", 200, `{` + petr4 + `,"last_quote":` + petr4Day2 + `}`},
		{"unknown ticker", stored(), "/tickers/ZZZZ3", 404, `{"error":"ticker ZZZZ3 not found"}`},
		{"ticker quotes", stored(), "/tickers/PETR4/quotes", 200, `[` + petr4Day1 + `,` + petr4Day2 + `]`},
		{"ticker quotes from a day", stored(), "/tickers/PETR4/quotes?from=2026-10-30", 200, `[` + petr4Day2 + `]`},
		{"ticker quotes until a day", stored(), "/tickers/PETR4/quotes?to=2026-10-29", 200, `[` + petr4Day1 + `]`},
		{"ticker quotes without sessions in range", stored(), "/tickers/PETR4/quotes?from=2026-11-02", 200, `[]`},
		{"ticker quotes, bad from", stored(), "/tickers/PETR4/quotes?from=30/10/2026", 400, `{"error":"from \"30/10/2026\" is not a YYYY-MM-DD date"}`},
		{"ticker quotes, bad to", stored(), "/tickers/PETR4/quotes?to=yesterday", 400, `{"error":"to \"yesterday\" is not a YYYY-MM-DD date"}`},
		{"ticker quotes, from after to", stored(), "/tickers/PETR4/quotes?from=2026-10-30&to=2026-10-29", 400, `{"error":"from must not be after to"}`},
		{"ticker quotes, unknown ticker", stored(), "/tickers/ZZZZ3/quotes", 404, `{"error":"ticker ZZZZ3 not found"}`},
		{"latest session, stale", stored(), "/sessions/latest", 200, `{"date":"2026-10-30","stale":true}`},
		{"latest session of an empty store", &mock.Store{}, "/sessions/latest", 404, `{"error":"no quotes stored yet, run oracle sync"}`},
		{"session quotes", stored(), "/sessions/2026-10-29/quotes", 200, `[` + petr4Day1 + `,` + vale3Day1 + `]`},
		{"session without quotes", stored(), "/sessions/2026-11-01/quotes", 404, `{"error":"no session on 2026-11-01"}`},
		{"session, bad date", stored(), "/sessions/today/quotes", 400, `{"error":"date \"today\" is not a YYYY-MM-DD date"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := serve(t, c.store, c.path)
			if rec.Code != c.status || strings.TrimSpace(rec.Body.String()) != c.body {
				t.Errorf("got %d %s\nwant %d %s", rec.Code, rec.Body.String(), c.status, c.body)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type %q", ct)
			}
		})
	}
}

func TestLatestSessionIsFresh(t *testing.T) {
	store := &mock.Store{Quotes: []quote.Quote{{Ticker: "PETR4", Date: now().AddDate(0, 0, -4)}}}
	rec := serve(t, store, "/sessions/latest")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stale":false`) {
		t.Errorf("got %d %s, want a fresh session", rec.Code, rec.Body.String())
	}
}

func TestQuoteRoutesHideStoreErrors(t *testing.T) {
	paths := []string{"/tickers", "/tickers/PETR4", "/tickers/PETR4/quotes", "/sessions/latest", "/sessions/2026-10-29/quotes"}
	for _, path := range paths {
		store := stored()
		store.ReadErr = errors.New("connection refused: password=secret")
		rec := serve(t, store, path)
		if rec.Code != http.StatusInternalServerError || strings.TrimSpace(rec.Body.String()) != `{"error":"internal error"}` {
			t.Errorf("%s: got %d %s, want a 500 without the cause", path, rec.Code, rec.Body.String())
		}
	}
}

type failQuotes struct{ *mock.Store }

func (failQuotes) TickerQuotes(_ context.Context, _ string, _, _ time.Time) ([]quote.Quote, error) {
	return nil, errors.New("query canceled")
}

func TestTickerRoutesFailOnQuotes(t *testing.T) {
	mux := newMux(failQuotes{stored()}, now, nil)
	for _, path := range []string{"/tickers/PETR4", "/tickers/PETR4/quotes"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: got %d %s, want 500", path, rec.Code, rec.Body.String())
		}
	}
}

func TestHealthAndSwaggerRoutes(t *testing.T) {
	for _, path := range []string{"/health", "/swagger/doc.json"} {
		if rec := serve(t, &mock.Store{}, path); rec.Code != http.StatusOK {
			t.Errorf("%s: got %d", path, rec.Code)
		}
	}
	if rec := serve(t, &mock.Store{}, "/tickers/PETR4/unknown"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown route: got %d", rec.Code)
	}
}

func TestTickersAreCachedUntilANewSession(t *testing.T) {
	store := stored()
	mux := newMux(store, now, nil)
	first := get(t, mux, "/tickers").Body.String()
	for range 3 {
		get(t, mux, "/tickers")
	}
	if n := countCalls(store, "Tickers"); n != 1 {
		t.Errorf("listed tickers %d times, want once for the same session", n)
	}
	store.Quotes = append(store.Quotes, quote.Quote{Ticker: "NEW3", Spec: "ON", Date: fixture.Day(2026, 10, 31), Close: 10})
	rec := get(t, mux, "/tickers")
	if n := countCalls(store, "Tickers"); n != 2 || !strings.Contains(rec.Body.String(), `"ticker":"NEW3"`) || rec.Body.String() == first {
		t.Errorf("after a new session: %d listings, body %s", n, rec.Body.String())
	}
}

func countCalls(store *mock.Store, method string) int {
	n := 0
	for _, c := range store.Calls {
		if c == method {
			n++
		}
	}
	return n
}

func TestRequestTimeout(t *testing.T) {
	slow := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // a query canceled with the request
	})
	rec := httptest.NewRecorder()
	withTimeout(slow, 20*time.Millisecond).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/signals", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Content-Type") != "application/json" ||
		strings.TrimSpace(rec.Body.String()) != `{"error":"the request took too long, try again in a moment"}` {
		t.Errorf("got %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	page := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html></html>"))
	})
	rec = httptest.NewRecorder()
	withTimeout(page, time.Second).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/swagger/index.html", http.NoBody))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("page: got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
