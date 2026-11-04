//go:build integration

package integration

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"oracle/test/fixture"
)

// The API on its own port, the way Prometheus sees it: every route counted by pattern, the
// cache counted, the connection pool exported, and nothing of the API reachable on that port.
func TestCLIAPIExportsMetricsOnItsOwnPort(t *testing.T) {
	t.Parallel()
	conn, cfg := newDatabase(t)
	insert(t, conn, fixture.Trend("WIN3", 300, fixture.Today(), nil))
	metricsPort := freePort(t)
	base := startAPI(t, cfg, "METRICS_PORT="+strconv.Itoa(metricsPort))
	metrics := "http://127.0.0.1:" + strconv.Itoa(metricsPort)

	cacheState(t, base+"/tickers") // miss
	cacheState(t, base+"/tickers") // hit
	cacheState(t, base+"/tickers/WIN3")
	if code := status(t, base+"/nope"); code != http.StatusNotFound {
		t.Fatalf("/nope answered %d", code)
	}

	resp := get(t, metrics+"/metrics")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`oracle_http_requests_total{method="GET",route="GET /tickers",status="200"} 2`,
		`oracle_http_requests_total{method="GET",route="GET /tickers/{ticker}",status="200"} 1`,
		`oracle_http_requests_total{method="GET",route="unmatched",status="404"} 1`,
		`oracle_cache_requests_total{result="hit",route="GET /tickers"} 1`,
		`oracle_cache_requests_total{result="miss",route="GET /tickers"} 1`,
		`oracle_http_request_duration_seconds_count{method="GET",route="GET /tickers"} 2`,
		`go_sql_open_connections{db_name="oracle"}`,
		`go_goroutines`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics lacks %s", want)
		}
	}
	if strings.Contains(string(body), "/tickers/WIN3") {
		t.Error("a concrete URL became a label value")
	}

	if code := status(t, metrics+"/tickers"); code != http.StatusNotFound {
		t.Errorf("the API answered on the metrics port: %d", code)
	}
	if code := status(t, base+"/metrics"); code != http.StatusNotFound {
		t.Errorf("/metrics is exposed on the API port: %d", code)
	}
}

func TestCLIAPIKeepsRunningWhenTheMetricsPortIsTaken(t *testing.T) {
	t.Parallel()
	_, cfg := newDatabase(t)
	taken := freePort(t)
	hold := listen(t, taken) // someone else owns the metrics port
	defer hold.Close()

	base := startAPI(t, cfg, "METRICS_PORT="+strconv.Itoa(taken))

	if code := status(t, base+"/health"); code != http.StatusOK {
		t.Errorf("the API stopped answering: %d", code)
	}
}

// listen holds a port on every interface, as the API's metrics server would.
func listen(t *testing.T, port int) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", ":"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// status is the status code of a GET.
func status(t *testing.T, url string) int {
	t.Helper()
	resp := get(t, url)
	defer resp.Body.Close()
	return resp.StatusCode
}
