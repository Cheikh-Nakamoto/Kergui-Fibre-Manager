// Package logging provides a small leveled logger implementing port.Logger and
// port.LogReader. Every line is kept in an in-memory ring buffer (for the
// dashboard) and appended to an optional log file, whatever the verbosity; the
// console only shows debug/info lines when verbose.
package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const ringSize = 2000

// Logger writes to a console writer, an optional file, and a ring buffer.
type Logger struct {
	mu      sync.Mutex
	console io.Writer
	file    io.Writer
	verbose bool
	seq     int64
	ring    []port.LogEntry
}

var (
	_ port.Logger    = (*Logger)(nil)
	_ port.LogReader = (*Logger)(nil)
)

// New returns a console-only logger. When verbose is false, only warnings and
// errors reach the console.
func New(w io.Writer, verbose bool) *Logger { return &Logger{console: w, verbose: verbose} }

// NewFile returns a logger that also appends every line, timestamped, to path.
// The returned close function closes the file.
func NewFile(path string, console io.Writer, verbose bool) (*Logger, func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	return &Logger{console: console, file: f, verbose: verbose}, f.Close, nil
}

func (l *Logger) line(level string, toConsole bool, format string, args ...any) {
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	l.ring = append(l.ring, port.LogEntry{Seq: l.seq, Time: now, Level: strings.TrimSpace(level), Msg: msg})
	if len(l.ring) > ringSize {
		l.ring = l.ring[len(l.ring)-ringSize:]
	}
	if l.file != nil {
		fmt.Fprintf(l.file, "%s %s %s\n", now.Format("2006-01-02 15:04:05.000"), level, msg)
	}
	if toConsole && l.console != nil {
		fmt.Fprintf(l.console, "%s %s\n", level, msg)
	}
}

// Debugf logs at debug level.
func (l *Logger) Debugf(format string, args ...any) { l.line("DEBUG", l.verbose, format, args...) }

// Infof logs at info level.
func (l *Logger) Infof(format string, args ...any) { l.line("INFO ", l.verbose, format, args...) }

// Warnf logs a warning.
func (l *Logger) Warnf(format string, args ...any) { l.line("WARN ", true, format, args...) }

// Errorf logs an error.
func (l *Logger) Errorf(format string, args ...any) { l.line("ERROR", true, format, args...) }

// LogsSince implements port.LogReader.
func (l *Logger) LogsSince(since int64, limit int) []port.LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []port.LogEntry{}
	for _, e := range l.ring {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
