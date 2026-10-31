package b3

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = "00COTAHIST.2026BOVESPA 20261002\n" +
	"012026100202COGN3       010COGNA ON    ON      NM   R$  000000000023800000000002450000000000235000000000024000000000002430000000000243000000000024429896000000000044274400000000010626183600000000000000009999123100000010000000000000BRCOGNACNOR2112\n" +
	"012026100296COGN3F      020COGNA ON    ON      NM   R$  000000000023800000000002450000000000235000000000024000000000002430000000000243000000000024429896000000000044274400000000010626183600000000000000009999123100000010000000000000BRCOGNACNOR2112\n" +
	"99COTAHIST.2026BOVESPA 2026100200000000004\n"

func TestParse(t *testing.T) {
	var got []Quote
	err := Parse(strings.NewReader(sample), func(q Quote) error {
		got = append(got, q)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d quotes, want 1", len(got))
	}

	want := Quote{
		Date:     time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		Ticker:   "COGN3",
		Name:     "COGNA ON",
		Spec:     "ON      NM",
		ISIN:     "BRCOGNACNOR2",
		Open:     2.38,
		High:     2.45,
		Low:      2.35,
		Average:  2.40,
		Close:    2.43,
		Trades:   29896,
		Quantity: 44274400,
		Volume:   106261836.00,
	}
	if got[0] != want {
		t.Errorf("got  %+v\nwant %+v", got[0], want)
	}
}

func TestFileNames(t *testing.T) {
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	if got := AnnualFile(2025); got != "COTAHIST_A2025.ZIP" {
		t.Error(got)
	}
	if got := MonthlyFile(2026, time.September); got != "COTAHIST_M092026.ZIP" {
		t.Error(got)
	}
	if got := DailyFile(day); got != "COTAHIST_D02102026.ZIP" {
		t.Error(got)
	}
}

// record is the COGN3 line of the sample with the given field replaced.
func record(start int, value string) string {
	rec := strings.Split(sample, "\n")[1]
	return rec[:start] + value + rec[start+len(value):]
}

func parseAll(input string) ([]Quote, error) {
	var got []Quote
	err := Parse(strings.NewReader(input), func(q Quote) error {
		got = append(got, q)
		return nil
	})
	return got, err
}

func TestParseSkipsOtherRecords(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"header", "00COTAHIST.2026BOVESPA 20261002" + strings.Repeat(" ", recordLen)},
		{"trailer", "99COTAHIST.2026BOVESPA 2026100200000000004" + strings.Repeat(" ", recordLen)},
		{"short line", record(0, "01")[:recordLen-1]},
		{"odd lot", record(10, "96")},
		{"options market", record(24, "070")},
		{"forward market", record(24, "030")},
		{"empty line", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAll(tt.line + "\n")
			if err != nil || len(got) != 0 {
				t.Errorf("got %v, %v, want no quotes and no error", got, err)
			}
		})
	}
}

func TestParseReadsEveryQuote(t *testing.T) {
	second := record(12, "PETR4       ")
	got, err := parseAll(sample + second + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Ticker != "COGN3" || got[1].Ticker != "PETR4" {
		t.Errorf("got %+v", got)
	}
}

func TestParseConvertsLatin1(t *testing.T) {
	got, err := parseAll(record(27, "A\xc7\xc3O SA     ") + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "AÇÃO SA" {
		t.Errorf("got %+v", got)
	}
}

func TestParseInvalidFields(t *testing.T) {
	tests := []struct {
		name  string
		start int
		value string
	}{
		{"date", 2, "2026AB02"},
		{"open", 56, "0000000000X38"},
		{"high", 69, "0000000000X45"},
		{"low", 82, "0000000000X35"},
		{"average", 95, "0000000000X40"},
		{"close", 108, "0000000000X43"},
		{"trades", 147, "29X96"},
		{"quantity", 152, "00000000004427X400"},
		{"volume", 170, "0000000001062618X6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseAll("header\n" + record(tt.start, tt.value) + "\n")
			if err == nil || !strings.HasPrefix(err.Error(), "line 2:") {
				t.Errorf("got %v, want an error on line 2", err)
			}
		})
	}
}

func TestParseStopsOnCallbackError(t *testing.T) {
	stop := errors.New("stop")
	calls := 0
	err := Parse(strings.NewReader(sample+sample), func(Quote) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Errorf("got %v after %d calls, want the callback error after 1", err, calls)
	}
}

func TestParseLineTooLong(t *testing.T) {
	if _, err := parseAll(strings.Repeat("x", bufio.MaxScanTokenSize+1)); err == nil {
		t.Error("got no error")
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient()
	if c.BaseURL != baseURL || c.HTTP == nil || c.HTTP.Timeout == 0 {
		t.Errorf("got %+v", c)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serve(t *testing.T, status int, body []byte) (*Client, *string) {
	t.Helper()
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL}, &path
}

func TestFetch(t *testing.T) {
	c, path := serve(t, http.StatusOK, zipOf(t, map[string]string{"COTAHIST_D02102026.TXT": sample}))
	var got []Quote
	err := c.Fetch(t.Context(), "COTAHIST_D02102026.ZIP", func(q Quote) error {
		got = append(got, q)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if *path != "/COTAHIST_D02102026.ZIP" {
		t.Errorf("requested %q", *path)
	}
	if len(got) != 1 || got[0].Ticker != "COGN3" {
		t.Errorf("got %+v", got)
	}
}

func TestFetchReadsEveryFileInTheArchive(t *testing.T) {
	c, _ := serve(t, http.StatusOK, zipOf(t, map[string]string{"a.txt": sample, "b.txt": sample}))
	calls := 0
	if err := c.Fetch(t.Context(), "x.zip", func(Quote) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("got %d quotes, want 2", calls)
	}
}

func TestFetchErrors(t *testing.T) {
	stop := errors.New("stop")
	tests := []struct {
		name   string
		status int
		body   []byte
		fn     func(Quote) error
		is     error
	}{
		{"not published", http.StatusNotFound, nil, nil, ErrNotFound},
		{"server error", http.StatusInternalServerError, nil, nil, nil},
		{"not a zip", http.StatusOK, []byte("not a zip"), nil, nil},
		{"invalid record", http.StatusOK, zipOf(t, map[string]string{"a.txt": record(2, "2026AB02")}), nil, nil},
		{"callback error", http.StatusOK, zipOf(t, map[string]string{"a.txt": sample}), func(Quote) error { return stop }, stop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := serve(t, tt.status, tt.body)
			fn := tt.fn
			if fn == nil {
				fn = func(Quote) error { return nil }
			}
			err := c.Fetch(t.Context(), "x.zip", fn)
			if err == nil {
				t.Fatal("got no error")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("got %v, want %v", err, tt.is)
			}
			if !errors.Is(tt.is, ErrNotFound) && errors.Is(err, ErrNotFound) {
				t.Errorf("got %v, must not look like an unpublished file", err)
			}
		})
	}
}

func TestFetchNetworkErrors(t *testing.T) {
	t.Run("server down", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
		if err := c.Fetch(t.Context(), "x.zip", func(Quote) error { return nil }); err == nil {
			t.Error("got no error")
		}
	})
	t.Run("invalid URL", func(t *testing.T) {
		c := &Client{HTTP: http.DefaultClient, BaseURL: "://bad"}
		if err := c.Fetch(t.Context(), "x.zip", func(Quote) error { return nil }); err == nil {
			t.Error("got no error")
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		c, _ := serve(t, http.StatusOK, zipOf(t, map[string]string{"a.txt": sample}))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := c.Fetch(ctx, "x.zip", func(Quote) error { return nil }); !errors.Is(err, context.Canceled) {
			t.Errorf("got %v, want context.Canceled", err)
		}
	})
	t.Run("truncated download", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte("short"))
		}))
		defer srv.Close()
		c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
		if err := c.Fetch(t.Context(), "x.zip", func(Quote) error { return nil }); err == nil || !strings.Contains(err.Error(), "download") {
			t.Errorf("got %v, want a download error", err)
		}
	})
}

func TestFetchUnsupportedCompression(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "a.txt", Method: 99})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("data"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	c, _ := serve(t, http.StatusOK, buf.Bytes())
	if err := c.Fetch(t.Context(), "x.zip", func(Quote) error { return nil }); !errors.Is(err, zip.ErrAlgorithm) {
		t.Errorf("got %v, want zip.ErrAlgorithm", err)
	}
}

func TestFetchWithoutTempDir(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	c, _ := serve(t, http.StatusOK, zipOf(t, map[string]string{"a.txt": sample}))
	if err := c.Fetch(t.Context(), "x.zip", func(Quote) error { return nil }); err == nil {
		t.Error("got no error")
	}
}
