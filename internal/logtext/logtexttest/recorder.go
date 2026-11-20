// Package logtexttest holds a slog handler that keeps the raw values of a log
// record, for a test of the text that a log line repeats.
package logtexttest

import (
	"context"
	"log/slog"
	"sync"
)

// Record is one log record with its attribute values as the logger got them.
// A text handler quotes a line break, so only the raw value shows whether the
// code removed it.
type Record struct {
	Message string
	Attrs   map[string]string
}

// Recorder is a slog.Handler that keeps each record.
type Recorder struct {
	mu      sync.Mutex
	records []Record
}

// New returns an empty Recorder.
func New() *Recorder {
	return &Recorder{mu: sync.Mutex{}, records: nil}
}

// Logger returns a logger that writes to r.
func (r *Recorder) Logger() *slog.Logger {
	return slog.New(r)
}

// Enabled reports true for each level.
func (*Recorder) Enabled(context.Context, slog.Level) bool { return true }

// Handle keeps the record.
func (r *Recorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := map[string]string{}

	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()

		return true
	})

	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = append(r.records, Record{Message: rec.Message, Attrs: attrs})

	return nil
}

// WithAttrs returns r: the handlers of the code under test log with no group.
func (r *Recorder) WithAttrs([]slog.Attr) slog.Handler { return r }

// WithGroup returns r: the handlers of the code under test log with no group.
func (r *Recorder) WithGroup(string) slog.Handler { return r }

// Records returns a copy of the records so far.
func (r *Recorder) Records() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]Record(nil), r.records...)
}
