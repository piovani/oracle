package command

import (
	"log"
	"net/http"
	"time"

	_ "oracle/docs" // swagger spec
	"oracle/internal/bootstrap"
	"oracle/internal/handler"
	"oracle/internal/quote"

	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// API starts the HTTP server.
//
//	@title			oracle API
//	@version		1.0
//	@description	HTTP API of the oracle, started with `oracle api`.
//	@description	The server listens on `PORT` (default 8080) and connects to Postgres before it starts listening: if the database is unreachable, the process exits and nothing answers.
//	@description	There is no authentication. Routes that don't exist answer 404, and known routes called with the wrong method answer 405 with an `Allow` header, both in plain text.
//	@BasePath		/
func API(_ []string) {
	_, app := bootstrap.Start()
	defer app.Close()
	log.Println("database connected")

	server := &http.Server{
		Addr:              ":" + app.Config.Port,
		Handler:           newMux(quote.NewPostgres(app.DB), time.Now),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("server listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil {
		app.Fatalf("server error: %v", err)
	}
}

func newMux(store handler.QuoteReader, now func() time.Time) *http.ServeMux {
	quotes := handler.NewQuotes(store, now)
	mux := http.NewServeMux()
	// Infra
	mux.HandleFunc("GET /health", handler.Health)
	mux.Handle("GET /swagger/", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))

	// Tickers
	mux.HandleFunc("GET /tickers", quotes.Tickers)
	mux.HandleFunc("GET /tickers/{ticker}", quotes.Ticker)
	mux.HandleFunc("GET /tickers/{ticker}/quotes", quotes.TickerQuotes)

	// Sessions
	mux.HandleFunc("GET /sessions/latest", quotes.LatestSession)
	mux.HandleFunc("GET /sessions/{date}/quotes", quotes.SessionQuotes)
	return mux
}
