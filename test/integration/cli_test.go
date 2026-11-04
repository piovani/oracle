//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"oracle/config"
	"oracle/internal/backtest"
	"oracle/internal/quote"
	"oracle/internal/swing"
	"oracle/test/fixture"
)

type output struct {
	stdout, stderr string
	code           int
}

// oracle runs the compiled binary against the given database, with extra environment variables.
func oracle(t *testing.T, cfg config.DBConfig, extraEnv []string, args ...string) output {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Env = append(env(cfg), extraEnv...)
	cmd.Dir = t.TempDir() // no .env file is picked up
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	out := output{}
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		out.code = exit.ExitCode()
	case err != nil:
		t.Fatalf("run oracle %v: %v", args, err)
	}
	out.stdout, out.stderr = stdout.String(), stderr.String()
	return out
}

func (o output) String() string {
	return fmt.Sprintf("exit %d\n--- stdout\n%s--- stderr\n%s", o.code, o.stdout, o.stderr)
}

func TestCLISyncCreatesTheSchema(t *testing.T) {
	t.Parallel()
	db, cfg := emptyDatabase(t)
	// No file is due for a year in the future, so nothing is downloaded from the real B3.
	out := oracle(t, cfg, nil, "sync", "-since", "2999")
	if out.code != 0 || !strings.Contains(out.stderr, "quote history is up to date") {
		t.Fatalf("sync failed:\n%s", out)
	}
	for _, table := range []any{&quote.Quote{}, &quote.SyncedFile{}} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table for %T not created", table)
		}
	}
}

func TestCLIBacktest(t *testing.T) {
	t.Parallel()
	db, cfg := newDatabase(t)
	bars := fixture.Trend("WIN3", 700, fixture.Today(), fixture.Dips(15))
	insert(t, db, bars, fixture.Trend("FLAT3", 700, fixture.Today(), nil))
	from := bars[300].Date
	trades := filepath.Join(t.TempDir(), "trades.csv")

	out := oracle(t, cfg, nil, "backtest", "-from", from.Format(time.DateOnly), "-csv", trades)
	if out.code != 0 {
		t.Fatalf("backtest failed:\n%s", out)
	}

	// The command must print what the backtest package computes from the same database with the default flags.
	history, err := quote.NewPostgres(db).History(t.Context(), from.AddDate(-4, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	res, err := backtest.Run(history, backtest.Config{
		Params: swing.DefaultParams(), Capital: 500, Positions: 5, RiskPct: 2, From: from, Lookback: 3, Rank: backtest.RankExpectancy,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summary()
	if s.Trades == 0 {
		t.Fatal("the scenario produced no trades")
	}
	for _, want := range []string{
		fmt.Sprintf("Backtest — %s a %s", from.Format("02/01/2006"), fixture.Today().Format("02/01/2006")),
		"ranking expectancy | RSI(2) < 10",
		fmt.Sprintf("Operações: %d | ordens enviadas: %d", s.Trades, res.Orders),
		fmt.Sprintf("R$ %.2f", s.Final),
		fmt.Sprintf("%d operações gravadas em %s", s.Trades, trades),
	} {
		if !strings.Contains(out.stdout, want) {
			t.Errorf("output does not contain %q:\n%s", want, out.stdout)
		}
	}

	f, err := os.Open(trades)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != s.Trades+1 || rows[0][0] != "ticker" || rows[1][0] != "WIN3" {
		t.Errorf("CSV has %d rows starting %v, want a header and %d trades of WIN3", len(rows), rows[0], s.Trades)
	}
}

func TestCLIBacktestFlags(t *testing.T) {
	t.Parallel()
	db, cfg := newDatabase(t)
	insert(t, db, fixture.Trend("WIN3", 400, fixture.Today(), fixture.Dips(5)))

	out := oracle(t, cfg, nil, "backtest", "-from", fixture.Today().AddDate(0, 0, -100).Format(time.DateOnly),
		"-rank", "rsi", "-oversold", "30", "-cost", "0.1", "-top", "1", "-risk", "1.5", "-capital", "2000")
	if out.code != 0 {
		t.Fatalf("backtest failed:\n%s", out)
	}
	want := "Capital R$ 2000.00 | até 1 posições | risco 1.5% | custo 0.10% por lado | ranking rsi | RSI(2) < 30"
	if !strings.Contains(out.stdout, want) {
		t.Errorf("output does not contain %q:\n%s", want, out.stdout)
	}
}

func TestCLIRecommend(t *testing.T) {
	t.Parallel()
	db, cfg := newDatabase(t)
	today := fixture.Today()
	insert(t, db,
		fixture.Trend("WIN3", 400, today, fixture.Dips(5), fixture.StrongPullback...),
		fixture.Trend("OLD3", 400, today.AddDate(0, 0, -10), fixture.Dips(5), fixture.StrongPullback...), // stopped trading
		fixture.Trend("FLAT3", 400, today, nil),                                                          // no signal today
	)

	out := oracle(t, cfg, nil, "recommend", "-no-estimates", "-capital", "1000", "-top", "3")
	if out.code != 0 {
		t.Fatalf("recommend failed:\n%s", out)
	}

	history, err := quote.NewPostgres(db).History(t.Context(), today.AddDate(-3, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	a := swing.Analyze("WIN3", history["WIN3"], swing.DefaultParams())
	if !a.Signal || a.Trades != 5 {
		t.Fatalf("scenario: WIN3 analysis %+v, want 5 trades and a signal", a)
	}
	plan := ""
	for line := range strings.SplitSeq(out.stdout, "\n") {
		if strings.HasPrefix(line, "WIN3F") {
			plan = line
		}
	}
	if plan == "" {
		t.Fatalf("WIN3 not recommended:\n%s", out.stdout)
	}
	for _, want := range []string{"forte", fmt.Sprintf("%.2f", a.MaxEntry), fmt.Sprintf("%.2f", a.Target), fmt.Sprintf("%.2f", a.Stop), "100%"} {
		if !strings.Contains(plan, want) {
			t.Errorf("WIN3 line %q does not contain %q", plan, want)
		}
	}
	if strings.Contains(out.stdout, "OLD3F") || strings.Contains(out.stdout, "FLAT3F") {
		t.Errorf("recommended a stock without a signal on the last session:\n%s", out.stdout)
	}
	if want := "Plano de operações — base: pregão de " + today.Format("02/01/2006"); !strings.Contains(out.stdout, want) {
		t.Errorf("output does not contain %q:\n%s", want, out.stdout)
	}
	if strings.Contains(out.stderr, "run `make sync` to update") {
		t.Errorf("warned about stale data for a session from today:\n%s", out.stderr)
	}
}

func TestCLIRecommendWithoutSignals(t *testing.T) {
	t.Parallel()
	db, cfg := newDatabase(t)
	insert(t, db, fixture.Trend("FLAT3", 400, fixture.Today().AddDate(0, 0, -10), nil))

	out := oracle(t, cfg, nil, "recommend", "-no-estimates")
	if out.code != 0 || !strings.Contains(out.stdout, "Nenhuma ação passou nos filtros hoje") {
		t.Errorf("want an empty plan:\n%s", out)
	}
	if !strings.Contains(out.stderr, "run `make sync` to update") {
		t.Errorf("want a warning about the last session being 10 days old:\n%s", out.stderr)
	}
}

func TestCLIFailures(t *testing.T) {
	t.Parallel()
	_, noTables := emptyDatabase(t)
	_, noQuotes := newDatabase(t)
	seeded, withQuotes := newDatabase(t)
	insert(t, seeded, fixture.Trend("WIN3", 300, fixture.Today(), nil))

	recent := fixture.Today().AddDate(0, 0, -50).Format(time.DateOnly)
	tomorrow := fixture.Today().AddDate(0, 0, 1).Format(time.DateOnly)
	unwritable := filepath.Join(t.TempDir(), "missing", "trades.csv")

	tests := []struct {
		name  string
		cfg   config.DBConfig
		env   []string
		args  []string
		code  int
		error string
	}{
		{"no command", noTables, nil, nil, 2, "usage: oracle <command>"},
		{"unknown command", noTables, nil, []string{"trade"}, 2, `unknown command "trade"`},
		{"unknown flag", noTables, nil, []string{"backtest", "-turbo"}, 2, "flag provided but not defined: -turbo"},
		{"invalid start date", noTables, nil, []string{"backtest", "-from", "31/12/2024"}, 1, "invalid -from"},
		{"invalid ranking", noTables, nil, []string{"backtest", "-rank", "luck"}, 1, `invalid -rank "luck"`},
		{"database unreachable", noTables, []string{"DB_PORT=1"}, []string{"recommend"}, 1, "failed to connect to database"},
		{"recommend before the first sync", noTables, nil, []string{"recommend", "-no-estimates"}, 1, "failed to load history"},
		{"recommend without quotes", noQuotes, nil, []string{"recommend", "-no-estimates"}, 1, "no quotes in the database"},
		{"backtest before the first sync", noTables, nil, []string{"backtest"}, 1, "failed to load history"},
		{"backtest without quotes", noQuotes, nil, []string{"backtest"}, 1, "backtest failed: backtest: no quotes"},
		{"backtest starting after the last quote", withQuotes, nil, []string{"backtest", "-from", tomorrow}, 1, "no quotes after the start date"},
		{"trades file cannot be written", withQuotes, nil, []string{"backtest", "-from", recent, "-csv", unwritable}, 1, "failed to write trades"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := oracle(t, tt.cfg, tt.env, tt.args...)
			if out.code != tt.code || !strings.Contains(out.stderr, tt.error) {
				t.Errorf("want exit %d with %q:\n%s", tt.code, tt.error, out)
			}
		})
	}
}

func TestCLIAPIHealth(t *testing.T) {
	t.Parallel()
	_, cfg := newDatabase(t)
	resp := get(t, startAPI(t, cfg)+"/health")
	defer resp.Body.Close()

	var body struct {
		Status string `json:"status"`
		Time   string `json:"time"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" || body.Status != "ok" {
		t.Errorf("got %d %q %+v", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if ts, err := time.Parse(time.RFC3339, body.Time); err != nil || time.Since(ts) > time.Minute {
		t.Errorf("time %q is not the current RFC 3339 time", body.Time)
	}
}

func TestCLIAPIShutsDownOnInterrupt(t *testing.T) {
	t.Parallel()
	_, cfg := newDatabase(t)
	port := freePort(t)
	cmd := exec.CommandContext(t.Context(), binary, "api")
	cmd.Env = append(env(cfg), "PORT="+strconv.Itoa(port))
	cmd.Dir = t.TempDir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitForAPI(t, fmt.Sprintf("http://127.0.0.1:%d", port), &stderr)

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil || !strings.Contains(stderr.String(), "shutting down") {
			t.Errorf("exit %v, want a clean shutdown\n%s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("api still running 10s after Ctrl+C\n%s", stderr.String())
	}
}

func TestCLIAPISwagger(t *testing.T) {
	t.Parallel()
	_, cfg := newDatabase(t)
	base := startAPI(t, cfg)

	ui := get(t, base+"/swagger/index.html")
	defer ui.Body.Close()
	if ui.StatusCode != http.StatusOK || !strings.HasPrefix(ui.Header.Get("Content-Type"), "text/html") {
		t.Errorf("swagger ui: got %d %q", ui.StatusCode, ui.Header.Get("Content-Type"))
	}

	doc := get(t, base+"/swagger/doc.json")
	defer doc.Body.Close()
	var spec struct {
		Info struct {
			Title string `json:"title"`
		} `json:"info"`
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.NewDecoder(doc.Body).Decode(&spec); err != nil {
		t.Fatalf("swagger doc.json: %v", err)
	}
	if doc.StatusCode != http.StatusOK || spec.Info.Title != "oracle API" || spec.Paths["/health"]["get"] == nil {
		t.Errorf("swagger doc.json: got %d, title %q, paths %v", doc.StatusCode, spec.Info.Title, spec.Paths)
	}
}

func TestCLIAPIQuotes(t *testing.T) {
	t.Parallel()
	conn, cfg := newDatabase(t)
	day := fixture.Day(2026, 10, 29)
	insert(t, conn, []quote.Quote{
		{Ticker: "PETR4", Name: "PETROBRAS", Spec: "PN      N2", ISIN: "BRPETRACNPR6", Date: day, Close: 38},
		{Ticker: "PETR4", Name: "PETROBRAS", Spec: "PN      N2", ISIN: "BRPETRACNPR6", Date: day.AddDate(0, 0, 1), Close: 38.5},
		{Ticker: "XPML11", Name: "XP MALLS", Spec: "CI", ISIN: "BRXPMLCTF000", Date: day, Close: 100},
	})
	base := startAPI(t, cfg)

	cases := []struct {
		path, want string
	}{
		{"/tickers", `"ticker":"XPML11"`},
		{"/tickers/petr4", `"sessions":2,"last_quote":{"ticker":"PETR4","name":"PETROBRAS","date":"2026-10-30"`},
		{"/tickers/PETR4/quotes?from=2026-10-30&to=2026-10-30", `[{"ticker":"PETR4","name":"PETROBRAS","date":"2026-10-30"`},
		{"/sessions/latest", `{"date":"2026-10-30","stale":`},
		{"/sessions/2026-10-29/quotes", `"ticker":"XPML11","name":"XP MALLS","date":"2026-10-29"`},
	}
	for _, c := range cases {
		resp := get(t, base+c.path)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), c.want) {
			t.Errorf("%s: got %d %s, want it to contain %s", c.path, resp.StatusCode, body, c.want)
		}
	}
	unknown := get(t, base+"/tickers/ZZZZ3")
	unknown.Body.Close()
	if unknown.StatusCode != http.StatusNotFound {
		t.Errorf("unknown ticker: got %d", unknown.StatusCode)
	}
}

func TestCLIAPIStrategy(t *testing.T) {
	t.Parallel()
	conn, cfg := newDatabase(t)
	today := fixture.Today()
	insert(t, conn, fixture.Trend("WIN3", 400, today, fixture.Dips(5), fixture.StrongPullback...), fixture.Trend("FLAT3", 400, today, nil))
	base := startAPI(t, cfg)

	cases := []struct {
		path, want string
	}{
		{"/strategy", `"oversold":10`},
		{"/strategy/tiers", `{"label":"forte","oversold":10,"rule":"RSI(2) < 10"}`},
		{"/tickers/WIN3/analysis", `"signal":true`},
		{"/tickers/WIN3/analysis", `"trades":5,"wins":5,"win_rate":1`},
		{"/tickers/WIN3/trades", `"signal_date":`},
		{"/signals", `"signals":[{"ticker":"WIN3"`},
	}
	for _, c := range cases {
		resp := get(t, base+c.path)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), c.want) {
			t.Errorf("%s: got %d %s, want it to contain %s", c.path, resp.StatusCode, body, c.want)
		}
	}
}

// Yahoo is a live service, so the plan is tested without it.
func TestCLIAPIPlan(t *testing.T) {
	t.Parallel()
	conn, cfg := newDatabase(t)
	today := fixture.Today()
	insert(t, conn, fixture.Trend("WIN3", 400, today, fixture.Dips(5), fixture.StrongPullback...), fixture.Trend("FLAT3", 400, today, nil))
	base := startAPI(t, cfg)

	cases := []struct {
		path, want string
	}{
		{"/recommendations?estimates=false&capital=1000", `"capital":1000,"top":5,"risk":2,"per_stock":200,"max_loss":20`},
		{"/recommendations?estimates=false", `"analyst_estimates":false,"picks":[{"ticker":"WIN3","name":"WIN3 SA","tier":"forte"`},
		{"/recommendations/tiers", `{"label":"forte","rule":"RSI(2) < 10","candidates":1,"trades":5`},
	}
	for _, c := range cases {
		resp := get(t, base+c.path)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), c.want) {
			t.Errorf("%s: got %d %s, want it to contain %s", c.path, resp.StatusCode, body, c.want)
		}
	}
}

func startAPI(t *testing.T, cfg config.DBConfig, extraEnv ...string) string {
	t.Helper()
	port := freePort(t)

	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, binary, "api")
	cmd.Env = append(append(env(cfg), "PORT="+strconv.Itoa(port)), extraEnv...)
	cmd.Dir = t.TempDir()
	// Ctrl+C, so it exits cleanly and writes its coverage.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForAPI(t, base, &stderr)
	return base
}

func waitForAPI(t *testing.T, base string, stderr *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/health", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("api did not start: %v\n%s", err, stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
