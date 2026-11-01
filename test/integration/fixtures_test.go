//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"oracle/internal/b3"
	"oracle/internal/quote"
)

// cotahist formats q as a 245-byte COTAHIST record, in the layout B3 publishes.
// bdi "02" is the round lot and market "010" the cash market, the only records the sync keeps.
func cotahist(q quote.Quote, bdi, market string) string {
	cents := func(v float64) int64 { return int64(math.Round(v * 100)) }
	var b strings.Builder
	fmt.Fprintf(&b, "01%s%-2s%-12s%-3s%-12s%-10s", q.Date.Format("20060102"), bdi, q.Ticker, market, q.Name, q.Spec)
	b.WriteString("   R$  ")
	for _, p := range []float64{q.Open, q.High, q.Low, q.Average, q.Close, 0, 0} {
		fmt.Fprintf(&b, "%013d", cents(p))
	}
	fmt.Fprintf(&b, "%05d%018d%018d", q.Trades, q.Quantity, cents(q.Volume))
	b.WriteString(strings.Repeat("0", 42)) // strike, option type, expiry, quote factor and exercise points
	fmt.Fprintf(&b, "%-12s112", q.ISIN)
	return b.String()
}

func stock(q quote.Quote) string { return cotahist(q, "02", "010") }

// file is the content of one COTAHIST archive, or an HTTP failure.
type file struct {
	records []string
	status  int // 0 serves the records
}

// fakeB3 serves COTAHIST archives by name and answers 404, "not published yet", for every other file.
type fakeB3 struct {
	mu         sync.Mutex
	files      map[string]file
	downloaded []string // files served when requested, in order
}

func newFakeB3(t *testing.T, files map[string]file) (*fakeB3, *b3.Client) {
	t.Helper()
	f := &fakeB3{files: files}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &b3.Client{HTTP: srv.Client(), BaseURL: srv.URL}
}

func (f *fakeB3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	f.mu.Lock()
	content, ok := f.files[name]
	if ok {
		f.downloaded = append(f.downloaded, name)
	}
	f.mu.Unlock()

	switch {
	case !ok:
		w.WriteHeader(http.StatusNotFound)
	case content.status != 0:
		w.WriteHeader(content.status)
	default:
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		zf, _ := zw.Create(strings.TrimSuffix(name, ".ZIP") + ".TXT")
		for _, rec := range content.records {
			_, _ = zf.Write([]byte(rec + "\r\n"))
		}
		_ = zw.Close()
		_, _ = w.Write(buf.Bytes())
	}
}

// fetched lists the files requested while B3 had them, ignoring the ones not published yet.
func (f *fakeB3) fetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.downloaded)
}

func (f *fakeB3) set(name string, content file) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[name] = content
}

// trend is a liquid stock in a steady uptrend, with a winning pullback trade at every index in dips
// and, when pullbackToday is set, a fresh oversold signal on the last bar. The last bar is on last.
//
// A dip is one down day (RSI(2) about 9, above the SMA200) followed by a day that touches the
// limit entry and closes above the target, so the rule records a win.
func trend(ticker string, n int, last time.Time, dips []int, pullbackToday bool) []quote.Quote {
	isDip := make(map[int]bool, len(dips))
	for _, d := range dips {
		isDip[d] = true
	}
	bars := make([]quote.Quote, 0, n)
	c := 10.0
	for i := range n {
		var q quote.Quote
		switch {
		case pullbackToday && i >= n-2:
			c -= 0.4
			q = quote.Quote{Open: c + 0.34, High: c + 0.39, Low: c - 0.06, Close: c}
		case isDip[i]:
			c -= 0.4
			q = quote.Quote{Open: c + 0.34, High: c + 0.39, Low: c - 0.06, Close: c}
		case isDip[i-1]:
			q = quote.Quote{Open: c + 0.04, High: c + 0.94, Low: c - 0.26, Close: c + 0.84}
			c += 0.84
		default:
			c += 0.04
			q = quote.Quote{Open: c - 0.02, High: c + 0.1, Low: c - 0.1, Close: c}
		}
		q.Ticker, q.Name, q.Spec, q.ISIN = ticker, ticker+" SA", "ON      NM", "BR"+ticker+"ACNOR"
		q.Date = last.AddDate(0, 0, i-n+1)
		q.Average = round(q.Close)
		q.Open, q.High, q.Low, q.Close = round(q.Open), round(q.High), round(q.Low), round(q.Close)
		q.Trades, q.Quantity, q.Volume = 1000, 1_000_000, 1e8
		bars = append(bars, q)
	}
	return bars
}

// dips spaces n winning trades 30 sessions apart, after the 200 sessions the trend filter needs.
func dips(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = 220 + 30*i
	}
	return idx
}

func round(v float64) float64 { return math.Round(v*100) / 100 }

func records(bars []quote.Quote) []string {
	recs := make([]string, len(bars))
	for i, q := range bars {
		recs[i] = stock(q)
	}
	return recs
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// todayUTC is the current date at midnight, how dates are stored.
func todayUTC() time.Time {
	y, m, d := time.Now().Date()
	return day(y, m, d)
}
