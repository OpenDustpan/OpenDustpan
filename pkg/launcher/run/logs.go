package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"log/slog"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// logSink cuts the masked output of a step into chunks and stores each
// chunk through the session, in order. After a failed upload it drops the
// rest of the log, so the count and the hash of the end of the step name
// what the server holds.
type logSink struct {
	// put stores the chunk seq, with its retries.
	put  func(seq int, chunk []byte) error
	run  *phaseRun
	step string
	size int

	mu     sync.Mutex
	buf    []byte
	chunks int
	bytes  int
	hash   hash.Hash
	err    error
	capped bool
}

// newLogSink returns the sink of the log of step.
func newLogSink(ctx context.Context, r *phaseRun, step string) *logSink {
	size := r.cfg.ChunkSize
	if size <= 0 {
		size = DefaultChunkSize
	}

	return &logSink{
		put: func(seq int, chunk []byte) error {
			return r.retry(ctx, func() error { return r.sess.PutLog(ctx, step, seq, chunk) })
		},
		run: r, step: step, size: size, mu: sync.Mutex{}, buf: nil, chunks: 0, bytes: 0,
		hash: sha256.New(), err: nil, capped: false,
	}
}

// Write adds p to the log and sends each full chunk. It never fails, so the
// output of the step keeps flowing after a failed upload.
func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.buf = append(s.buf, p...)

	for len(s.buf) >= s.size {
		s.send(s.buf[:s.size])
		s.buf = s.buf[s.size:]
	}

	return len(p), nil
}

// flush sends what the sink holds.
func (s *logSink) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.buf) > 0 {
		s.send(s.buf)
		s.buf = nil
	}
}

// flushEvery flushes the sink at each tick of every, until the returned
// function is called.
func (s *logSink) flushEvery(every time.Duration) func() {
	done := make(chan struct{})
	ended := make(chan struct{})

	go func() {
		defer close(ended)

		t := time.NewTicker(every)
		defer t.Stop()

		for {
			select {
			case <-done:
				return
			case <-t.C:
				s.flush()
			}
		}
	}()

	return func() {
		close(done)
		<-ended
	}
}

// send stores one chunk. The caller holds mu.
func (s *logSink) send(chunk []byte) {
	if s.err != nil || s.capped {
		return
	}

	if s.chunks >= protocol.MaxLogChunksPerStep || s.bytes+len(chunk) > protocol.MaxLogBytesPerStep {
		s.capped = true
		s.run.log.Warn("the log of the step is above its cap: the rest is not stored", slog.String("step", s.step))

		return
	}

	data := append([]byte(nil), chunk...)

	if err := s.put(s.chunks, data); err != nil {
		s.err = err

		if errors.Is(err, session.ErrClaimLost) {
			s.run.markLost()
		} else {
			s.run.log.Warn("store the log of the step", slog.String("step", s.step), slog.Any("error", err))
		}

		return
	}

	s.chunks++
	s.bytes += len(data)
	s.hash.Write(data)
}

// sum returns the hex SHA-256 of the stored log, or "" for a log with no
// chunk.
func (s *logSink) sum() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.chunks == 0 {
		return ""
	}

	return hex.EncodeToString(s.hash.Sum(nil))
}
