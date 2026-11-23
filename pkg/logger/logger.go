// Package logger builds the process-wide slog logger.
package logger

import (
	"fmt"
	"io"
	"log/slog"
)

// New returns a logger writing to w. level is debug|info|warn|error, format is json|text.
func New(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level %q: %w", level, err)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("log format %q: want json or text", format)
	}
}
