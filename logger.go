package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Logger is a tiny timestamped logger matching reconnect.sh's
// log() format: "[HH:MM:SS] message".
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLogger returns a Logger writing to w.
func NewLogger(w io.Writer) *Logger {
	return &Logger{w: w}
}

// Logf writes a timestamped line.
func (l *Logger) Logf(format string, args ...any) {
	if l == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.w, line)
}

// Log writes a timestamped line.
func (l *Logger) Log(msg string) {
	l.Logf("%s", msg)
}
