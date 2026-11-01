package command

import (
	"log"
	"net/http"
	"time"

	_ "oracle/docs" // swagger spec
	"oracle/internal/bootstrap"
	"oracle/internal/handler"

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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)
	mux.Handle("GET /swagger/", httpSwagger.Handler(httpSwagger.URL("/swagger/doc.json")))

	server := &http.Server{
		Addr:              ":" + app.Config.Port,
		Handler:           mux,
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
