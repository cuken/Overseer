package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ANSI color codes
const (
	Reset   = "\033[0m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	Gray    = "\033[90m"
	Bold    = "\033[1m"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger provides structured logging with colors and file output
type Logger struct {
	component   string
	color       string
	logDir      string
	logFile     *os.File
	verbose     bool
	mu          sync.Mutex
	noColor     bool
	minLevel    LogLevel
}

var (
	globalMu sync.Mutex
	loggers  = make(map[string]*Logger)
)

// Setup initializes the logging system
func Setup(logDir string, verbose bool) error {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	return nil
}

// New creates a new logger for a component
func New(component, logDir string) *Logger {
	globalMu.Lock()
	defer globalMu.Unlock()

	if logger, exists := loggers[component]; exists {
		return logger
	}

	color := getComponentColor(component)
	logger := &Logger{
		component: component,
		color:     color,
		logDir:    logDir,
		verbose:   false,
		noColor:   os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb",
		minLevel:  LevelInfo,
	}

	// Open log file for this component
	if logDir != "" {
		logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", strings.ToLower(component)))
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			logger.logFile = f
		}
	}

	loggers[component] = logger
	return logger
}

// SetVerbose enables verbose (debug) logging
func (l *Logger) SetVerbose(v bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.verbose = v
	if v {
		l.minLevel = LevelDebug
	} else {
		l.minLevel = LevelInfo
	}
}

// Close closes the log file
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.logFile != nil {
		return l.logFile.Close()
	}
	return nil
}

// Debug logs a debug message (only if verbose is enabled)
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, format, args...)
}

// Info logs an informational message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, format, args...)
}

// Success logs a success message (same level as Info but green)
func (l *Logger) Success(format string, args ...interface{}) {
	l.logColored(LevelInfo, Green, format, args...)
}

// log writes a log message to both console and file
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	l.logColored(level, l.color, format, args...)
}

func (l *Logger) logColored(level LogLevel, color string, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if level < l.minLevel {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf(format, args...)

	// Console output (with colors if enabled)
	var consoleMsg string
	if l.noColor {
		consoleMsg = fmt.Sprintf("[%s] [%s] %s", timestamp, l.component, message)
	} else {
		consoleMsg = fmt.Sprintf("%s[%s]%s %s[%s]%s %s",
			Gray, timestamp, Reset,
			color, l.component, Reset,
			colorizeMessage(message, level))
	}
	fmt.Fprintln(os.Stderr, consoleMsg)

	// File output (no colors)
	if l.logFile != nil {
		fileMsg := fmt.Sprintf("[%s] [%s] [%s] %s\n", timestamp, level, l.component, message)
		l.logFile.WriteString(fileMsg)
	}
}

// LogAgentThinking logs agent thinking in a formatted way
func (l *Logger) LogAgentThinking(thinking string) {
	if !l.verbose {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("15:04:05")
	lines := strings.Split(strings.TrimSpace(thinking), "\n")

	// Console output
	if !l.noColor {
		fmt.Fprintf(os.Stderr, "%s[%s]%s %s[%s THINKING]%s\n",
			Gray, timestamp, Reset,
			Cyan, l.component, Reset)
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(os.Stderr, "%s  │ %s%s\n", Gray, line, Reset)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "[%s] [%s THINKING]\n", timestamp, l.component)
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(os.Stderr, "  | %s\n", line)
			}
		}
	}

	// File output
	if l.logFile != nil {
		l.logFile.WriteString(fmt.Sprintf("[%s] [THINKING] %s\n", timestamp, thinking))
	}
}

// LogAgentMessage logs agent messages in a formatted way
func (l *Logger) LogAgentMessage(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("15:04:05")
	lines := strings.Split(strings.TrimSpace(message), "\n")

	// Console output
	if !l.noColor {
		fmt.Fprintf(os.Stderr, "%s[%s]%s %s[%s MESSAGE]%s\n",
			Gray, timestamp, Reset,
			Yellow, l.component, Reset)
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(os.Stderr, "%s  │ %s%s\n", Gray, line, Reset)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "[%s] [%s MESSAGE]\n", timestamp, l.component)
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(os.Stderr, "  | %s\n", line)
			}
		}
	}

	// File output
	if l.logFile != nil {
		l.logFile.WriteString(fmt.Sprintf("[%s] [MESSAGE] %s\n", timestamp, message))
	}
}

// LogError logs an error with full details to both console and error log file
func (l *Logger) LogError(err error, context string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format("2006-01-02 15:04:05")

	// Console output
	if !l.noColor {
		fmt.Fprintf(os.Stderr, "%s[%s]%s %s[%s ERROR]%s %s: %s%s%s\n",
			Gray, timestamp, Reset,
			Red+Bold, l.component, Reset,
			context,
			Red, err.Error(), Reset)
	} else {
		fmt.Fprintf(os.Stderr, "[%s] [%s ERROR] %s: %s\n",
			timestamp, l.component, context, err.Error())
	}

	// Write to component log file
	if l.logFile != nil {
		l.logFile.WriteString(fmt.Sprintf("[%s] [ERROR] %s: %s\n", timestamp, context, err.Error()))
	}

	// Also write to errors.log
	if l.logDir != "" {
		errorLogPath := filepath.Join(l.logDir, "errors.log")
		if f, err := os.OpenFile(errorLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			defer f.Close()
			f.WriteString(fmt.Sprintf("[%s] [%s] %s: %s\n", timestamp, l.component, context, err.Error()))
		}
	}
}

// Writer returns an io.Writer that logs to this logger at the Info level
func (l *Logger) Writer() io.Writer {
	return &logWriter{logger: l}
}

type logWriter struct {
	logger *Logger
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	w.logger.Info("%s", strings.TrimSuffix(string(p), "\n"))
	return len(p), nil
}

// getComponentColor returns the ANSI color code for a component
func getComponentColor(component string) string {
	switch strings.ToLower(component) {
	case "daemon":
		return Blue
	case "worker":
		return Green
	case "agent":
		return Yellow
	case "llama":
		return Magenta
	case "pool":
		return Cyan
	case "git":
		return Cyan
	case "mcp":
		return Magenta
	default:
		return Reset
	}
}

// getLevelColor returns the ANSI color code for a log level
func getLevelColor(level LogLevel) string {
	switch level {
	case LevelDebug:
		return Gray
	case LevelInfo:
		return Reset
	case LevelWarn:
		return Yellow
	case LevelError:
		return Red
	default:
		return Reset
	}
}

// colorizeMessage applies color to error/warning keywords in messages
func colorizeMessage(message string, level LogLevel) string {
	switch level {
	case LevelError:
		return Red + message + Reset
	case LevelWarn:
		return Yellow + message + Reset
	default:
		return message
	}
}

// CloseAll closes all open loggers
func CloseAll() {
	globalMu.Lock()
	defer globalMu.Unlock()

	for _, logger := range loggers {
		logger.Close()
	}
}
