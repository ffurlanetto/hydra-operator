package logging

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// New creates a zerolog.Logger configured from level and format.
// console format emits human-readable output; otherwise JSON.
func New(level, format string) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339

	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil {
		lvl = zerolog.InfoLevel
	}

	if strings.ToLower(format) == "console" {
		return zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
			Level(lvl).
			With().
			Timestamp().
			Logger()
	}

	return zerolog.New(os.Stdout).
		Level(lvl).
		With().
		Timestamp().
		Logger()
}
