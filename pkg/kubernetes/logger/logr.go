// Package logger provides a logging capability for toolhive for running locally as a CLI and in Kubernetes
package logger

import (
	"github.com/go-logr/logr"
)

// NewLogr returns a logr.Logger which uses the singleton logger.
func NewLogr() logr.Logger {
	return logr.New(&toolhiveLogSink{logger: log})
}

// toolhiveLogSink adapts our logger to the logr.LogSink interface
type toolhiveLogSink struct {
	logger Logger
	name   string
}

// Init implements logr.LogSink
func (*toolhiveLogSink) Init(logr.RuntimeInfo) {
	// Nothing to do
}

// Enabled implements logr.LogSink
func (*toolhiveLogSink) Enabled(int) bool {
	// Always enable logging
	return true
}

// Info implements logr.LogSink
func (l *toolhiveLogSink) Info(_ int, msg string, keysAndValues ...interface{}) {
	l.logger.Info(msg, keysAndValues...)
}

// Error implements logr.LogSink
func (l *toolhiveLogSink) Error(err error, msg string, keysAndValues ...interface{}) {
	args := append([]interface{}{"error", err}, keysAndValues...)
	l.logger.Error(msg, args...)
}

// WithValues implements logr.LogSink
func (l *toolhiveLogSink) WithValues(keysAndValues ...interface{}) logr.LogSink {
	// Create a new logger with the additional key-value pairs
	if slogger, ok := l.logger.(*slogLogger); ok {
		newLogger := &slogLogger{
			logger: slogger.logger.With(keysAndValues...),
		}
		return &toolhiveLogSink{
			logger: newLogger,
			name:   l.name,
		}
	}

	// If we can't add the values, just return a sink with the same logger
	return &toolhiveLogSink{
		logger: l.logger,
		name:   l.name,
	}
}

// WithName implements logr.LogSink
func (l *toolhiveLogSink) WithName(name string) logr.LogSink {
	// If we already have a name, append the new name
	newName := name
	if l.name != "" {
		newName = l.name + "/" + name
	}

	// Create a new sink with the component logger
	return &toolhiveLogSink{
		logger: GetLogger(newName),
		name:   newName,
	}
}
