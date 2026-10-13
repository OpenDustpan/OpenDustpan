// Package objectstore is a logsink.Sink adapter that writes records to a
// blob.Store as files of JSON lines, compressed with gzip (ADR 60 decision
// 12). The store can be a directory or an S3 bucket (ADR 31).
//
// Each Write is one object, so a record that Write answered for is in the
// store. The caller sets the size of a file through the size of its
// batches: the audit shipper sends one batch for each interval. The key of
// an object is
//
//	<prefix>/<yyyy-mm-dd>/<first>-<last>-<random>.jsonl.gz
//
// where the date and the two times (Unix milliseconds) come from the
// records of the batch, and the random part keeps two instances that write
// at the same time apart. A key is never written twice.
package objectstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/logsink"
)

// randomBytes is the length of the random part of a key, before hex.
const randomBytes = 8

// Sink writes one gzip JSONL object for each batch. It is safe for
// concurrent use.
type Sink struct {
	store  blob.Store
	prefix string
	mu     sync.RWMutex
	closed bool
}

var _ logsink.Sink = (*Sink)(nil)

// New returns a sink that writes under prefix in store. The prefix must be
// a valid blob key, for example "audit" or "logs/app".
func New(store blob.Store, prefix string) (*Sink, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: object store sink needs a blob store", logsink.ErrInvalidConfig)
	}

	if _, err := blob.ParseKey(prefix); err != nil {
		return nil, fmt.Errorf("%w: object store sink prefix: %w", logsink.ErrInvalidConfig, err)
	}

	return &Sink{store: store, prefix: prefix, mu: sync.RWMutex{}, closed: false}, nil
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

	if len(records) == 0 {
		return nil
	}

	body, err := compress(records)
	if err != nil {
		return err
	}

	key, err := s.key(records)
	if err != nil {
		return err
	}

	if err := s.store.Put(ctx, key, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("write log object: %w", err)
	}

	return nil
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

// key returns the object key for a batch: the date of its first record,
// the first and last record times in Unix milliseconds, and a random part.
func (s *Sink) key(records []logsink.Record) (blob.Key, error) {
	first := records[0].Time.UTC()
	last := records[len(records)-1].Time.UTC()

	var random [randomBytes]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("log object key: %w", err)
	}

	name := s.prefix + "/" + first.Format(time.DateOnly) + "/" +
		strconv.FormatInt(first.UnixMilli(), 10) + "-" +
		strconv.FormatInt(last.UnixMilli(), 10) + "-" +
		hex.EncodeToString(random[:]) + ".jsonl.gz"

	key, err := blob.ParseKey(name)
	if err != nil {
		return "", fmt.Errorf("log object key: %w", err)
	}

	return key, nil
}
