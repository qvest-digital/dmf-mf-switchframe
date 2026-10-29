package control

import "net/http"

// corsAllowMethods lists every method the API registers a route for: a
// browser on one engine's origin preflights each non-simple call it makes to
// the other engine, and a method missing here fails there before the request
// is sent.
const corsAllowMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"

// corsAllowHeaders lists every request header the UI sets on an API call.
const corsAllowHeaders = "Content-Type, Authorization"

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

			w.Header().Set("Access-Control-Allow-Methods", corsAllowMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
