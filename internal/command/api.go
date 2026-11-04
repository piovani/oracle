package command

import (
	"context"
	"log"
	"net/http"
	"time"

	_ "oracle/docs" // swagger spec
	"oracle/internal/bootstrap"
	"oracle/internal/cache"
	"oracle/internal/handler"
	"oracle/internal/plan"
	"oracle/internal/quote"
	"oracle/internal/yahoo"

	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// API starts the HTTP server. Ctrl+C stops it after the requests in flight.
//
//	@title			oracle API
//	@version		1.0
//	@description	HTTP API of the oracle, started with `oracle api`.
//	@description	The server listens on `PORT` (default 8080) and connects to Postgres before it starts listening: if the database is unreachable, the process exits and nothing answers.
//	@description	There is no authentication. Routes that don't exist answer 404, and known routes called with the wrong method answer 405 with an `Allow` header, both in plain text.
//	@description	The answers of the data routes (everything but `/health`, `/strategy` and `/strategy/tiers`) are cached in Redis for 1 minute, per path and query, and the sync command empties that cache when it stores new quotes. The `X-Cache` header says `HIT` or `MISS`. If Redis is down the routes answer as usual, without the cache.
//	@description	Any route answers 503 {"error": ...} when it takes longer than 9 seconds. The ticker list and the history behind the strategy and plan routes are cached until a new session is stored, so the first call after a sync is the slow one (a few seconds) and the next take milliseconds.
//	@description	Ctrl+C (SIGINT) stops the server after the requests in flight finish.
//	@BasePath		/
func API(_ []string) {
	ctx, app := bootstrap.Start()
	defer app.Close()
	log.Println("database connected")

	server := &http.Server{
		Addr:              ":" + app.Config.Port,
		Handler:           withTimeout(newMux(quote.NewPostgres(app.DB), time.Now, connectYahoo, app.Cache), requestTimeout),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	log.Printf("server listening on %s", server.Addr)

	select {
	case err := <-serveErr:
		app.Fatalf("server error: %v", err)
	case <-ctx.Done():
	}
	log.Println("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		app.Fatalf("shutdown: %v", err)
	}
}

const (
	requestTimeout  = 9 * time.Second // under WriteTimeout, which would just drop the connection
	shutdownTimeout = 15 * time.Second
)

func withTimeout(next http.Handler, timeout time.Duration) http.Handler {
	timed := http.TimeoutHandler(next, timeout, `{"error":"the request took too long, try again in a moment"}`)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the timeout answer keeps this; the others set their own.
		w.Header().Set("Content-Type", "application/json")
		timed.ServeHTTP(w, r)
	})
}

func connectYahoo(ctx context.Context) (plan.Estimator, error) {
	client, err := yahoo.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// cacheTTL is how long the cache keeps an answer. The sync command empties it sooner, when new quotes arrive.
const cacheTTL = time.Minute

// newMux registers the routes. Answers of the data routes are kept in c for cacheTTL; a nil c turns the cache off.
func newMux(store handler.StrategyReader, now func() time.Time, connect handler.Connect, c cache.Cache) *http.ServeMux {
	cached := handler.NewResponseCache(c, cacheTTL).Wrap
	histories := handler.NewHistories(store, now)
	quotes := handler.NewQuotes(store, now)
	strategy := handler.NewStrategy(store, histories, now)
	plans := handler.NewPlan(store, histories, now, connect)
	mux := http.NewServeMux()
	// Infra
	mux.HandleFunc("GET /health", handler.Health)
	mux.Handle("GET /swagger/", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))

	// Tickers
	mux.HandleFunc("GET /tickers", cached(quotes.Tickers))
	mux.HandleFunc("GET /tickers/{ticker}", cached(quotes.Ticker))
	mux.HandleFunc("GET /tickers/{ticker}/quotes", cached(quotes.TickerQuotes))

	// Sessions
	mux.HandleFunc("GET /sessions/latest", cached(quotes.LatestSession))
	mux.HandleFunc("GET /sessions/{date}/quotes", cached(quotes.SessionQuotes))

	// Strategy
	mux.HandleFunc("GET /strategy", strategy.Params)
	mux.HandleFunc("GET /strategy/tiers", strategy.Tiers)
	mux.HandleFunc("GET /tickers/{ticker}/analysis", cached(strategy.Analysis))
	mux.HandleFunc("GET /tickers/{ticker}/trades", cached(strategy.Trades))
	mux.HandleFunc("GET /signals", cached(strategy.Signals))

	// Plan
	mux.HandleFunc("GET /recommendations", cached(plans.Recommendations))
	mux.HandleFunc("GET /recommendations/tiers", cached(plans.RecommendationTiers))
	mux.HandleFunc("GET /tickers/{ticker}/estimate", cached(plans.Estimate))
	return mux
}
