package command

import (
	"log"
	"net/http"
	"time"

	"oracle/internal/bootstrap"
	"oracle/internal/handler"
)

// API starts the HTTP server.
func API(_ []string) {
	_, app := bootstrap.Start()
	defer app.Close()
	log.Println("database connected")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)

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
