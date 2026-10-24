package idempotent_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
)

const target = "/api/dustpan/v1/things"

func newMiddleware(t *testing.T, store idempotency.Store, maxBody int64) *idempotent.Middleware {
	t.Helper()

	mw, err := idempotent.New(idempotent.Config{
		Store: store, Logger: slog.New(slog.DiscardHandler), Now: time.Now, MaxBodyBytes: maxBody,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return mw
}

var errNoResult = errors.New("no write passes a result here")

func noAnswer(any) (idempotency.Answer, error) { return idempotency.Answer{}, errNoResult }

// Values that most calls of the tests send.
const (
	caller = "alice"
	key1   = "k1"
	body1  = "body"
)

// call runs one request with the key key1 and the body body1 through Handle.
func call(t *testing.T, mw *idempotent.Middleware, serve http.HandlerFunc) (*httptest.ResponseRecorder, error) {
	t.Helper()

	return callWith(t, mw, plainCall(noAnswer, serve), nil)
}

// plainCall returns the Call of a route with an answer that holds no secret
// and a key that is optional.
func plainCall(build func(any) (idempotency.Answer, error), serve http.HandlerFunc) idempotent.Call {
	return idempotent.Call{Principal: caller, Build: build, Serve: serve, SecretAnswer: nil, Required: false}
}

// callWith runs one request with the key key1 and the body body1 through
// Handle, with the Call c. edit changes the request first, if set.
func callWith(
	t *testing.T, mw *idempotent.Middleware, c idempotent.Call, edit func(*http.Request),
) (*httptest.ResponseRecorder, error) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target+"?x=1", strings.NewReader(body1))
	req.Header.Set(idempotent.Header, key1)

	if edit != nil {
		edit(req)
	}

	rec := httptest.NewRecorder()

	return rec, mw.Handle(rec, req, c)
}

// finish does what the store of a keyed route does as the last step of its
// transaction: it stores the answer that the Build of the Call makes.
func finish(t *testing.T, store idempotency.Store, r *http.Request) {
	t.Helper()

	completion, ok := idempotency.CompletionFrom(r.Context())
	if !ok {
		t.Error("the context carries no completion")

		return
	}

	ctx := r.Context()

	err := completion.Finish(nil, func(token string, a idempotency.Answer) error {
		return store.Complete(ctx, completion.Ref, token, a)
	})
	if err != nil {
		t.Errorf("Finish: %v", err)
	}
}

// builds returns a Build that makes the answer a.
func builds(a idempotency.Answer) func(any) (idempotency.Answer, error) {
	return func(any) (idempotency.Answer, error) { return a, nil }
}

func TestNewNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	store := idempotencytest.NewMemory()

	for name, cfg := range map[string]idempotent.Config{
		"no store":  {Store: nil, Logger: logger, Now: nil, MaxBodyBytes: 1},
		"no logger": {Store: store, Logger: nil, Now: nil, MaxBodyBytes: 1},
		"no limit":  {Store: store, Logger: logger, Now: nil, MaxBodyBytes: 0},
	} {
		if _, err := idempotent.New(cfg); !errors.Is(err, idempotent.ErrMissingDependency) {
			t.Errorf("%s: err = %v, want ErrMissingDependency", name, err)
		}
	}
}

func TestStoresAnAnswerThatTheHandlerWrote(t *testing.T) {
	t.Parallel()

	store := idempotencytest.NewMemory()
	mw := newMiddleware(t, store, 1<<20)
	runs := 0
	stored := idempotency.Answer{
		Status: http.StatusAccepted, Headers: map[string]string{"Content-Type": "text/plain"}, Body: []byte("done"),
	}
	serve := func(w http.ResponseWriter, r *http.Request) {
		runs++

		got, readErr := io.ReadAll(r.Body)
		if readErr != nil || string(got) != body1 {
			t.Errorf("the handler read %q, %v, want the body again", got, readErr)
		}

		finish(t, store, r)
		w.Header().Set("X-Not-Kept", "1")
		idempotent.Write(w, stored)
	}
	c := plainCall(builds(stored), serve)

	first, err := callWith(t, mw, c, nil)
	if err != nil || first.Code != http.StatusAccepted {
		t.Fatalf("first: %d, %v", first.Code, err)
	}

	again, err := callWith(t, mw, c, nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	if runs != 1 {
		t.Errorf("the handler ran %d times, want 1", runs)
	}

	checkReplay(t, again)
}

// checkReplay checks the replay of the answer of TestStoresAnAnswerThatTheHandlerWrote.
func checkReplay(t *testing.T, got *httptest.ResponseRecorder) {
	t.Helper()

	header := got.Header()

	if got.Code != http.StatusAccepted || got.Body.String() != "done" || header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("replay: %d %q %v", got.Code, got.Body.String(), header)
	}

	if header.Get("Content-Type") != "text/plain" || header.Get("X-Not-Kept") != "" {
		t.Errorf("replay headers = %v, want the content type and no other header", header)
	}
}

func TestRefusals(t *testing.T) {
	t.Parallel()

	store := idempotencytest.NewMemory()
	mw := newMiddleware(t, store, 8)
	serve := func(w http.ResponseWriter, r *http.Request) {
		finish(t, store, r)
		w.WriteHeader(http.StatusNoContent)
	}
	build := builds(idempotency.Answer{Status: http.StatusNoContent, Headers: nil, Body: nil})

	if _, err := callWith(t, mw, plainCall(build, serve), nil); err != nil {
		t.Fatalf("first: %v", err)
	}

	for name, tc := range map[string]struct {
		key, body, code string
		status          int
	}{
		"another body": {"k1", "other", idempotent.CodeReused, http.StatusUnprocessableEntity},
		"empty key":    {"", "body", idempotent.CodeInvalidKey, http.StatusBadRequest},
		"body too big": {"k2", "0123456789", idempotent.CodeBody, http.StatusBadRequest},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target+"?x=1", strings.NewReader(tc.body))
		req.Header.Set(idempotent.Header, tc.key)

		err := mw.Handle(httptest.NewRecorder(), req, plainCall(noAnswer, serve))

		refusal, ok := errors.AsType[*idempotent.Error](err)
		if !ok || refusal.Answer.Code() != tc.code || refusal.Status != tc.status {
			t.Errorf("%s: err = %v, want %d %s", name, err, tc.status, tc.code)
		}
	}

	// Another query is another request.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target+"?x=2", strings.NewReader("body"))
	req.Header.Set(idempotent.Header, "k1")

	err := mw.Handle(httptest.NewRecorder(), req, plainCall(noAnswer, serve))
	if refusal, ok := errors.AsType[*idempotent.Error](err); !ok || refusal.Answer.Code() != idempotent.CodeReused {
		t.Errorf("another query: err = %v, want %s", err, idempotent.CodeReused)
	}
}

func TestAnErrorAnswerFreesTheKey(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, idempotencytest.NewMemory(), 1<<20)
	runs := 0

	for _, status := range []int{http.StatusConflict, http.StatusInternalServerError, http.StatusBadRequest} {
		serve := func(w http.ResponseWriter, _ *http.Request) {
			runs++

			w.WriteHeader(status)
		}

		if _, err := call(t, mw, serve); err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
	}

	if runs != 3 {
		t.Errorf("the handler ran %d times, want 3: an error answer is not stored", runs)
	}

	// A handler that wrote nothing answered 200. Its store did not call
	// Finish, so the middleware keeps no answer and frees the key.
	empties := 0
	nothing := func(http.ResponseWriter, *http.Request) { empties++ }

	for range 2 {
		if _, err := call(t, mw, nothing); err != nil {
			t.Fatalf("empty handler: %v", err)
		}
	}

	if empties != 2 {
		t.Errorf("the empty handler ran %d times, want 2: no answer is stored without Finish", empties)
	}
}

func TestAPanicFreesTheKey(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, idempotencytest.NewMemory(), 1<<20)

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Error("the panic did not reach the caller")
			}
		}()

		_, callErr := call(t, mw, func(http.ResponseWriter, *http.Request) { panic("boom") })
		t.Errorf("call returned %v after a panic", callErr)
	}()

	ran := false
	serve := func(w http.ResponseWriter, _ *http.Request) {
		ran = true

		w.WriteHeader(http.StatusCreated)
	}

	if _, err := call(t, mw, serve); err != nil || !ran {
		t.Errorf("after a panic: err = %v, ran = %t, want the handler to run", err, ran)
	}
}

func TestACanceledClientStillFreesTheKey(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, idempotencytest.NewMemory(), 1<<20)
	ctx, cancel := context.WithCancel(t.Context())

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader("body"))
	req.Header.Set(idempotent.Header, "k1")

	gone := func(w http.ResponseWriter, _ *http.Request) {
		cancel() // The client gives up while the operation runs.
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	err := mw.Handle(httptest.NewRecorder(), req, plainCall(noAnswer, gone))
	if err != nil {
		t.Fatalf("canceled request: %v", err)
	}

	ran := false
	serve := func(w http.ResponseWriter, _ *http.Request) {
		ran = true

		w.WriteHeader(http.StatusCreated)
	}

	if _, err = call(t, mw, serve); err != nil || !ran {
		t.Errorf("retry after a canceled client: err = %v, ran = %t, want the handler to run", err, ran)
	}
}

func TestPresent(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, http.NoBody)
	if idempotent.Present(req) {
		t.Error("Present on a request with no header")
	}

	req.Header.Set(idempotent.Header, "")

	if !idempotent.Present(req) {
		t.Error("an empty header is present: it is an invalid key, never ignored")
	}
}

func TestAFailedCommitFreesTheKey(t *testing.T) {
	t.Parallel()

	mw := newMiddleware(t, idempotencytest.NewMemory(), 1<<20)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body1))
	req.Header.Set(idempotent.Header, key1)

	build := func(any) (idempotency.Answer, error) {
		return idempotency.Answer{Status: http.StatusCreated, Headers: nil, Body: nil}, nil
	}

	// The write builds its answer in its transaction, then the commit fails
	// and the handler answers 500. The record holds no answer in the store.
	failed := func(w http.ResponseWriter, r *http.Request) {
		completion, ok := idempotency.CompletionFrom(r.Context())
		if !ok {
			t.Error("the context carries no completion")

			return
		}

		keep := func(string, idempotency.Answer) error { return nil }

		if finishErr := completion.Finish(nil, keep); finishErr != nil {
			t.Errorf("Finish: %v", finishErr)
		}

		w.WriteHeader(http.StatusInternalServerError)
	}

	err := mw.Handle(httptest.NewRecorder(), req, plainCall(build, failed))
	if err != nil {
		t.Fatalf("failed commit: %v", err)
	}

	ran := false
	serve := func(w http.ResponseWriter, _ *http.Request) {
		ran = true

		w.WriteHeader(http.StatusCreated)
	}

	if _, err = call(t, mw, serve); err != nil || !ran {
		t.Errorf("retry after a failed commit: err = %v, ran = %t, want the handler to run", err, ran)
	}
}
