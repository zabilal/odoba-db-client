// Package logging is the application's structured log (NFR-R4), with secret
// redaction applied at the single boundary every log line crosses (NFR-S2).
//
// Redaction is done by the handler, indiscriminately, rather than by call
// sites remembering to redact. Selective redaction is redaction that
// eventually gets forgotten (see internal/redact).
//
// This package must not import any UI package (ARCH-1).
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/ikigai-db/ikigai-db/internal/redact"
)

const (
	// FileName is the current log file; rotated files get .1, .2, ... suffixes.
	FileName = "ikigai.log"

	// DefaultMaxBytes and DefaultKeep bound the log on disk: at most
	// (DefaultKeep+1) files of DefaultMaxBytes each, about 20 MiB.
	DefaultMaxBytes = 5 << 20
	DefaultKeep     = 3
)

// Options configures a logger.
type Options struct {
	Level    slog.Level
	MaxBytes int64
	Keep     int
}

// New returns a logger that writes JSON lines to dir/ikigai.log, rotated by
// size and redacted. Close the returned Closer at shutdown.
func New(dir string, opt Options) (*slog.Logger, io.Closer, error) {
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = DefaultMaxBytes
	}
	if opt.Keep <= 0 {
		opt.Keep = DefaultKeep
	}
	w, err := openRotating(filepath.Join(dir, FileName), opt.MaxBytes, opt.Keep)
	if err != nil {
		return nil, nil, err
	}
	h := NewRedactingHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: opt.Level}))
	return slog.New(h), w, nil
}

// NewRedactingHandler wraps a handler so that nothing reaches it unredacted.
func NewRedactingHandler(next slog.Handler) slog.Handler { return redactingHandler{next} }

type redactingHandler struct{ next slog.Handler }

func (h redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, redact.String(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

// WithAttrs redacts attributes as they are attached, because logger.With is
// the most common way a DSN ends up in every subsequent line.
func (h redactingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(as))
	for i, a := range as {
		red[i] = redactAttr(a)
	}
	return redactingHandler{h.next.WithAttrs(red)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()

	// An attribute named like a secret holds one, whatever its value looks
	// like: slog.String("password", pw) must never print pw.
	if redact.IsSecretKey(a.Key) {
		if a.Value.Kind() == slog.KindString {
			return slog.String(a.Key, redact.Value(a.Value.String()))
		}
		return slog.String(a.Key, redact.Mask)
	}

	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, redact.String(a.Value.String()))
	case slog.KindGroup:
		group := a.Value.Group()
		red := make([]slog.Attr, len(group))
		for i, g := range group {
			red[i] = redactAttr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(red...)}
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case error:
			return slog.String(a.Key, redact.Error(v))
		case fmt.Stringer:
			return slog.String(a.Key, redact.String(v.String()))
		default:
			// The JSON handler would marshal an arbitrary value field by field,
			// including any credential a struct happens to hold. Rendering it
			// as text and redacting that loses the structure but not the
			// secret. That is the right trade for a log.
			return slog.String(a.Key, redact.String(fmt.Sprintf("%+v", v)))
		}
	}
	return a // numbers, bools, times and durations carry no text
}

// rotating is an io.Writer that rotates its file by size. Safe for
// concurrent use.
type rotating struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

func openRotating(path string, max int64, keep int) (*rotating, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rotating{path: path, max: max, keep: keep, f: f, size: st.Size()}, nil
}

func (r *rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.max {
		// A failed rotation must not lose the line: keep writing to the
		// current file. An oversized log is recoverable; a missing line is not.
		_ = r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotating) rotate() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	os.Remove(fmt.Sprintf("%s.%d", r.path, r.keep))
	for i := r.keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	renameErr := os.Rename(r.path, r.path+".1")

	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if renameErr == nil {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(r.path, flags, 0o600)
	if err != nil {
		r.f = nil
		return err
	}
	st, _ := f.Stat()
	r.f, r.size = f, 0
	if st != nil {
		r.size = st.Size()
	}
	return renameErr
}

func (r *rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
