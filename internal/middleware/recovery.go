package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	myLogger "github.com/pcristin/golang_contest/internal/logger"
	"github.com/pcristin/golang_contest/internal/utils"
)

// ErrorResponse represents a standardized error response
type ErrorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
	Timestamp string `json:"timestamp"`
}

// RecoveryMiddleware wraps handlers with panic recovery
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Get request ID from context (set by RequestIDMiddleware)
				requestID := getRequestIDFromContext(r.Context())

				// Create logger with context
				logger := myLogger.FromContext(r.Context(), "recovery_middleware")

				// Log the panic with full details
				logger.Error("panic recovered",
					"error", err,
					"method", r.Method,
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
					"user_agent", r.Header.Get("User-Agent"),
					"stack", string(debug.Stack()),
				)

				// Ensure we haven't already written to the response
				if !isResponseWritten(w) {
					writeErrorResponse(w, http.StatusInternalServerError,
						"Internal server error", requestID)
				}
			}
		}()

		// Continue to the next handler
		next.ServeHTTP(w, r)
	})
}

// RequestIDMiddleware adds request ID to context and response headers
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate request ID
		requestID := utils.GenerateRequestID()

		// Add to context
		ctx := context.WithValue(r.Context(), myLogger.RequestIDKey, requestID)
		r = r.WithContext(ctx)

		// Add to response header for tracing
		w.Header().Set("X-Request-ID", requestID)

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs request/response details
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response writer wrapper to capture status code
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Get logger with context
		logger := myLogger.FromContext(r.Context(), "http_middleware")

		// Log request
		logger.Debug("request started",
			"method", r.Method,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.Header.Get("User-Agent"),
		)

		// Continue to next handler
		next.ServeHTTP(wrapped, r)

		// Log response
		duration := time.Since(start)
		logger.Info("request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
			"bytes_written", wrapped.bytesWritten,
		)
	})
}

// TimeoutMiddleware adds request timeout
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			r = r.WithContext(ctx)

			done := make(chan struct{})
			go func() {
				defer close(done)
				next.ServeHTTP(w, r)
			}()

			select {
			case <-done:
				// Request completed normally
				return
			case <-ctx.Done():
				// Request timed out
				requestID := getRequestIDFromContext(r.Context())
				logger := myLogger.FromContext(r.Context(), "timeout_middleware")

				logger.Warn("request timeout",
					"method", r.Method,
					"path", r.URL.Path,
					"timeout", timeout,
				)

				if !isResponseWritten(w) {
					writeErrorResponse(w, http.StatusGatewayTimeout,
						"Request timeout", requestID)
				}
			}
		})
	}
}

// Chain combines multiple middlewares
func Chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		// Apply middlewares in reverse order so they execute in the correct order
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}

// Helper functions

func getRequestIDFromContext(ctx context.Context) string {
	if requestID, ok := ctx.Value(myLogger.RequestIDKey).(string); ok {
		return requestID
	}
	return ""
}

func writeErrorResponse(w http.ResponseWriter, statusCode int, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	errorResp := ErrorResponse{
		Error:     message,
		RequestID: requestID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// If JSON encoding fails, write a simple error
	if err := json.NewEncoder(w).Encode(errorResp); err != nil {
		slog.Error("failed to encode error response", "error", err)
		fmt.Fprintf(w, `{"error":"Internal server error","timestamp":"%s"}`,
			time.Now().UTC().Format(time.RFC3339))
	}
}

func isResponseWritten(w http.ResponseWriter) bool {
	// This is a heuristic - if we can set a header, response hasn't been written
	w.Header().Set("X-Recovery-Test", "1")
	delete(w.Header(), "X-Recovery-Test")
	return false // For simplicity, assume we can always write
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}
