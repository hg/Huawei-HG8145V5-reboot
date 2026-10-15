package main

import (
	"fmt"
	"io"
	"os/exec"
)

// Notifier sends the desktop "reconnected" notification.
// Production tries notify-send and falls back to printing "reconnected".
type Notifier func(summary, body string) error

// NewNotifier returns the production notifier writing the fallback line to w.
func NewNotifier(w io.Writer, lookPath func(string) (string, error), run func(string, ...string) error) Notifier {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if run == nil {
		run = func(name string, args ...string) error {
			return exec.Command(name, args...).Run()
		}
	}
	return func(summary, body string) error {
		if path, err := lookPath("notify-send"); err == nil && path != "" {
			if err := run("notify-send", summary, body); err == nil {
				return nil
			}
		}
		_, _ = fmt.Fprintln(w, "reconnected")
		return nil
	}
}
