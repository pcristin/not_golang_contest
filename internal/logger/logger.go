package logger

import (
	"context"
	"log/slog"
)

// Type for context key for request ID and source in logger
type contextKey string

// Context keys for logging
const (
	RequestIDKey contextKey = "request_id"
	SourceKey    contextKey = "source"
)

// FromContext extracts the request ID or source from the context and returns a logger with the module
func FromContext(ctx context.Context, module string) *slog.Logger {
	// Try request ID first (HTTP requests)
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok && requestID != "" {
		return slog.With("request_id", requestID, "module", module)
	}

	// Try source (background tasks)
	if source, ok := ctx.Value(SourceKey).(string); ok && source != "" {
		return slog.With("source", source, "module", module)
	}

	// Fallback
	return slog.With("source", "unknown", "module", module)
}
