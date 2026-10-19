// Package idempotent serves the Idempotency-Key header for the HTTP adapters
// of the tool-neutral API. A route that takes the key runs its handler inside
// Middleware.Handle. The first request with a key runs the handler and keeps
// its answer. A request with the same key and the same body gets that answer
// again and does not run the handler.
package idempotent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
)

// Header is the request header that carries the key.
const Header = "Idempotency-Key"

// ReplayedHeader is the answer header of a replay.
const ReplayedHeader = "Idempotent-Replayed"

// Error codes of the refusals of the middleware.
const (
	CodeInvalidKey = "request.idempotency-key-invalid"
	CodeReused     = "request.idempotency-key-reused"
	CodeInProgress = "request.idempotency-key-in-progress"
	CodeBody       = "request.body-invalid"
)

// storeTimeout bounds the calls that end a request: a client that gives up
// must not leave a pending record behind.
const storeTimeout = 5 * time.Second

// storedHeaders are the answer headers that a replay sends again.
//
//nolint:gochecknoglobals // A fixed list; Go has no constant slices.
var storedHeaders = []string{"Content-Type", "Etag", "Location", "Cache-Control"}

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// Error is a refusal of the middleware. The adapter writes it in its own
// error shape.
type Error struct {
	Code    string
	Message string
	Status  int
}

// Error implements error.
func (e *Error) Error() string { return e.Message }

// Config holds the dependencies of a Middleware.
type Config struct {
	Store  idempotency.Store
	Logger *slog.Logger
	// Now is the clock. Nil uses time.Now.
	Now func() time.Time
	// MaxBodyBytes bounds a request body that the middleware reads.
	MaxBodyBytes int64
}

// Middleware serves the Idempotency-Key header.
type Middleware struct {
	store  idempotency.Store
	logger *slog.Logger
	now    func() time.Time
	max    int64
}

// New returns a Middleware.
func New(cfg Config) (*Middleware, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: idempotency store", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	case cfg.MaxBodyBytes < 1:
		return nil, fmt.Errorf("%w: body limit", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Middleware{store: cfg.Store, logger: cfg.Logger, now: now, max: cfg.MaxBodyBytes}, nil
}

// Present reports whether the request has the header. A header with an empty
// value is present, and an invalid key.
func Present(r *http.Request) bool {
	return len(r.Header.Values(Header)) > 0
}

// Call is a request that has the key header.
type Call struct {
	// Build makes the answer of the request from the result of its write, for
	// a store that stores the answer in the transaction of the write. Serve
	// writes that same answer.
	Build func(result any) (idempotency.Answer, error)
	// Serve runs the operation and writes its answer.
	Serve http.HandlerFunc
	// Principal names the caller.
	Principal string
}

// Handle runs c.Serve for a request that has the key header.
//
// Handle returns an *Error for a refusal, and another error when the store
// fails. It returns nil when it wrote the answer: a replay, or the answer of
// c.Serve.
func (m *Middleware) Handle(w http.ResponseWriter, r *http.Request, c Call) error {
	values := r.Header.Values(Header)
	if len(values) != 1 {
		return &Error{
			Status:  http.StatusBadRequest,
			Code:    CodeInvalidKey,
			Message: "send the Idempotency-Key header one time",
		}
	}

	key, err := idempotency.ParseKey(values[0])
	if err != nil {
		return &Error{
			Status: http.StatusBadRequest,
			Code:   CodeInvalidKey,
			Message: fmt.Sprintf(
				"the Idempotency-Key must have 1 to %d printable ASCII characters",
				idempotency.MaxKeyLength,
			),
		}
	}

	body, err := m.readBody(w, r)
	if err != nil {
		return err
	}

	ref := idempotency.Ref{Principal: c.Principal, Method: r.Method, Path: r.URL.EscapedPath(), Key: key}

	// The query is part of the request: another query is another request.
	material := append(append(slices.Clip(body), 0), r.URL.RawQuery...)

	res, err := m.store.Begin(r.Context(), ref, material, m.now())
	if err != nil {
		return fmt.Errorf("begin idempotent request: %w", err)
	}

	switch res.Outcome {
	case idempotency.Replayed:
		w.Header().Set(ReplayedHeader, "true")
		Write(w, res.Answer)

		return nil
	case idempotency.Reused:
		return &Error{
			Status: http.StatusUnprocessableEntity, Code: CodeReused,
			Message: "this Idempotency-Key was used with another request: send a new key for a new request",
		}
	case idempotency.InProgress:
		return &Error{
			Status: http.StatusConflict, Code: CodeInProgress,
			Message: "the first request with this Idempotency-Key has not ended: retry later",
		}
	case idempotency.Started:
		m.run(w, r, ref, res.Token, c)

		return nil
	}

	return fmt.Errorf("%w: unknown outcome %d", idempotency.ErrInvalid, res.Outcome)
}

// readBody reads the body, up to the limit, and puts it back in the request.
func (m *Middleware) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, m.max))
	if err != nil {
		return nil, &Error{
			Status: http.StatusBadRequest, Code: CodeBody,
			Message: "the request body is not readable or is over the size limit",
		}
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	return body, nil
}

// run runs serve for the first request with the key, then keeps the answer
// or frees the key.
func (m *Middleware) run(w http.ResponseWriter, r *http.Request, ref idempotency.Ref, token string, c Call) {
	completion := idempotency.NewCompletion(ref, token, c.Build)
	rec := &recorder{ResponseWriter: w, status: 0, header: map[string]string{}, body: nil}
	finished := false

	// A panic or an early return frees the key: no answer is kept for it.
	defer func() {
		if !finished {
			m.end(r, ref, token, nil, 0)
		}
	}()

	c.Serve(rec, r.WithContext(idempotency.WithCompletion(r.Context(), completion)))

	finished = true

	// A write that stored the answer and then failed to commit answers an
	// error status: the record is still pending, so end frees the key.
	if completion.Stored() && idempotency.Storable(rec.code()) {
		return
	}

	m.end(r, ref, token, rec, rec.code())
}

// end keeps the answer in rec when the server stores its status, else it
// frees the key. It runs on a context that the client cannot cancel.
func (m *Middleware) end(r *http.Request, ref idempotency.Ref, token string, rec *recorder, status int) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), storeTimeout)
	defer cancel()

	if rec != nil && idempotency.Storable(status) {
		err := m.store.Complete(
			ctx,
			ref,
			token,
			idempotency.Answer{Headers: rec.header, Body: rec.body, Status: status},
		)
		if err == nil {
			return
		}

		m.logger.WarnContext(ctx, "idempotent answer not stored", slog.String("error_kind", errorKind(err)))
	}

	if err := m.store.Abandon(ctx, ref, token); err != nil {
		m.logger.WarnContext(ctx, "idempotent request not freed", slog.String("error_kind", errorKind(err)))
	}
}

func errorKind(err error) string {
	switch {
	case errors.Is(err, idempotency.ErrLost):
		return "lost"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout"
	default:
		return "store"
	}
}

// Write sends a. The handler of a route that takes the key calls it for the
// answer that build makes, so the stored answer and the sent answer are one.
func Write(w http.ResponseWriter, a idempotency.Answer) {
	for name, value := range a.Headers {
		w.Header().Set(name, value)
	}

	w.WriteHeader(a.Status)

	if len(a.Body) > 0 {
		//nolint:errcheck // The client closed the connection: nothing to do.
		_, _ = w.Write(a.Body)
	}
}

// recorder passes an answer through and keeps a copy of its status, its
// stored headers and its body.
type recorder struct {
	http.ResponseWriter

	header map[string]string
	body   []byte
	status int
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status

		for _, name := range storedHeaders {
			if v := r.Header().Get(name); v != "" {
				r.header[name] = v
			}
		}
	}

	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}

	r.body = append(r.body, p...)

	n, err := r.ResponseWriter.Write(p)
	if err != nil {
		return n, fmt.Errorf("write answer: %w", err)
	}

	return n, nil
}

// Unwrap lets http.ResponseController reach the writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// code returns the status of the answer. A handler that wrote nothing
// answered 200.
func (r *recorder) code() int {
	if r.status == 0 {
		return http.StatusOK
	}

	return r.status
}
