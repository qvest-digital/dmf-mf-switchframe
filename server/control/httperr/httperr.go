// Package httperr provides helpers for writing JSON error responses.
package httperr

import (
	"encoding/json"
	"net/http"
)

// Write sends a JSON {"error":"msg"} response with the given status code.
func Write(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// WriteErr sends a JSON {"error":"err.Error()"} response with the given status code.
func WriteErr(w http.ResponseWriter, status int, err error) {
	Write(w, status, err.Error())
}
