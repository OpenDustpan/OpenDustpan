// Package idempotent serves the Idempotency-Key header for the HTTP adapters
// of the tool-neutral API. A route that takes the key runs its handler inside
// Middleware.Handle. The first request with a key runs the handler and keeps
// its answer. A request with the same key and the same body gets that answer
// again and does not run the handler.
//
// The handler of a keyed route stores the answer in the transaction of its
// write: the store of the route calls idempotency.Completion.Finish as the
// last step of that transaction. A keyed route whose store never calls Finish
// stores no answer. The middleware then logs an error after a 2xx answer and
// frees the key, so a retry does the operation again.
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
	CodeRequired   = "request.idempotency-key-required"
	// CodeReplayedSecret answers a replay of a route whose answer holds a
	// secret. The server never keeps the secret, so the replay names the
	// object that the first request made.
	CodeReplayedSecret = "request.idempotency-key-replayed-secret"
	CodeReused         = "request.idempotency-key-reused"
	CodeInProgress     = "request.idempotency-key-in-progress"
	CodeBody           = "request.body-invalid"
)

// storeTimeout bounds the calls that end a request: a client that gives up
// must not leave a pending record behind.
const storeTimeout = 5 * time.Second

// replayObjectHeader is the reserved header of a stored answer that holds the
// ID of an object in place of an answer with a secret.
const replayObjectHeader = "X-Dustpan-Replay-Object"

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
	// ObjectID is the ID of the object that the first request made. It is
	// set for CodeReplayedSecret only.
	ObjectID string
	Status   int
}

// Error implements error.
func (e *Error) Error() string { return e.Message }

// Config holds the dependencies of a Middleware.
type Config struct {
	Store  idempotency.Store
	Logger *slog.Logger
	// Now overrides the clock of the store, for tests. Nil uses the clock of
	// the store, so the clock of one instance changes no decision.
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
		now = func() time.Time { return time.Time{} }
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
	// SecretAnswer is set when the answer holds a secret or a credential.
	// It returns the ID of the object that the write made, from the result
	// that Build gets. The record keeps that ID in place of the answer, and
	// a replay answers 409 with CodeReplayedSecret and the ID.
	SecretAnswer func(result any) (objectID string)
	// Serve runs the operation and writes its answer.
	Serve http.HandlerFunc
	// Principal names the caller.
	Principal string
	// Required says that the route needs the key: a request with no key
	// answers 400.
	Required bool
}

// Handle runs c.Serve for a request that has the key header. A request with
// no key header answers 400 when c.Required is set, else Handle runs c.Serve
// with no record.
//
// Handle returns an *Error for a refusal, and another error when the store
// fails. It returns nil when it wrote the answer: a replay, or the answer of
// c.Serve.
func (m *Middleware) Handle(w http.ResponseWriter, r *http.Request, c Call) error {
	key, err := keyOf(r)
	if err != nil {
		return err
	}

	if key == "" {
		if c.Required {
			return &Error{
				Status: http.StatusBadRequest, Code: CodeRequired, ObjectID: "",
				Message: "this route needs the Idempotency-Key header: send a new random key",
			}
		}

		c.Serve(w, r)

		return nil
	}

	body, err := m.readBody(w, r)
	if err != nil {
		return err
	}

	ref := idempotency.Ref{Principal: c.Principal, Method: r.Method, Path: r.URL.EscapedPath(), Key: key}

	// The query and the If-Match header are part of the request: another
	// query or another precondition is another request.
	material := append(append(slices.Clip(body), 0), r.URL.RawQuery...)
	material = append(append(material, 0), r.Header.Get("If-Match")...)

	res, err := m.store.Begin(r.Context(), ref, material, m.now())
	if err != nil {
		return fmt.Errorf("begin idempotent request: %w", err)
	}

	switch res.Outcome {
	case idempotency.Replayed:
		if id, secret := res.Answer.Headers[replayObjectHeader]; secret {
			return &Error{
				Status: http.StatusConflict, Code: CodeReplayedSecret, ObjectID: id,
				Message: fmt.Sprintf(
					"the first request with this Idempotency-Key made the object %s and answered a secret that the "+
						"server does not keep: read the object, or send a new key to make another", id,
				),
			}
		}

		w.Header().Set(ReplayedHeader, "true")
		Write(w, res.Answer)

		return nil
	case idempotency.Reused:
		return &Error{
			Status: http.StatusUnprocessableEntity, Code: CodeReused, ObjectID: "",
			Message: "this Idempotency-Key was used with another request: send a new key for a new request",
		}
	case idempotency.InProgress:
		return &Error{
			Status: http.StatusConflict, Code: CodeInProgress, ObjectID: "",
			Message: "the first request with this Idempotency-Key has not ended: retry later",
		}
	case idempotency.Started:
		m.run(w, r, ref, res.Token, c)

		return nil
	}

	return fmt.Errorf("%w: unknown outcome %d", idempotency.ErrInvalid, res.Outcome)
}

// keyOf returns the key of the request, or the empty key when the request
// has no header.
func keyOf(r *http.Request) (idempotency.Key, error) {
	values := r.Header.Values(Header)

	switch len(values) {
	case 0:
		return "", nil
	case 1:
	default:
		return "", &Error{
			Status: http.StatusBadRequest, Code: CodeInvalidKey, ObjectID: "",
			Message: "send the Idempotency-Key header one time",
		}
	}

	key, err := idempotency.ParseKey(values[0])
	if err != nil {
		return "", &Error{
			Status: http.StatusBadRequest, Code: CodeInvalidKey, ObjectID: "",
			Message: fmt.Sprintf(
				"the Idempotency-Key must have 1 to %d printable ASCII characters",
				idempotency.MaxKeyLength,
			),
		}
	}

	return key, nil
}

// readBody reads the body, up to the limit, and puts it back in the request.
func (m *Middleware) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, m.max))
	if err != nil {
		return nil, &Error{
			Status: http.StatusBadRequest, Code: CodeBody, ObjectID: "",
			Message: "the request body is not readable or is over the size limit",
		}
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	return body, nil
}

// run runs serve for the first request with the key, then keeps the answer
// or frees the key.
func (m *Middleware) run(w http.ResponseWriter, r *http.Request, ref idempotency.Ref, token string, c Call) {
	completion := idempotency.NewCompletion(ref, token, buildAnswer(c))
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

	// A 2xx answer means the operation ran. Its answer belongs in the
	// transaction of the write, and the store did not put it there. A store
	// outside that transaction could keep an answer for an operation that
	// rolled back, so the middleware keeps nothing and frees the key.
	if !completion.Stored() && rec.code() >= http.StatusOK && rec.code() < http.StatusMultipleChoices {
		m.logger.ErrorContext(r.Context(), "keyed route answered success and its store did not call Finish",
			slog.String("method", ref.Method), slog.String("path", ref.Path))
		m.end(r, ref, token, nil, 0)

		return
	}

	m.end(r, ref, token, rec, rec.code())
}

// buildAnswer returns the function that makes the answer to store. A route
// whose answer holds a secret stores the ID of the object in place of it.
func buildAnswer(c Call) func(result any) (idempotency.Answer, error) {
	if c.SecretAnswer == nil {
		return c.Build
	}

	return func(result any) (idempotency.Answer, error) {
		full, err := c.Build(result)
		if err != nil {
			return idempotency.Answer{}, err
		}

		return idempotency.Answer{
			Status:  full.Status,
			Headers: map[string]string{replayObjectHeader: c.SecretAnswer(result)},
			Body:    nil,
		}, nil
	}
}

// end keeps the answer in rec when the server stores its status, else it
// frees the key. It runs on a context that the client cannot cancel.
func (m *Middleware) end(r *http.Request, ref idempotency.Ref, token string, rec *recorder, status int) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), storeTimeout)
	defer cancel()

	if rec != nil && len(rec.body) > idempotency.MaxAnswerBytes {
		m.logger.WarnContext(ctx, "idempotent answer over the size limit not stored",
			slog.Int("bytes", len(rec.body)), slog.Int("limit", idempotency.MaxAnswerBytes))
	} else if rec != nil && idempotency.Storable(status) {
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
