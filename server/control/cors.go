package control

import "net/http"

// CORSMiddleware sets cross-origin resource sharing headers on every response.
// In dev mode, the Vite UI (localhost:5173) and the API server (QUIC :8080)
// are different origins, so browsers require CORS headers to allow requests.
//
// For OPTIONS preflight requests, it returns 204 No Content immediately
// without invoking the inner handler. All other methods pass through normally.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
