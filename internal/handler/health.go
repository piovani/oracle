package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type healthResponse struct {
	Status string `json:"status" example:"ok"`                 // always "ok"
	Time   string `json:"time" example:"2026-11-01T22:07:11Z"` // current server time, UTC, RFC 3339
} //	@name	HealthResponse

// Health reports that the server is up, with the current server time.
//
//	@Summary		Health check
//	@Description	Reports that the server is up, with the current server time in UTC (RFC 3339).
//	@Description	The database connection is checked once at startup, not on every call, so a 200 means the process is running and was able to connect when it started.
//	@Description	No side effects; safe to call repeatedly, for example from a load balancer or monitor.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	healthResponse
//	@Failure		405	{string}	string	"Method Not Allowed: only GET and HEAD are accepted (Allow: GET, HEAD)"
//	@Router			/health [get]
func Health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(healthResponse{
		Status: "ok",
		Time:   time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		log.Printf("health: failed to write response: %v", err)
	}
}
