// Package logger provides a logging capability for toolhive for running locally as a CLI and in Kubernetes
package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/lmittmann/tint"
	"github.com/spf13/viper"
)

// Log is a global logger instance
var log Logger

// Debug logs a message at debug level using the singleton logger.
func Debug(msg string, args ...any) {
	log.Debug(msg, args...)
}

// Debugf logs a message at debug level using the singleton logger.
func Debugf(msg string, args ...any) {
	log.Debugf(msg, args...)
}

// Info logs a message at info level using the singleton logger.
func Info(msg string, args ...any) {
	log.Info(msg, args...)
}

// Infof logs a message at info level using the singleton logger.
func Infof(msg string, args ...any) {
	log.Infof(msg, args...)
}

// Warn logs a message at warning level using the singleton logger.
func Warn(msg string, args ...any) {
	log.Warn(msg, args...)
}

// Warnf logs a message at warning level using the singleton logger.
func Warnf(msg string, args ...any) {
	log.Warnf(msg, args...)
}

// Error logs a message at error level using the singleton logger.
func Error(msg string, args ...any) {
	log.Error(msg, args...)
}

// Errorf logs a message at error level using the singleton logger.
func Errorf(msg string, args ...any) {
	log.Errorf(msg, args...)
}

// Panic logs a message at error level using the singleton logger and panics the program.
func Panic(msg string) {
	log.Panic(msg)
}

// Panicf logs a message at error level using the singleton logger and panics the program.
func Panicf(msg string, args ...any) {
	log.Panicf(msg, args...)
}

// Logger provides a unified interface for logging
type Logger interface {
	Debug(msg string, args ...any)
	Debugf(msg string, args ...any)
	Info(msg string, args ...any)
	Infof(msg string, args ...any)
	Warn(msg string, args ...any)
	Warnf(msg string, args ...any)
	Error(msg string, args ...any)
	Errorf(msg string, args ...any)
	Panic(msg string)
	Panicf(msg string, args ...any)
}

// Implementation using slog
type slogLogger struct {
	logger *slog.Logger
}

func (l *slogLogger) Debugf(msg string, args ...any) {
	l.logger.Debug(fmt.Sprintf(msg, args...))
}

func (l *slogLogger) Infof(msg string, args ...any) {
	l.logger.Info(fmt.Sprintf(msg, args...))
}

func (l *slogLogger) Warnf(msg string, args ...any) {
	l.logger.Warn(fmt.Sprintf(msg, args...))
}

func (l *slogLogger) Errorf(msg string, args ...any) {
	l.logger.Error(fmt.Sprintf(msg, args...))
}

func (l *slogLogger) Panicf(msg string, args ...any) {
	l.Panic(fmt.Sprintf(msg, args...))
}

func (l *slogLogger) Debug(msg string, args ...any) {
	l.logger.Debug(msg, args...)
}

func (l *slogLogger) Info(msg string, args ...any) {
	l.logger.Info(msg, args...)
}

func (l *slogLogger) Warn(msg string, args ...any) {
	l.logger.Warn(msg, args...)
}

func (l *slogLogger) Error(msg string, args ...any) {
	l.logger.Error(msg, args...)
}

func (l *slogLogger) Panic(msg string) {
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:]) // skip [Callers, Panic]
	record := slog.NewRecord(time.Now(), slog.LevelError, msg, pcs[0])
	_ = l.logger.Handler().Handle(context.Background(), record)
	panic(msg)
}

func unstructuredLogs() bool {
	unstructuredLogs, err := strconv.ParseBool(os.Getenv("UNSTRUCTURED_LOGS"))
	if err != nil {
		// at this point if the error is not nil, the env var wasn't set, or is ""
		// which means we just default to outputting unstructured logs.
		return true
	}
	return unstructuredLogs
}

// Initialize creates and configures the appropriate logger.
// If the UNSTRUCTURED_LOGS is set to true, it will output plain log message
// with only time and LogLevelType (INFO, DEBUG, ERROR, WARN)).
// Otherwise it will create a standard structured slog logger
func Initialize() {
	if unstructuredLogs() {
		w := os.Stderr

		handler := tint.NewHandler(w, &tint.Options{
			Level:      getLogLevel(),
			TimeFormat: time.Kitchen,
		})

		slogger := slog.New(handler)

		slog.SetDefault(slogger)
		log = &slogLogger{logger: slogger}
	} else {
		w := os.Stdout

		handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: getLogLevel(),
		})

		slogger := slog.New(handler)

		slog.SetDefault(slogger)
		log = &slogLogger{logger: slogger}
	}
}

// GetLogger returns a context-specific logger
func GetLogger(component string) Logger {
	if slogger, ok := log.(*slogLogger); ok {
		return &slogLogger{
			logger: slogger.logger.With("component", component),
		}
	}

	return log
}

// getLogLevel returns the appropriate slog.Level based on the debug flag
func getLogLevel() slog.Level {
	var level slog.Level
	if viper.GetBool("debug") {
		level = slog.LevelDebug
	} else {
		level = slog.LevelInfo
	}
	return level
}
