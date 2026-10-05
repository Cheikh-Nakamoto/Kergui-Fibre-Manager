// Package logging provides a small leveled logger implementing port.Logger.
package logging

import (
	"fmt"
	"io"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Logger writes to an io.Writer. Debug/Info are emitted only when verbose.
type Logger struct {
	w       io.Writer
	verbose bool
}

var _ port.Logger = (*Logger)(nil)

// New returns a logger. When verbose is false, only warnings and errors print.
func New(w io.Writer, verbose bool) *Logger { return &Logger{w: w, verbose: verbose} }

func (l *Logger) line(level, format string, args ...any) {
	fmt.Fprintf(l.w, level+" "+format+"\n", args...)
}

// Debugf logs at debug level (verbose only).
func (l *Logger) Debugf(format string, args ...any) {
	if l.verbose {
		l.line("DEBUG", format, args...)
	}
}

// Infof logs at info level (verbose only).
func (l *Logger) Infof(format string, args ...any) {
	if l.verbose {
		l.line("INFO ", format, args...)
	}
}

// Warnf logs a warning.
func (l *Logger) Warnf(format string, args ...any) { l.line("WARN ", format, args...) }

// Errorf logs an error.
func (l *Logger) Errorf(format string, args ...any) { l.line("ERROR", format, args...) }
