package control

import "net/http"

// CORSMiddleware sets cross-origin resource sharing headers on every response.
// When allowedOrigins is non-empty, only requests whose Origin header matches
// one of the listed origins receive a permissive Access-Control-Allow-Origin.
// When allowedOrigins is nil or empty, all origins are allowed (wildcard "*").
//
// For OPTIONS preflight requests, it returns 204 No Content immediately
// without invoking the inner handler. All other methods pass through normally.
func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if len(allowedOrigins) == 0 {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				for _, allowed := range allowedOrigins {
					if origin == allowed {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Set("Vary", "Origin")
						break
					}
				}
			}

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
}
