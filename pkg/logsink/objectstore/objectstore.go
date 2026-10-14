// Package objectstore is a logsink.Sink adapter that writes records to a
// blob.Store as files of JSON lines, compressed with gzip (ADR 60 decision
// 12). The store can be a directory or an S3 bucket (ADR 31).
//
// Each Write is one object for each partition of the batch, so a record
// that Write answered for is in the store. The caller sets the size of a
// file through the size of its batches: the audit shipper sends one batch
// for each scope and interval. The key of an object is
//
//	<prefix>/[<partition>/]<yyyy-mm-dd>/<first>-<last>-<random>.jsonl.gz
//
// where the date and the two times (Unix milliseconds) come from the
// records of the object, and the random part keeps two instances that write
// at the same time apart. A key is never written twice.
//
// WithPartition names a record attribute, for example "scope" for audit
// entries. Records with different values of it go to different objects,
// and the value is a key segment. A key segment holds [a-z0-9._-] only, so
// each other byte of the value, and "_", becomes "_" and two hexadecimal
// digits: "acme" stays "acme", "Acme" becomes "_41cme" and "@server"
// becomes "_40server".
package objectstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/logsink"
)

// randomBytes is the length of the random part of a key, before hex.
const randomBytes = 8

// Sink writes one gzip JSONL object for each batch and partition. It is
// safe for concurrent use.
type Sink struct {
	store     blob.Store
	prefix    string
	partition string
	mu        sync.RWMutex
	closed    bool
}

// Option configures a Sink.
type Option func(*Sink)

// WithPartition makes the value of the record attribute attr a key
// segment, so the records of each value go to their own objects.
func WithPartition(attr string) Option {
	return func(s *Sink) { s.partition = attr }
}

var _ logsink.Sink = (*Sink)(nil)

// New returns a sink that writes under prefix in store. The prefix must be
// a valid blob key, for example "audit" or "logs/app".
func New(store blob.Store, prefix string, opts ...Option) (*Sink, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: object store sink needs a blob store", logsink.ErrInvalidConfig)
	}

	if _, err := blob.ParseKey(prefix); err != nil {
		return nil, fmt.Errorf("%w: object store sink prefix: %w", logsink.ErrInvalidConfig, err)
	}

	s := &Sink{store: store, prefix: prefix, partition: "", mu: sync.RWMutex{}, closed: false}
	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// Write implements logsink.Sink: it writes the batch as one object.
func (s *Sink) Write(ctx context.Context, records []logsink.Record) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write log object: %w", err)
	}

	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()

	if closed {
		return logsink.ErrClosed
	}

	for _, part := range s.split(records) {
		if err := s.put(ctx, part); err != nil {
			return err
		}
	}

	return nil
}

// part is the records of one partition, in order, and the key segment of
// the partition, empty when the sink has no partition.
type part struct {
	segment string
	records []logsink.Record
}

// attrText answers the text of the attribute key of r, or "" when r has
// none.
func attrText(r logsink.Record, key string) string {
	for _, a := range r.Attrs {
		if a.Key == key {
			return fmt.Sprint(a.Value)
		}
	}

	return ""
}

// segment escapes value as one key segment: each byte outside [a-z0-9.-],
// and "_", becomes "_" and two hexadecimal digits. An empty value becomes
// "_", and a value that starts with "." gets that byte escaped, so the
// segment is never empty, "." or "..".
func segment(value string) string {
	if value == "" {
		return "_"
	}

	var b strings.Builder

	for i := range len(value) {
		c := value[i]

		plain := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || (c == '.' && i > 0)
		if plain {
			b.WriteByte(c)

			continue
		}

		b.WriteString("_" + hex.EncodeToString([]byte{c}))
	}

	return b.String()
}

// compress writes records as gzip JSON lines.
func compress(records []logsink.Record) ([]byte, error) {
	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)

	for i := range records {
		line, err := logsink.MarshalJSON(records[i])
		if err != nil {
			return nil, err
		}

		line = append(line, '\n')

		if _, err := zw.Write(line); err != nil {
			return nil, fmt.Errorf("compress log object: %w", err)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compress log object: %w", err)
	}

	return buf.Bytes(), nil
}

// Close implements logsink.Sink. The blob store stays open: its owner
// closes it.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	return nil
}

// split groups records by partition, in order of first appearance. Without
// a partition it answers one part.
func (s *Sink) split(records []logsink.Record) []part {
	if len(records) == 0 {
		return nil
	}

	if s.partition == "" {
		return []part{{segment: "", records: records}}
	}

	var order []string

	groups := map[string][]logsink.Record{}

	for i := range records {
		seg := segment(attrText(records[i], s.partition))

		if _, ok := groups[seg]; !ok {
			order = append(order, seg)
		}

		groups[seg] = append(groups[seg], records[i])
	}

	parts := make([]part, 0, len(order))
	for _, seg := range order {
		parts = append(parts, part{segment: seg, records: groups[seg]})
	}

	return parts
}

// put writes the records of p as one object.
func (s *Sink) put(ctx context.Context, p part) error {
	body, err := compress(p.records)
	if err != nil {
		return err
	}

	key, err := s.key(p)
	if err != nil {
		return err
	}

	if err := s.store.Put(ctx, key, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("write log object: %w", err)
	}

	return nil
}

// key returns the object key for p: the partition segment, the date of its
// first record, the first and last record times in Unix milliseconds, and a
// random part.
func (s *Sink) key(p part) (blob.Key, error) {
	first := p.records[0].Time.UTC()
	last := p.records[len(p.records)-1].Time.UTC()

	var random [randomBytes]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("log object key: %w", err)
	}

	dir := s.prefix + "/"
	if p.segment != "" {
		dir += p.segment + "/"
	}

	name := dir + first.Format(time.DateOnly) + "/" +
		strconv.FormatInt(first.UnixMilli(), 10) + "-" +
		strconv.FormatInt(last.UnixMilli(), 10) + "-" +
		hex.EncodeToString(random[:]) + ".jsonl.gz"

	key, err := blob.ParseKey(name)
	if err != nil {
		return "", fmt.Errorf("log object key: %w", err)
	}

	return key, nil
}
