package yahoo

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// fakeYahoo serves the cookie, crumb and quote summary endpoints.
// Requests are recorded so tests can check what was sent.
type fakeYahoo struct {
	crumbStatus int
	status      int
	body        string

	mu       sync.Mutex
	requests []*http.Request
}

func (f *fakeYahoo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()

	switch r.URL.Path {
	case "/":
		http.SetCookie(w, &http.Cookie{Name: "A3", Value: "session", Path: "/"})
		w.WriteHeader(http.StatusNotFound) // fc.yahoo.com answers 404 but still sets the cookie
	case "/v1/test/getcrumb":
		w.WriteHeader(f.crumbStatus)
		_, _ = w.Write([]byte("  abc/123\n"))
	default:
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}
}

func (f *fakeYahoo) request(t *testing.T, i int) *http.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		t.Fatalf("got %d requests, want at least %d", len(f.requests), i+1)
	}
	return f.requests[i]
}

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// unreachable is a URL nothing listens on.
// unreachable returns a URL nothing listens on. It is a fixed low port, not a
// closed httptest server: that one frees an ephemeral port that a parallel
// test's server can take, which made the connection succeed.
func unreachable(t *testing.T) string {
	t.Helper()
	return "http://127.0.0.1:1"
}

func newTestClient(t *testing.T, base string) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{HTTP: &http.Client{Jar: jar}, BaseURL: base}
}

func TestConnect(t *testing.T) {
	t.Parallel()
	f := &fakeYahoo{crumbStatus: http.StatusOK}
	base := serve(t, f)
	c := newTestClient(t, base)

	if err := c.connect(t.Context(), base+"/"); err != nil {
		t.Fatal(err)
	}
	if c.crumb != "abc/123" {
		t.Errorf("crumb %q, want it trimmed", c.crumb)
	}
	crumb := f.request(t, 1)
	if crumb.URL.Path != "/v1/test/getcrumb" {
		t.Errorf("second request to %q, want the crumb", crumb.URL.Path)
	}
	if cookie, err := crumb.Cookie("A3"); err != nil || cookie.Value != "session" {
		t.Errorf("crumb request did not send the session cookie: %v", err)
	}
	if crumb.UserAgent() != userAgent {
		t.Errorf("user agent %q", crumb.UserAgent())
	}
}

func TestConnectErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		crumbStatus         int
		cookieDown, apiDown bool
	}{
		{"cookie endpoint unreachable", http.StatusOK, true, false},
		{"crumb endpoint unreachable", http.StatusOK, false, true},
		{"crumb refused", http.StatusUnauthorized, false, false},
		{"crumb rate limited", http.StatusTooManyRequests, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := serve(t, &fakeYahoo{crumbStatus: tt.crumbStatus})
			cookie, api := base+"/", base
			if tt.cookieDown {
				cookie = unreachable(t)
			}
			if tt.apiDown {
				api = unreachable(t)
			}
			c := newTestClient(t, api)
			if err := c.connect(t.Context(), cookie); err == nil {
				t.Errorf("got crumb %q, want an error", c.crumb)
			}
		})
	}
}

// redirect sends every request to target, keeping the path and query.
type redirect struct {
	target *url.URL
	next   http.RoundTripper
}

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host = r.target.Scheme, r.target.Host
	out.Host = ""
	return r.next.RoundTrip(out)
}

// TestNewClient checks the wiring to Yahoo's real URLs, routing the default transport to a fake.
// It changes a global, so it must not run in parallel.
func TestNewClient(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	use := func(f *fakeYahoo) {
		target, err := url.Parse(serve(t, f))
		if err != nil {
			t.Fatal(err)
		}
		http.DefaultTransport = redirect{target: target, next: orig}
	}

	t.Run("connects", func(t *testing.T) {
		f := &fakeYahoo{crumbStatus: http.StatusOK}
		use(f)
		c, err := NewClient(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if c.BaseURL != baseURL || c.crumb != "abc/123" || c.HTTP.Jar == nil || c.HTTP.Timeout == 0 {
			t.Errorf("got %+v", c)
		}
		if got := f.request(t, 0).URL.Path; got != "/" {
			t.Errorf("first request to %q, want the cookie endpoint", got)
		}
	})

	t.Run("fails without a crumb", func(t *testing.T) {
		use(&fakeYahoo{crumbStatus: http.StatusUnauthorized})
		if c, err := NewClient(t.Context()); err == nil || c != nil {
			t.Errorf("got %+v, %v, want only an error", c, err)
		}
	})
}

func TestEstimate(t *testing.T) {
	t.Parallel()
	f := &fakeYahoo{status: http.StatusOK, body: `{"quoteSummary":{"result":[{"financialData":{
		"targetMeanPrice":{"raw":42.5,"fmt":"42.50"},
		"recommendationMean":{"raw":1.8},
		"numberOfAnalystOpinions":{"raw":12}}}]}}`}
	c := newTestClient(t, serve(t, f))
	c.crumb = "abc/123"

	got, err := c.Estimate(t.Context(), "PETR4")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Estimate{Target: 42.5, Rating: 1.8, Analysts: 12}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	req := f.request(t, 0)
	if req.URL.Path != "/v10/finance/quoteSummary/PETR4.SA" {
		t.Errorf("path %q", req.URL.Path)
	}
	if q := req.URL.Query(); q.Get("modules") != "financialData" || q.Get("crumb") != "abc/123" {
		t.Errorf("query %q", req.URL.RawQuery)
	}
	if req.UserAgent() != userAgent {
		t.Errorf("user agent %q", req.UserAgent())
	}
}

func TestEstimateWithoutRatingOrAnalysts(t *testing.T) {
	t.Parallel()
	f := &fakeYahoo{status: http.StatusOK, body: `{"quoteSummary":{"result":[{"financialData":{"targetMeanPrice":{"raw":10}}}]}}`}
	got, err := newTestClient(t, serve(t, f)).Estimate(t.Context(), "VALE3")
	if err != nil || got != (Estimate{Target: 10}) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestEstimateErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   error // nil means any error other than ErrNoCoverage
	}{
		{"unknown ticker", http.StatusNotFound, `{}`, ErrNoCoverage},
		{"empty result", http.StatusOK, `{"quoteSummary":{"result":[]}}`, ErrNoCoverage},
		{"no financial data", http.StatusOK, `{"quoteSummary":{"result":[{}]}}`, ErrNoCoverage},
		{"zero target", http.StatusOK, `{"quoteSummary":{"result":[{"financialData":{"targetMeanPrice":{"raw":0}}}]}}`, ErrNoCoverage},
		{"negative target", http.StatusOK, `{"quoteSummary":{"result":[{"financialData":{"targetMeanPrice":{"raw":-1}}}]}}`, ErrNoCoverage},
		{"server error", http.StatusInternalServerError, ``, nil},
		{"expired crumb", http.StatusUnauthorized, ``, nil},
		{"invalid JSON", http.StatusOK, `{"quoteSummary":`, nil},
		{"wrong type", http.StatusOK, `{"quoteSummary":{"result":[{"financialData":{"targetMeanPrice":{"raw":"ten"}}}]}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := newTestClient(t, serve(t, &fakeYahoo{status: tt.status, body: tt.body})).Estimate(t.Context(), "PETR4")
			switch {
			case err == nil:
				t.Fatal("got no error")
			case tt.want != nil && !errors.Is(err, tt.want):
				t.Errorf("got %v, want %v", err, tt.want)
			case tt.want == nil && errors.Is(err, ErrNoCoverage):
				t.Errorf("got %v, want a real failure, not missing coverage", err)
			}
		})
	}
}

func TestEstimateTransportErrors(t *testing.T) {
	t.Parallel()
	truncated := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte(`{"quoteSummary":`))
	}))
	tests := []struct {
		name string
		base string
	}{
		{"server unreachable", unreachable(t)},
		{"truncated body", truncated},
		{"invalid URL", "http://bad\x7f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newTestClient(t, tt.base).Estimate(t.Context(), "PETR4"); err == nil || errors.Is(err, ErrNoCoverage) {
				t.Errorf("got %v, want a transport error", err)
			}
		})
	}
}

func TestEstimateEscapesTicker(t *testing.T) {
	t.Parallel()
	f := &fakeYahoo{status: http.StatusNotFound}
	_, _ = newTestClient(t, serve(t, f)).Estimate(t.Context(), "A B/C")
	if got := f.request(t, 0).URL.EscapedPath(); got != "/v10/finance/quoteSummary/A%20B%2FC.SA" {
		t.Errorf("path %q", got)
	}
}
