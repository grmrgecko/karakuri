package logger

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type contextKey string

const correlationIDKey contextKey = "correlation_id"

// New creates a zap logger configured for the given level and output format.
// Format "console" uses colored development output; anything else uses JSON.
func New(level, format string) (*zap.Logger, error) {
	var cfg zap.Config

	if format == "console" {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		cfg = zap.NewProductionConfig()
	}

	parsedLevel, err := zapcore.ParseLevel(level)
	if err != nil {
		parsedLevel = zapcore.InfoLevel
	}
	cfg.Level = zap.NewAtomicLevelAt(parsedLevel)

	// The development config attaches stack traces from warn upward, which
	// buries actionable operator warnings in engine frames. Only failures are
	// worth a trace.
	return cfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
}

// WithCorrelationID stores a correlation ID in the context for request tracing.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationID retrieves the correlation ID from the context, returning empty if absent.
func CorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}
	return ""
}

// FromContext returns a child logger with the correlation ID field attached.
func FromContext(ctx context.Context, l *zap.Logger) *zap.Logger {
	if id := CorrelationID(ctx); id != "" {
		return l.With(zap.String("correlation_id", id))
	}
	return l
}
