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
	"unicode/utf8"

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

var (
	errLogNotAvailable = errors.New("the phase has no log yet")
	errTooManyStreams  = errors.New("too many open log streams: close one or wait")
)

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
	// Resume says that the stream ended at a limit and not at the end of the
	// phase: the client opens it again with ?after=<last>.
	Resume bool `json:"resume,omitempty"`
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
	h   *Handler
	w   http.ResponseWriter
	rc  *http.ResponseController
	enc *json.Encoder
	// failed is set when a write or a flush failed. The stream then ends.
	failed bool
	// lifeEnds is the time at which the stream ends with a resume line.
	lifeEnds time.Time
	after    int64
	seq      int64
	steps    map[string]*logStep
}

// getLog answers GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/logs/{phase}
// with the log of the phase as lines of JSON, one entry for each line of the
// log. While the phase runs, the stream stays open and sends new entries. It
// ends with a line {"done":true} when the phase ended and the stream sent all
// of the log. The query value after is the seq of the last entry that the
// client has: the stream starts with the next one.
func (h *Handler) getLog(w http.ResponseWriter, r *http.Request, p identity.Principal, t *target) {
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

	release, ok := h.streams.acquire(p.Subject)
	if !ok {
		h.writeError(ctx, w, errTooManyStreams)

		return
	}

	defer release()

	s := &logStream{
		h:      h,
		w:      w,
		rc:     http.NewResponseController(w),
		enc:    json.NewEncoder(w),
		failed: false, lifeEnds: time.Time{},
		after: after,
		seq:   0,
		steps: map[string]*logStep{},
	}

	s.follow(ctx, p, t, kind)
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

// follow writes the log until the phase ends, the client leaves, a limit is
// reached, or an error stops the stream.
func (s *logStream) follow(ctx context.Context, p identity.Principal, t *target, kind job.PhaseKind) {
	s.w.Header().Set("Content-Type", "application/x-ndjson")
	s.w.Header().Set("Cache-Control", "no-store")
	s.w.Header().Set("X-Content-Type-Options", "nosniff")
	s.w.WriteHeader(http.StatusOK)
	s.flush(ctx)

	s.lifeEnds = time.Now().Add(s.h.logs.MaxLife)
	current := t.job

	for !s.failed {
		finished := s.round(ctx, p, t, &current, kind)
		if finished {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.h.logs.Poll):
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

// round does one pass of the stream: it checks the grants, writes the new
// entries, and ends the stream when the phase ended or a limit is reached.
// It returns true when the stream ended.
func (s *logStream) round(
	ctx context.Context, p identity.Principal, t *target, current *job.Job, kind job.PhaseKind,
) bool {
	// Defense in depth: the route wrapper checked these actions at the start.
	// A grant that is revoked, or a caller that lost the unit, stops the
	// stream at the next poll.
	if err := s.authorize(ctx, p, &t.job); err != nil {
		s.fail(ctx, err)

		return true
	}

	// The record is read before the log: when it says that the phase ended,
	// the log is final and one read gets all of it.
	ended := phaseEnded(current, kind)

	if err := s.readPhase(ctx, t, current, kind); err != nil {
		s.fail(ctx, err)

		return true
	}

	s.flush(ctx)

	stopping := s.h.logs.Stopping
	limit := time.Now().After(s.lifeEnds) || (stopping != nil && stopping())

	switch {
	case ended:
		s.write(ctx, logEnd{Done: true, Last: s.seq, Resume: false})
	case limit:
		s.write(ctx, logEnd{Done: false, Last: s.seq, Resume: true})
	default:
		return false
	}

	s.flush(ctx)

	return true
}

// authorize checks again that p may read the namespace of the job, the job and
// its logs. A refusal and any other error end the stream.
func (s *logStream) authorize(ctx context.Context, p identity.Principal, j *job.Job) error {
	unit := j.Spec.Unit
	checks := []struct {
		action identity.Action
		obj    identity.Object
	}{
		{identity.ActionNamespaceRead, identity.NamespaceObject(unit.Namespace())},
		{identity.ActionJobRead, unit},
		{identity.ActionJobReadLogs, unit},
	}

	for i := range checks {
		c := &checks[i]
		if err := s.h.authz.Authorize(ctx, p, c.action, c.obj); err != nil {
			return fmt.Errorf("authorize %s: %w", c.action, err)
		}
	}

	return nil
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
// bytes, unless the log is closed. A line over maxLogEntryBytes is split at
// a rune boundary, so each entry holds whole runes of a UTF-8 log.
func (s *logStream) copyLines(
	ctx context.Context, rc io.Reader, step string, st *logStep, log *job.LogRef,
) error {
	closed := !log.ClosedAt.IsZero()
	br := bufio.NewReaderSize(failClosed{r: rc}, maxLogEntryBytes)

	var pending []byte

	for {
		line, err := br.ReadSlice('\n')
		pending = append(pending, line...)

		pending = s.cutLong(ctx, step, st, pending)

		switch {
		case err == nil:
			s.entry(ctx, step, pending)
			st.consumed += int64(len(pending))
			pending = pending[:0]
		case errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			// A last line with no newline is sent when the log is closed.
			if closed && len(pending) > 0 {
				s.entry(ctx, step, pending)
				st.consumed += int64(len(pending))
			}

			return nil
		default:
			return fmt.Errorf("read log: %w", err)
		}
	}
}

// cutLong writes the entries of pending that are over maxLogEntryBytes, each
// cut at a rune boundary, and returns the rest.
func (s *logStream) cutLong(ctx context.Context, step string, st *logStep, pending []byte) []byte {
	for len(pending) > maxLogEntryBytes {
		cut := maxLogEntryBytes
		for cut > 0 && !utf8.RuneStart(pending[cut]) {
			cut--
		}

		if cut == 0 {
			// Not UTF-8: split at the size.
			cut = maxLogEntryBytes
		}

		s.entry(ctx, step, pending[:cut])
		st.consumed += int64(cut)
		pending = pending[cut:]
	}

	return pending
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
	if s.failed {
		return
	}

	s.deadline(ctx)

	if err := s.enc.Encode(v); err != nil {
		s.failed = true
		s.h.logger.WarnContext(ctx, "write log stream failed", slog.String("error", err.Error()))
	}
}

// flush sends the written entries to the client.
func (s *logStream) flush(ctx context.Context) {
	if s.failed {
		return
	}

	s.deadline(ctx)

	if err := s.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.failed = true
		s.h.logger.DebugContext(ctx, "flush log stream failed", slog.String("error", err.Error()))
	}
}

// deadline sets the time limit of the next write or flush, so a client that
// does not read cannot hold the stream.
func (s *logStream) deadline(ctx context.Context) {
	err := s.rc.SetWriteDeadline(time.Now().Add(s.h.logs.WriteTimeout))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.failed = true
		s.h.logger.DebugContext(ctx, "set log stream deadline failed", slog.String("error", err.Error()))
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
	case errors.Is(err, identity.ErrDenied), errors.Is(err, errNotFound):
		body = logFailureBody{ErrorCode: errcode.AccessDenied, Message: "the caller may not read this log"}
	case errors.Is(err, job.ErrNotFound):
		body = logFailureBody{ErrorCode: errcode.JobLogNotAvailable, Message: "the log is not available"}
	default:
		// Any other error keeps the fixed words of the internal error.
	}

	s.write(ctx, logFailure{Error: body})
	s.flush(ctx)
}
