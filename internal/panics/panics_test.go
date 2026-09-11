package panics

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func logTo(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestAPanicBecomesAnErrorAndItsStackGoesToTheLog(t *testing.T) {
	log := logTo(t)
	err := func() (err error) {
		defer Recover(&err, "counting rows")
		var m map[string]int
		m["x"]++ // writing to a nil map panics
		return nil
	}()
	var pe *Error
	if !errors.As(err, &pe) || pe.What != "counting rows" {
		t.Fatalf("%v; want the panic as an *Error", err)
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "the driver failed while counting rows, and stopped:") || strings.Contains(msg, "goroutine") {
		t.Errorf("the message should be a sentence, without the stack: %q", msg)
	}
	if !strings.Contains(log.String(), "panics_test.go") {
		t.Error("the log should carry the stack")
	}
}

func TestCatchReportsAPanicOnAGoroutine(t *testing.T) {
	logTo(t)
	got := make(chan error, 1)
	go func() {
		defer Catch("reading rows", func(err error) { got <- err })
		panic("boom")
	}()
	var pe *Error
	if err := <-got; !errors.As(err, &pe) || pe.Value != "boom" {
		t.Errorf("%v", err)
	}
}

func TestNoPanicIsNoError(t *testing.T) {
	err := func() (err error) {
		defer Recover(&err, "anything")
		return nil
	}()
	if err != nil {
		t.Errorf("%v", err)
	}
}
