package jobs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/secret"
)

const (
	// maxLogEntryBytes is the largest text of one log entry. A longer line is
	// split into entries of this size. The split depends on the bytes of the
	// log alone, so a resume with ?after finds the same entries.
	maxLogEntryBytes = 16 * 1024
	// defaultLogPoll is the time between two reads of a running phase.
	defaultLogPoll = 250 * time.Millisecond
	// maxAfterLength bounds the digits of the after value.
	maxAfterLength = 18
)

var errLogNotAvailable = errors.New("the phase has no log yet")

// logEntry is one line of the log stream. Seq counts the entries of the phase
// from 1, over the logs of its steps in order. Text is the line with its
// newline. A byte that is not UTF-8 reads as U+FFFD.
type logEntry struct {
	Seq  int64  `json:"seq"`
	Step string `json:"step"`
	Text string `json:"text"`
}

// logEnd is the last line of a stream that read to the end of the phase. A
// stream with no such line, or with a logFailure line, did not end well.
type logEnd struct {
	Done bool  `json:"done"`
	Last int64 `json:"last"`
}

// logFailure is the last line of a stream that stopped on an error.
type logFailure struct {
	Error logFailureBody `json:"error"`
}

type logFailureBody struct {
	ErrorCode string `json:"errorCode"`
	Message   string `json:"message"`
}

// logStep keeps what a stream has read of the log of one step record.
type logStep struct {
	// consumed is the number of bytes of the log that the stream wrote.
	consumed int64
	// size and closed are the values of the job record at the last read.
	size   int64
	closed bool
	read   bool
}

// logStream writes the entries of the log of one phase.
type logStream struct {
	h     *Handler
	w     http.ResponseWriter
	flush func()
	enc   *json.Encoder
	after int64
	seq   int64
	steps map[string]*logStep
}

// getLog answers GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/logs/{phase}
// with the log of the phase as lines of JSON, one entry for each line of the
// log. While the phase runs, the stream stays open and sends new entries. It
// ends with a line {"done":true} when the phase ended and the stream sent all
// of the log. The query value after is the seq of the last entry that the
// client has: the stream starts with the next one.
func (h *Handler) getLog(w http.ResponseWriter, r *http.Request, _ identity.Principal, t *target) {
	ctx := r.Context()

	after, err := parseAfter(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	kind := job.PhaseKind(r.PathValue("phase"))
	if kind != job.PhasePlan && kind != job.PhaseApply && kind != job.PhaseDeliver {
		h.writeError(ctx, w, errNotFound)

		return
	}

	current := t.job
	if !hasLog(&current, kind) {
		h.writeError(ctx, w, errLogNotAvailable)

		return
	}

	s := &logStream{
		h:     h,
		w:     w,
		flush: func() {},
		enc:   json.NewEncoder(w),
		after: after,
		seq:   0,
		steps: map[string]*logStep{},
	}
	// The controller reaches a writer below the wrappers of the server. A
	// writer that cannot flush still gets the whole stream at its end.
	rc := http.NewResponseController(w)
	s.flush = func() {
		if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			h.logger.DebugContext(ctx, "flush log stream failed", slog.String("error", err.Error()))
		}
	}

	s.follow(ctx, t, kind)
}

// parseAfter reads the after value: a whole number from 0, the seq of the
// last entry that the client has.
func parseAfter(r *http.Request) (int64, error) {
	query := queryOf(r)
	if err := query.Err(); err != nil {
		return 0, fmt.Errorf("%w: the query cannot be read", errInvalidQuery)
	}

	raw := query.Get("after")
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 || len(raw) > maxAfterLength {
		return 0, fmt.Errorf("%w: after must be a whole number from 0", errInvalidQuery)
	}

	return n, nil
}

// phaseOf returns the first phase of kind in j, or nil.
func phaseOf(j *job.Job, kind job.PhaseKind) *job.Phase {
	for i := range j.Phases {
		if j.Phases[i].Kind == kind {
			return &j.Phases[i]
		}
	}

	return nil
}

// hasLog reports whether the phase of kind in j has started, so its log is
// readable now or later. A phase that did not start has no log yet.
func hasLog(j *job.Job, kind job.PhaseKind) bool {
	p := phaseOf(j, kind)

	return p != nil && (!p.StartedAt.IsZero() || !p.FinishedAt.IsZero() || len(p.Steps) > 0)
}

// follow writes the log until the phase ends, the client leaves, or an error
// stops the stream.
func (s *logStream) follow(ctx context.Context, t *target, kind job.PhaseKind) {
	s.w.Header().Set("Content-Type", "application/x-ndjson")
	s.w.Header().Set("Cache-Control", "no-store")
	s.w.Header().Set("X-Content-Type-Options", "nosniff")
	s.w.WriteHeader(http.StatusOK)
	s.flush()

	poll := s.h.logPoll
	if poll <= 0 {
		poll = defaultLogPoll
	}

	current := t.job

	for {
		// The record is read before the log: when it says that the phase
		// ended, the log is final and one read gets all of it.
		ended := phaseEnded(&current, kind)

		if err := s.readPhase(ctx, t, &current, kind); err != nil {
			s.fail(ctx, err)

			return
		}

		s.flush()

		if ended {
			s.write(ctx, logEnd{Done: true, Last: s.seq})
			s.flush()

			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(poll):
		}

		fresh, err := s.h.jobs.Get(ctx, t.job.ID)
		if err != nil {
			s.fail(ctx, fmt.Errorf("get job: %w", err))

			return
		}

		// Defense in depth: the namespace and the unit of the job do not
		// change; a record that differs ends the stream.
		if fresh.Spec.Unit != t.job.Spec.Unit {
			s.fail(ctx, errNotFound)

			return
		}

		current = fresh
	}
}

// phaseEnded reports whether the phase of kind in j ended, or the job ended
// without it.
func phaseEnded(j *job.Job, kind job.PhaseKind) bool {
	p := phaseOf(j, kind)

	return p == nil || !p.FinishedAt.IsZero() || j.State.Terminal()
}

// readPhase writes the new entries of each step record of the phase.
func (s *logStream) readPhase(ctx context.Context, t *target, j *job.Job, kind job.PhaseKind) error {
	p := phaseOf(j, kind)
	if p == nil {
		return nil
	}

	for i := range p.Steps {
		rec := &p.Steps[i]
		if rec.Log == nil {
			continue
		}

		if err := s.readStep(ctx, t, rec); err != nil {
			return err
		}
	}

	return nil
}

// readStep writes the entries of the log of the step record that the stream
// has not written. It opens the log again only when the record shows more
// bytes or the end of the log.
func (s *logStream) readStep(ctx context.Context, t *target, rec *job.StepRecord) (err error) {
	log := rec.Log
	closed := !log.ClosedAt.IsZero()

	st := s.steps[log.ID]
	if st == nil {
		st = &logStep{consumed: 0, size: 0, closed: false, read: false}
		s.steps[log.ID] = st
	}

	if st.read && st.size == log.Size && st.closed == closed {
		return nil
	}

	rc, err := s.h.artifacts.OpenLog(ctx, t.ns.Name, log.ID)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}

	defer func() { err = errors.Join(err, rc.Close()) }()

	if _, err = io.CopyN(io.Discard, rc, st.consumed); err != nil {
		return fmt.Errorf("skip log: %w", err)
	}

	if copyErr := s.copyLines(ctx, rc, rec.Name, st, log); copyErr != nil {
		return copyErr
	}

	st.size, st.closed, st.read = log.Size, closed, true

	return nil
}

// copyLines writes the lines of rc. A last line with no newline waits for more
// bytes, unless the log is closed.
func (s *logStream) copyLines(
	ctx context.Context, rc io.Reader, step string, st *logStep, log *job.LogRef,
) error {
	closed := !log.ClosedAt.IsZero()

	br := bufio.NewReaderSize(failClosed{r: rc}, maxLogEntryBytes)

	for {
		line, err := br.ReadSlice('\n')

		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
			s.entry(ctx, step, line)
			st.consumed += int64(len(line))
		case errors.Is(err, io.EOF):
			// The bytes of a failed read are never written: only a read that
			// ends well gives its bytes to the client.
			if closed && len(line) > 0 {
				s.entry(ctx, step, line)
				st.consumed += int64(len(line))
			}

			return nil
		default:
			return fmt.Errorf("read log: %w", err)
		}
	}
}

// failClosed drops the bytes of a read that fails. A reader may return bytes
// together with an error; the buffer of bufio would then give those bytes to
// the stream before the error. Defense in depth: the log reader verifies each
// chunk before it returns its bytes; this wrapper keeps a reader that does
// not from serving the bytes of a chunk that failed its check.
type failClosed struct{ r io.Reader }

func (f failClosed) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		return n, io.EOF
	default:
		return 0, fmt.Errorf("read log: %w", err)
	}
}

// entry writes one entry, unless the client has it already.
func (s *logStream) entry(ctx context.Context, step string, text []byte) {
	s.seq++
	if s.seq > s.after {
		s.write(ctx, logEntry{Seq: s.seq, Step: step, Text: string(text)})
	}
}

func (s *logStream) write(ctx context.Context, v any) {
	if err := s.enc.Encode(v); err != nil {
		s.h.logger.WarnContext(ctx, "write log stream failed", slog.String("error", err.Error()))
	}
}

// fail ends the stream with an error line. The status is sent already, so the
// line carries the code of the error. The line has the fixed words of the
// code and never the text of the error, which can name a blob or a key.
func (s *logStream) fail(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}

	s.h.logger.WarnContext(ctx, "read job log failed", slog.String("error", err.Error()))

	body := logFailureBody{ErrorCode: errcode.ServerInternal, Message: "the log cannot be read"}

	switch {
	case errors.Is(err, secret.ErrShredded):
		body.Message = "the log was redacted"
	case errors.Is(err, secret.ErrDecrypt):
		body.Message = "a chunk of the log failed its check"
	case errors.Is(err, job.ErrNotFound), errors.Is(err, errNotFound):
		body = logFailureBody{ErrorCode: errcode.JobLogNotAvailable, Message: "the log is not available"}
	default:
		// Any other error keeps the fixed words of the internal error.
	}

	s.write(ctx, logFailure{Error: body})
	s.flush()
}
