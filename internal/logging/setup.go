package logging

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
)

// InitLogging configures the global zerolog logger based on CLI flags
func InitLogging(cmd *cli.Command) error {
	var writers []io.Writer

	// Configure log format
	if cmd.Bool("log.pretty") {
		// Pretty console output for development
		consoleWriter := zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: time.RFC3339,
			NoColor:    false,
		}
		writers = append(writers, consoleWriter)
	} else {
		// JSON output for production
		writers = append(writers, os.Stderr)
	}

	// Optional: Add file logging if configured
	if logFile := cmd.String("log.file"); logFile != "" {
		if err := ensureLogDirectory(logFile); err != nil {
			log.Warn().Err(err).Str("file", logFile).Msg("Failed to create log directory")
		} else {
			file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
			if err != nil {
				log.Warn().Err(err).Str("file", logFile).Msg("Failed to open log file")
			} else {
				writers = append(writers, file)
			}
		}
	}

	// Setup multi-writer if needed
	var output io.Writer
	if len(writers) > 1 {
		output = zerolog.MultiLevelWriter(writers...)
	} else {
		if len(writers) == 1 {
			output = writers[0]
		} else {
			output = os.Stderr
		}
	}

	// Configure log level first
	level := getLogLevel(cmd)
	zerolog.SetGlobalLevel(level)

	// Create logger with conditional caller information
	logContext := zerolog.New(output).With().Timestamp()

	// Only add caller information in debug or trace mode
	if level <= zerolog.DebugLevel {
		logContext = logContext.Caller()
	}

	log.Logger = logContext.Logger()

	// Configure zerolog global settings
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.TimestampFieldName = "timestamp"
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "message"
	zerolog.ErrorFieldName = "error"
	zerolog.CallerFieldName = "caller"

	// Set duration format to milliseconds for better readability
	zerolog.DurationFieldUnit = time.Millisecond
	zerolog.DurationFieldInteger = false

	// Log initial configuration
	log.Info().
		Str("level", level.String()).
		Bool("pretty", cmd.Bool("log.pretty")).
		Bool("caller_enabled", level <= zerolog.DebugLevel).
		Msg("Logging initialized")

	switch level {
	case zerolog.DebugLevel:
		log.Debug().Msg("Debug logging enabled - verbose output active")
	case zerolog.TraceLevel:
		log.Trace().Msg("Trace logging enabled - maximum verbosity active")
	}

	return nil
}

// getLogLevel determines the appropriate log level based on CLI flags
func getLogLevel(cmd *cli.Command) zerolog.Level {
	if cmd.Bool("log.trace") {
		return zerolog.TraceLevel
	}
	if cmd.Bool("log.debug") {
		return zerolog.DebugLevel
	}
	if cmd.Bool("log.warn") {
		return zerolog.WarnLevel
	}
	if cmd.Bool("log.error") {
		return zerolog.ErrorLevel
	}

	// Default to Info level
	return zerolog.InfoLevel
}

// ensureLogDirectory creates the directory for the log file if it doesn't exist
func ensureLogDirectory(logFile string) error {
	dir := filepath.Dir(logFile)
	if dir != "." && dir != "/" {
		return os.MkdirAll(dir, 0755)
	}
	return nil
}

// ContextualLogger creates a child logger with additional context fields
func ContextualLogger(component string) zerolog.Logger {
	return log.With().Str("component", component).Logger()
}

// HTTPLogger creates a logger specifically for HTTP request logging
func HTTPLogger(method, path, remoteAddr string) zerolog.Logger {
	return log.With().
		Str("method", method).
		Str("path", path).
		Str("remote_addr", remoteAddr).
		Logger()
}

// ErrorLogger creates a logger with error context
func ErrorLogger(err error, component string) zerolog.Logger {
	return log.With().
		Err(err).
		Str("component", component).
		Logger()
}
