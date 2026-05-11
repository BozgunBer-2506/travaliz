package middleware

import (
	"log"
	"net/http"
	"time"
)

// LoggingMiddleware logs the details of each incoming HTTP request.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Call the next handler in the chain
		next.ServeHTTP(w, r)

		// Log request details: Method, Path, and Duration
		log.Printf(
			"Method: %s | Path: %s | Duration: %v",
			r.Method,
			r.URL.Path,
			time.Since(start),
		)
	})
}
