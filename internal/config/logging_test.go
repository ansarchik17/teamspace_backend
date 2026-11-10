package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestLoggerRetainsOperationsAndRepeatedEvents(t *testing.T) {
	t.Cleanup(func() { SetConsoleLogMode(false) })
	path := filepath.Join(t.TempDir(), "backend.log")
	logger, err := NewLogger(LoggerConfig{Format: "json", Level: "info", Output: path})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("created publication", zap.Int64("user_id", 9))
	logger.With(LogOp(LogOpDelete)).Info("publication processed")
	logger.With(LogOp(LogOpRead)).Info("publication processed", LogOp(LogOpUpdate))
	logger.Debug("filtered debug event")
	for range 200 {
		logger.Info("repeated event")
	}
	if err := logger.Sync(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 203 {
		t.Fatalf("expected all 203 info events exactly once, got %d", len(lines))
	}
	for i, op := range []string{LogOpCreate, LogOpDelete, LogOpUpdate} {
		var event map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &event); err != nil {
			t.Fatal(err)
		}
		if event["op"] != op || !strings.HasPrefix(event["msg"].(string), "["+strings.ToUpper(op)+"] ") {
			t.Fatalf("operation %s missing in event: %s", op, lines[i])
		}
	}
	if strings.Contains(string(data), "\x1b") || strings.Contains(string(data), `\u001b`) {
		t.Fatal("JSON contains ANSI escapes")
	}
}

func TestConsoleLoggerColorsLevelsAndOperations(t *testing.T) {
	t.Cleanup(func() { SetConsoleLogMode(false) })
	path := filepath.Join(t.TempDir(), "backend.log")
	logger, err := NewLogger(LoggerConfig{Format: "console", Level: "debug", Output: path})
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("debug event")
	logger.Info("created publication")
	logger.Warn("warning event")
	logger.Error("error event")
	if err := logger.Sync(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"DEBUG", "INFO", "WARN", "ERROR"} {
		if !strings.Contains(string(data), level+"\x1b[0m") {
			t.Fatalf("missing colored %s level in %q", level, data)
		}
	}
	if !strings.Contains(string(data), "\x1b[32m[CREATE]\x1b[0m created publication") {
		t.Fatalf("missing colored operation in %q", data)
	}
}
