// Package file is a logsink.Sink adapter that appends JSON lines to a file
// (ADR 60 decision 12). Each record is one line that logsink.MarshalJSON
// writes. Write syncs the file before it returns, so a record that Write
// answered for is on disk. Reopen opens the path again, so an external
// rotation tool can move the file away and the sink goes on in a new file.
package file

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"

	"opendustpan.org/opendustpan/pkg/logsink"
)

// fileMode is the mode of a new log file: owner read and write only, so
// audit records do not leak through a shared directory.
const fileMode = 0o600

// Sink appends records to one file. It is safe for concurrent use.
type Sink struct {
	f    *os.File
	path string
	mu   sync.Mutex
}

var _ logsink.Sink = (*Sink)(nil)

// Open opens path for appending and creates it with mode 0600 when it does
// not exist.
func Open(path string) (*Sink, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: log file path is empty", logsink.ErrInvalidConfig)
	}

	f, err := openAppend(path)
	if err != nil {
		return nil, err
	}

	return &Sink{f: f, path: path, mu: sync.Mutex{}}, nil
}

// openAppend opens path for appending.
func openAppend(path string) (*os.File, error) {
	//nolint:gosec // The operator names the log file in the server settings; no request sets it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return f, nil
}

// Write implements logsink.Sink: it appends one line for each record and
// syncs the file.
func (s *Sink) Write(ctx context.Context, records []logsink.Record) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write log file: %w", err)
	}

	var buf bytes.Buffer

	for i := range records {
		line, err := logsink.MarshalJSON(records[i])
		if err != nil {
			return err
		}

		buf.Write(line)
		buf.WriteByte('\n')
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.f == nil {
		return logsink.ErrClosed
	}

	if buf.Len() == 0 {
		return nil
	}

	if _, err := s.f.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write log file: %w", err)
	}

	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("sync log file: %w", err)
	}

	return nil
}

// Reopen closes the file and opens the path again. The server calls it on
// SIGHUP, after an external tool moved the file away. When the open fails,
// the sink keeps the old file.
func (s *Sink) Reopen() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.f == nil {
		return logsink.ErrClosed
	}

	f, err := openAppend(s.path)
	if err != nil {
		return err
	}

	old := s.f
	s.f = f

	if err := old.Close(); err != nil {
		return fmt.Errorf("close old log file: %w", err)
	}

	return nil
}

// Close implements logsink.Sink.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.f == nil {
		return nil
	}

	err := s.f.Close()
	s.f = nil

	if err != nil {
		return fmt.Errorf("close log file: %w", err)
	}

	return nil
}
