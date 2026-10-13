// Package slogsink is a log/slog handler that writes records to a
// logsink.Sink (ADR 60 decision 14). The structured application log of the
// server uses it, so the application log can go to the same targets as the
// audit log: a file, syslog, a webhook or object storage.
//
// The handler is best effort. Handle never waits for the sink: it puts the
// record in a bounded queue and returns. A goroutine writes the queue to
// the sink in batches. When the queue is full, or a write fails, the
// handler drops the records and reports the count through OnDrop. The
// audit log never uses this handler, because it must not drop an entry.
package slogsink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
)

// Defaults of Options.
const (
	DefaultQueueSize     = 4096
	DefaultBatchSize     = 256
	DefaultFlushInterval = time.Second
	DefaultWriteTimeout  = 10 * time.Second
)

// ErrClosed reports a Close on a handler that is already closed.
var ErrClosed = errors.New("slog sink handler closed")

// Options configures a Handler. Zero values take the defaults.
type Options struct {
	// Level is the lowest level the handler writes. Nil means info.
	Level slog.Leveler
	// OnDrop receives the count of records the handler dropped: the queue
	// was full, the write failed, or the handler was closed. Nil ignores
	// the count.
	OnDrop func(records int)
	// QueueSize caps the records that wait for the sink.
	QueueSize int
	// BatchSize caps the records of one sink write.
	BatchSize int
	// FlushInterval is the longest time a record waits in the queue.
	FlushInterval time.Duration
	// WriteTimeout bounds one sink write.
	WriteTimeout time.Duration
}

// Handler is a slog.Handler that queues records for one sink. Handlers that
// WithAttrs and WithGroup derive share the queue of their parent.
type Handler struct {
	core   *core
	prefix string
	attrs  []logsink.Attr
}

var _ slog.Handler = (*Handler)(nil)

// core is the queue and the writer goroutine that every derived handler
// shares.
type core struct {
	sink   logsink.Sink
	level  slog.Leveler
	onDrop func(int)
	queue  chan logsink.Record
	done   chan struct{}
	opts   Options
	mu     sync.RWMutex
	closed bool
}

// New returns a handler that writes to sink and starts its writer
// goroutine. Call Close to flush the queue and stop the goroutine. Close
// does not close the sink.
func New(sink logsink.Sink, opts Options) *Handler {
	if opts.Level == nil {
		opts.Level = slog.LevelInfo
	}

	opts.QueueSize = positive(opts.QueueSize, DefaultQueueSize)
	opts.BatchSize = positive(opts.BatchSize, DefaultBatchSize)
	opts.FlushInterval = positiveDuration(opts.FlushInterval, DefaultFlushInterval)
	opts.WriteTimeout = positiveDuration(opts.WriteTimeout, DefaultWriteTimeout)

	c := &core{
		sink:   sink,
		level:  opts.Level,
		onDrop: opts.OnDrop,
		queue:  make(chan logsink.Record, opts.QueueSize),
		done:   make(chan struct{}),
		opts:   opts,
		mu:     sync.RWMutex{},
		closed: false,
	}

	go c.run()

	return &Handler{core: c, prefix: "", attrs: nil}
}

// positive returns v, or def when v is zero or negative.
func positive(v, def int) int {
	if v <= 0 {
		return def
	}

	return v
}

// positiveDuration returns v, or def when v is zero or negative.
func positiveDuration(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}

	return v
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.core.level.Level()
}

// Handle implements slog.Handler: it queues the record and never waits. A
// full queue or a closed handler drops the record and counts it.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	attrs := make([]logsink.Attr, 0, len(h.attrs)+r.NumAttrs())
	attrs = append(attrs, h.attrs...)

	r.Attrs(func(a slog.Attr) bool {
		attrs = appendAttr(attrs, h.prefix, a)

		return true
	})

	rec := logsink.Record{
		Time:    r.Time,
		Message: r.Message,
		Attrs:   attrs,
		Level:   logsink.Level(r.Level),
	}

	h.core.mu.RLock()
	defer h.core.mu.RUnlock()

	if h.core.closed {
		h.core.drop(1)

		return nil
	}

	select {
	case h.core.queue <- rec:
	default:
		h.core.drop(1)
	}

	return nil
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]logsink.Attr, 0, len(h.attrs)+len(attrs))
	out = append(out, h.attrs...)

	for _, a := range attrs {
		out = appendAttr(out, h.prefix, a)
	}

	return &Handler{core: h.core, prefix: h.prefix, attrs: out}
}

// WithGroup implements slog.Handler: later attributes get the group name
// and a "." before their key.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &Handler{core: h.core, prefix: h.prefix + name + ".", attrs: h.attrs}
}

// Close stops the handler: it refuses new records, writes the queue to the
// sink, and waits for the writer goroutine until ctx is done. Records that
// it cannot write before ctx is done count as dropped. A second Close
// returns ErrClosed.
func (h *Handler) Close(ctx context.Context) error {
	c := h.core

	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()

		return ErrClosed
	}

	c.closed = true
	close(c.queue)
	c.mu.Unlock()

	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("flush log sink: %w", ctx.Err())
	}
}

// appendAttr appends a as one or more flat attributes: a group becomes one
// attribute for each member, with the group name and "." before its key.
func appendAttr(out []logsink.Attr, prefix string, a slog.Attr) []logsink.Attr {
	v := a.Value.Resolve()

	if v.Kind() == slog.KindGroup {
		inner := prefix
		if a.Key != "" {
			inner = prefix + a.Key + "."
		}

		for _, member := range v.Group() {
			out = appendAttr(out, inner, member)
		}

		return out
	}

	if a.Key == "" {
		return out
	}

	return append(out, logsink.Attr{Key: prefix + a.Key, Value: plainValue(v)})
}

// plainValue maps a resolved slog value to a string, an int64, a float64,
// a bool or nil, as logsink.Attr expects. A duration is its count of
// nanoseconds, as the JSON handler of log/slog writes it. A time is RFC
// 3339 text in UTC.
func plainValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		if u := v.Uint64(); u <= math.MaxInt64 {
			return int64(u)
		}

		return v.String()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().Nanoseconds()
	case slog.KindTime:
		return v.Time().UTC().Format(time.RFC3339Nano)
	case slog.KindAny, slog.KindGroup, slog.KindLogValuer:
		return anyValue(v.Any())
	default:
		return fmt.Sprint(v.Any())
	}
}

// anyValue maps an Any value to text: an error is its message, nil stays
// nil, and anything else is its default text form.
func anyValue(x any) any {
	switch t := x.(type) {
	case nil:
		return nil
	case error:
		return t.Error()
	case fmt.Stringer:
		return t.String()
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

// drop reports n dropped records.
func (c *core) drop(n int) {
	if c.onDrop != nil && n > 0 {
		c.onDrop(n)
	}
}

// run writes the queue to the sink in batches until Close closes the
// queue, then writes the rest and closes done.
func (c *core) run() {
	defer close(c.done)

	ticker := time.NewTicker(c.opts.FlushInterval)
	defer ticker.Stop()

	batch := make([]logsink.Record, 0, c.opts.BatchSize)

	for {
		select {
		case rec, ok := <-c.queue:
			if !ok {
				c.write(batch)

				return
			}

			batch = append(batch, rec)
			if len(batch) >= c.opts.BatchSize {
				c.write(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			c.write(batch)
			batch = batch[:0]
		}
	}
}

// write sends batch to the sink within the write timeout. A failed write
// drops the batch: the application log is best effort. A sink that skips
// records it cannot hold (logsink.ErrSkipped) delivered the rest, so only
// the skipped records count as dropped.
func (c *core) write(batch []logsink.Record) {
	if len(batch) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.opts.WriteTimeout)
	defer cancel()

	if err := c.sink.Write(ctx, batch); err != nil {
		if skip, ok := errors.AsType[*logsink.SkipError](err); ok {
			c.drop(skip.Skipped)

			return
		}

		c.drop(len(batch))
	}
}
