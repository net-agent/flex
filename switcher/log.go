package switcher

import (
	"context"
	"log/slog"
)

// LogConfig controls the minimum log level for each sub-module.
// A nil *LogConfig passed to NewServer uses DefaultLogConfig().
type LogConfig struct {
	Server   slog.Level // default: Info
	Registry slog.Level // default: Warn
	Router   slog.Level // default: Warn
	Context  slog.Level // default: Warn
}

// DefaultLogConfig returns a LogConfig where Server logs at Info
// and all other modules log at Warn to reduce noise in production.
func DefaultLogConfig() LogConfig {
	return LogConfig{
		Server:   slog.LevelInfo,
		Registry: slog.LevelWarn,
		Router:   slog.LevelWarn,
		Context:  slog.LevelWarn,
	}
}

// levelHandler wraps an slog.Handler with a minimum level filter.
type levelHandler struct {
	level slog.Level
	inner slog.Handler
}

func (h *levelHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level && h.inner.Enabled(context.Background(), l)
}

func (h *levelHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.inner.Handle(ctx, r)
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{level: h.level, inner: h.inner.WithAttrs(attrs)}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{level: h.level, inner: h.inner.WithGroup(name)}
}

// newModuleLogger creates a logger with a minimum level filter and a "module" attribute.
func newModuleLogger(base *slog.Logger, level slog.Level, module string) *slog.Logger {
	return slog.New(&levelHandler{
		level: level,
		inner: base.Handler(),
	}).With("module", module)
}
