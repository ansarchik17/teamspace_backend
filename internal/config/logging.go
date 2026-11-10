package config

import (
	"strconv"
	"strings"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	LogOpCreate = "create"
	LogOpRead   = "read"
	LogOpUpdate = "update"
	LogOpDelete = "delete"
	LogOpHTTP   = "http"
)

var consoleLogMode atomic.Bool

// NewLogger uses the same format for the API, workers and maintenance commands.
// Console colors are emitted even without a TTY so docker logs retains them.
func NewLogger(cfg LoggerConfig) (*zap.Logger, error) {
	zapCfg := zap.NewProductionConfig()
	zapCfg.Sampling = nil // Keep every event, including repeated requests and errors.
	isConsole := strings.ToLower(strings.TrimSpace(cfg.Format)) != "json"
	if isConsole {
		zapCfg.Encoding = "console"
		zapCfg.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000Z07:00")
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		zapCfg.EncoderConfig.EncodeDuration = zapcore.StringDurationEncoder
		zapCfg.DisableStacktrace = true
	}
	SetConsoleLogMode(isConsole)

	level := zapcore.InfoLevel
	if err := level.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(cfg.Level)))); err != nil {
		level = zapcore.InfoLevel
	}
	zapCfg.Level = zap.NewAtomicLevelAt(level)
	output := cfg.Output
	if output == "" {
		output = "stdout"
	}
	zapCfg.OutputPaths = []string{output}
	return zapCfg.Build(WrapLoggerCore())
}

func SetConsoleLogMode(enabled bool) {
	consoleLogMode.Store(enabled)
}

func LogOp(op string) zap.Field {
	return zap.String("op", op)
}

func LogEvent(op, msg string) string {
	label := strings.ToUpper(op)
	if !consoleLogMode.Load() {
		return "[" + label + "] " + msg
	}

	switch op {
	case LogOpCreate:
		return "\x1b[32m[CREATE]\x1b[0m " + msg
	case LogOpRead:
		return "\x1b[36m[READ]\x1b[0m " + msg
	case LogOpUpdate:
		return "\x1b[33m[UPDATE]\x1b[0m " + msg
	case LogOpDelete:
		return "\x1b[31m[DELETE]\x1b[0m " + msg
	case LogOpHTTP:
		return "\x1b[35m[HTTP]\x1b[0m " + msg
	default:
		return "[" + label + "] " + msg
	}
}

func LogOpFromMethod(method string) string {
	switch strings.ToUpper(method) {
	case "POST":
		return LogOpCreate
	case "GET", "HEAD":
		return LogOpRead
	case "PUT", "PATCH":
		return LogOpUpdate
	case "DELETE":
		return LogOpDelete
	default:
		return LogOpHTTP
	}
}

func ColorizeHTTPStatus(status int) string {
	text := "[" + strconv.Itoa(status) + "]"
	if !consoleLogMode.Load() {
		return text
	}

	switch {
	case status >= 500:
		return "\x1b[31m" + text + "\x1b[0m"
	case status >= 400:
		return "\x1b[33m" + text + "\x1b[0m"
	case status >= 300:
		return "\x1b[34m" + text + "\x1b[0m"
	default:
		return "\x1b[32m" + text + "\x1b[0m"
	}
}

func WrapLoggerCore() zap.Option {
	return zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return &opAwareCore{Core: core}
	})
}

type opAwareCore struct {
	zapcore.Core
	op string
}

func (c *opAwareCore) With(fields []zapcore.Field) zapcore.Core {
	op := c.op
	if value, exists := findOpField(fields); exists {
		op = value
	}
	return &opAwareCore{Core: c.Core.With(fields), op: op}
}

func (c *opAwareCore) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !c.Enabled(entry.Level) {
		return ce
	}
	return ce.AddCore(entry, c)
}

func (c *opAwareCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	op, exists := findOpField(fields)
	if !exists && c.op != "" {
		op, exists = c.op, true
	}
	if !exists {
		op = inferOpFromMessage(entry.Message)
		if op != "" {
			fields = append(fields, zap.String("op", op))
		}
	}

	if op != "" && !hasOpPrefix(entry.Message, op) {
		entry.Message = LogEvent(op, entry.Message)
	}

	return c.Core.Write(entry, fields)
}

func findOpField(fields []zapcore.Field) (string, bool) {
	for _, field := range fields {
		if field.Key != "op" {
			continue
		}
		if field.Type == zapcore.StringType {
			return field.String, true
		}
	}
	return "", false
}

func hasOpPrefix(msg, op string) bool {
	upper := strings.ToUpper(op)
	return strings.Contains(msg, "["+upper+"]")
}

func inferOpFromMessage(msg string) string {
	lower := strings.ToLower(msg)

	switch {
	case containsAny(lower,
		"create", "created",
		"insert", "inserted",
		"add ", "added",
		"start ", "started",
		"send ", "sent",
		"register", "registered",
		"connect", "connected",
		"upload", "uploaded",
		"pin ", "pinned",
		"award", "awarded",
	):
		return LogOpCreate
	case containsAny(lower,
		"get ", "list",
		"fetch", "fetched",
		"found", "read ",
		"loaded", "load ",
		"search", "searched",
		"resolved", "lookup",
		"checking", "checked",
		"using ",
	):
		return LogOpRead
	case containsAny(lower,
		"update", "updated",
		"change", "changed",
		"set ", "sync", "synced",
		"upsert",
		"refresh", "refreshed",
		"edit", "edited",
		"role updated",
	):
		return LogOpUpdate
	case containsAny(lower,
		"delete", "deleted",
		"remove", "removed",
		"clear", "cleared",
		"stop ", "stopped",
		"abort", "cancel",
		"disconnect", "disconnected",
		"unpin", "unpinned",
		"hide", "hidden",
		"drop", "dropped",
		"revoke", "revoked",
		"kick", "kicked",
	):
		return LogOpDelete
	default:
		return ""
	}
}

func containsAny(s string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(s, pattern) {
			return true
		}
	}
	return false
}
