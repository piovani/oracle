package main

import (
	"fmt"
	"log"
	"net/http"
	"oracle/internal/bootstrap"
	"oracle/internal/handler"
)

func main() {
	_, app := bootstrap.Start()
	defer app.Close()
	log.Println("database connected")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)

	addr := fmt.Sprintf(":%s", app.Config.Port)
	log.Printf("server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
