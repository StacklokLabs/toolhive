package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestUnstructuredLogsCheck(t *testing.T) { //nolint:paralleltest // Uses environment variables
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{"Default Case", "", true},
		{"Explicitly True", "true", true},
		{"Explicitly False", "false", false},
		{"Invalid Value", "not-a-bool", true},
	}

	for _, tt := range tests { //nolint:paralleltest // Uses environment variables
		t.Run(tt.name, func(t *testing.T) { //nolint:paralleltest // Uses environment variables
			// Set environment variable
			if tt.envValue != "" {
				os.Setenv("UNSTRUCTURED_LOGS", tt.envValue)
				defer os.Unsetenv("UNSTRUCTURED_LOGS")
			} else {
				os.Unsetenv("UNSTRUCTURED_LOGS")
			}

			if got := unstructuredLogs(); got != tt.expected {
				t.Errorf("unstructuredLogs() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStructuredLogger(t *testing.T) { //nolint:paralleltest // Uses environment variables
	unformattedLogTestCases := []struct {
		level    string // The log level to test
		message  string // The message to log
		key      string // Key for structured logging
		value    string // Value for structured logging
		contains bool   // Whether to check if output contains the message
	}{
		{"DEBUG", "debug message", "key", "value", true},
		{"INFO", "info message", "key", "value", true},
		{"WARN", "warn message", "key", "value", true},
		{"ERROR", "error message", "key", "value", true},
	}

	for _, tc := range unformattedLogTestCases {
		t.Run("NonFormattedLogs", func(t *testing.T) {

			// we create a pipe to capture the output of the log
			// so we can test that the logger logs the right message
			originalStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			os.Setenv("UNSTRUCTURED_LOGS", "false")
			defer os.Unsetenv("UNSTRUCTURED_LOGS")

			viper.SetDefault("debug", true)

			Initialize()
			os.Stdout = originalStdout

			// Log the message based on the level
			switch tc.level {
			case "DEBUG":
				log.Debug(tc.message, tc.key, tc.value)
			case "INFO":
				log.Info(tc.message, tc.key, tc.value)
			case "WARN":
				log.Warn(tc.message, tc.key, tc.value)
			case "ERROR":
				log.Error(tc.message, tc.key, tc.value)
			}

			w.Close()

			// Read the captured output
			var capturedOutput bytes.Buffer
			io.Copy(&capturedOutput, r)
			output := capturedOutput.String()

			// Parse JSON output
			var logEntry map[string]interface{}
			if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
				t.Fatalf("Failed to parse JSON log output: %v", err)
			}

			// Check level
			if level, ok := logEntry["level"].(string); !ok || level != tc.level {
				t.Errorf("Expected level %s, got %v", tc.level, logEntry["level"])
			}

			// Check message
			if msg, ok := logEntry["msg"].(string); !ok || msg != tc.message {
				t.Errorf("Expected message %s, got %v", tc.message, logEntry["msg"])
			}

			// Check key-value pair
			if value, ok := logEntry[tc.key].(string); !ok || value != tc.value {
				t.Errorf("Expected %s=%s, got %v", tc.key, tc.value, logEntry[tc.key])
			}
		})
	}

	formattedLogTestCases := []struct {
		level    string
		message  string
		key      string
		value    string
		expected string
		contains bool
	}{
		{"DEBUG", "debug message %s and %s", "key", "value", "debug message key and value", true},
		{"INFO", "info message %s and %s", "key", "value", "info message key and value", true},
		{"WARN", "warn message %s and %s", "key", "value", "warn message key and value", true},
		{"ERROR", "error message %s and %s", "key", "value", "error message key and value", true},
	}

	for _, tc := range formattedLogTestCases { //nolint:paralleltest // Uses environment variables
		t.Run("FormattedLogs", func(t *testing.T) {
			// we create a pipe to capture the output of the log
			// so we can test that the logger logs the right message
			originalStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			os.Setenv("UNSTRUCTURED_LOGS", "false")
			defer os.Unsetenv("UNSTRUCTURED_LOGS")

			viper.SetDefault("debug", true)

			Initialize()
			os.Stdout = originalStdout

			// Log the message based on the level
			switch tc.level {
			case "DEBUG":
				log.Debugf(tc.message, tc.key, tc.value)
			case "INFO":
				log.Infof(tc.message, tc.key, tc.value)
			case "WARN":
				log.Warnf(tc.message, tc.key, tc.value)
			case "ERROR":
				log.Errorf(tc.message, tc.key, tc.value)
			}

			w.Close()

			// Read the captured output
			var capturedOutput bytes.Buffer
			io.Copy(&capturedOutput, r)
			output := capturedOutput.String()

			capturedOutput.Reset()

			// Parse JSON output
			var logEntry map[string]interface{}
			if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
				t.Fatalf("Failed to parse JSON log output: %v", err)
			}

			// Check level
			if level, ok := logEntry["level"].(string); !ok || level != tc.level {
				t.Errorf("Expected level %s, got %v", tc.level, logEntry["level"])
			}

			// Check message
			if msg, ok := logEntry["msg"].(string); !ok || msg != tc.expected {
				t.Errorf(tc.expected, tc.message, logEntry["msg"])
			}
		})
	}
}

func TestUnstructuredLogger(t *testing.T) { //nolint:paralleltest // Uses environment variables
	// we only test for the formatted logs here because the unstructured logs
	// do not contain the key/value pair format that the structured logs do
	formattedLogTestCases := []struct {
		level    string
		message  string
		key      string
		value    string
		expected string
	}{
		{"DBG", "debug message %s and %s", "key", "value", "debug message key and value"},
		{"INF", "info message %s and %s", "key", "value", "info message key and value"},
		{"WRN", "warn message %s and %s", "key", "value", "warn message key and value"},
		{"ERR", "error message %s and %s", "key", "value", "error message key and value"},
	}

	for _, tc := range formattedLogTestCases { //nolint:paralleltest // Uses environment variables
		t.Run("FormattedLogs", func(t *testing.T) {

			// we create a pipe to capture the output of the log
			// so we can test that the logger logs the right message
			originalStderr := os.Stderr
			r, w, _ := os.Pipe()
			os.Stderr = w

			viper.SetDefault("debug", true)

			Initialize()
			os.Stderr = originalStderr

			// Log the message based on the level
			switch tc.level {
			case "DBG":
				log.Debugf(tc.message, tc.key, tc.value)
			case "INF":
				log.Infof(tc.message, tc.key, tc.value)
			case "WRN":
				log.Warnf(tc.message, tc.key, tc.value)
			case "ERR":
				log.Errorf(tc.message, tc.key, tc.value)
			}

			w.Close()

			// Read the captured output
			var capturedOutput bytes.Buffer
			io.Copy(&capturedOutput, r)
			output := capturedOutput.String()

			assert.Contains(t, output, tc.level, "Expected log entry '%s' to contain log level '%s'", output, tc.level)
			assert.Contains(t, output, tc.expected, "Expected log entry '%s' to contain message '%s'", output, tc.expected)
		})
	}
}

// TestInitialize tests the Initialize function
func TestInitialize(t *testing.T) { //nolint:paralleltest // Uses environment variables
	// Test structured logs (JSON)
	t.Run("Structured Logs", func(t *testing.T) { //nolint:paralleltest // Uses environment variables
		// Set environment to use structured logs
		os.Setenv("UNSTRUCTURED_LOGS", "false")
		defer os.Unsetenv("UNSTRUCTURED_LOGS")

		// Redirect stdout to capture output
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		// Run initialization
		Initialize()

		// Log a test message
		log.Info("test message", "key", "value")

		// Restore stdout
		w.Close()
		os.Stdout = oldStdout

		// Read captured output
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		// Verify JSON format
		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
			t.Fatalf("Failed to parse JSON log output: %v", err)
		}

		if msg, ok := logEntry["msg"].(string); !ok || msg != "test message" {
			t.Errorf("Expected message 'test message', got %v", logEntry["msg"])
		}
	})

	// Test unstructured logs
	t.Run("Unstructured Logs", func(t *testing.T) { //nolint:paralleltest // Uses environment variables
		// Set environment to use unstructured logs
		os.Setenv("UNSTRUCTURED_LOGS", "true")
		defer os.Unsetenv("UNSTRUCTURED_LOGS")

		// Redirect stderr to capture output
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		// Run initialization
		Initialize()

		// Log a test message
		log.Info("test message", "key", "value")

		// Restore stderr
		w.Close()
		os.Stderr = oldStderr

		// Read captured output
		var buf bytes.Buffer
		buf.ReadFrom(r)
		output := buf.String()

		// Verify unstructured format (should contain message but not be JSON)
		if !strings.Contains(output, "test message") {
			t.Errorf("Expected output to contain 'test message', got %s", output)
		}

		if !strings.Contains(output, "INF") {
			t.Errorf("Expected output to contain 'INF', got %s", output)
		}
	})
}

// TestGetLogger tests the GetLogger function
func TestGetLogger(t *testing.T) { //nolint:paralleltest // Uses environment variables
	// Set up structured logger for testing
	os.Setenv("UNSTRUCTURED_LOGS", "false")
	defer os.Unsetenv("UNSTRUCTURED_LOGS")

	// Redirect stdout to capture output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Initialize and get a component logger
	Initialize()
	componentLogger := GetLogger("test-component")

	// Log a test message
	componentLogger.Info("component message")

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read captured output
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Parse JSON output
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
		t.Fatalf("Failed to parse JSON log output: %v", err)
	}

	// Verify the component was added
	if component, ok := logEntry["component"].(string); !ok || component != "test-component" {
		t.Errorf("Expected component='test-component', got %v", logEntry["component"])
	}
}
