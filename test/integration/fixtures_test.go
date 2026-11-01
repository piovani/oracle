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

func records(bars []quote.Quote) []string {
	recs := make([]string, len(bars))
	for i, q := range bars {
		recs[i] = stock(q)
	}
	return recs
}
