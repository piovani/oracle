// Package handler has the HTTP routes of oracle api.
package handler

import (
	"encoding/json"
	"log"
	"net/http"
)

type errorResponse struct {
	Error string `json:"error" example:"ticker ZZZZ3 not found"`
} //	@name	Error

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // keep "RSI(2) < 10" readable
	if err := enc.Encode(body); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// writeInternal logs the error and hides it from the client.
func writeInternal(w http.ResponseWriter, op string, err error) {
	log.Printf("%s: %v", op, err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
