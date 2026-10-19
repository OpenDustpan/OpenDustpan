package idempotent_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
)

const createdID = "obj-7"

// replayOutcome is what the second request with a key gets.
type replayOutcome struct {
	code     string
	objectID string
	status   int
}

// replayTwice sends one request twice with one key, to a route that answers
// the full answer, and returns what the second request gets and how many
// times the handler ran. It fails when the first answer is not the full
// answer or when the replay holds the secret.
func replayTwice(t *testing.T, secretID func(any) string) (replayOutcome, int) {
	t.Helper()

	full := idempotency.Answer{
		Status: http.StatusCreated, Headers: map[string]string{"Content-Type": "application/json"},
		Body: []byte(`{"id":"obj-7","token":"s3cret-value"}`),
	}
	store := idempotencytest.NewMemory()
	mw := newMiddleware(t, store, 1<<20)
	runs := 0
	c := idempotent.Call{
		Principal: caller, Build: builds(full), SecretAnswer: secretID, Required: false,
		Serve: func(w http.ResponseWriter, r *http.Request) {
			runs++

			finish(t, store, r)
			idempotent.Write(w, full)
		},
	}

	first, err := callWith(t, mw, c, nil)
	if err != nil || first.Code != http.StatusCreated || !strings.Contains(first.Body.String(), "s3cret-value") {
		t.Fatalf("first: %d %q, %v, want the full answer", first.Code, first.Body.String(), err)
	}

	again, err := callWith(t, mw, c, nil)
	if refusal, ok := errors.AsType[*idempotent.Error](err); ok {
		if strings.Contains(refusal.Message, "s3cret-value") || (again != nil && again.Body.Len() != 0) {
			t.Error("the replay holds the secret")
		}

		return replayOutcome{code: refusal.Code, objectID: refusal.ObjectID, status: refusal.Status}, runs
	}

	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	return replayOutcome{code: "", objectID: "", status: again.Code}, runs
}

func TestSecretAnswerKeepsTheObjectID(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		secretID func(any) string
		want     replayOutcome
	}{
		"a secret answer": {
			secretID: func(any) string { return createdID },
			want:     replayOutcome{code: idempotent.CodeReplayedSecret, objectID: createdID, status: http.StatusConflict},
		},
		"a plain answer": {
			secretID: nil,
			want:     replayOutcome{code: "", objectID: "", status: http.StatusCreated},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, runs := replayTwice(t, tc.secretID)
			if got != tc.want {
				t.Errorf("replay = %+v, want %+v", got, tc.want)
			}

			if runs != 1 {
				t.Errorf("the handler ran %d times, want 1", runs)
			}
		})
	}
}

// refusalOf returns the code and the status of the refusal in err, or empty
// values when err is nil.
func refusalOf(t *testing.T, err error) (string, int) {
	t.Helper()

	if err == nil {
		return "", 0
	}

	refusal, ok := errors.AsType[*idempotent.Error](err)
	if !ok {
		t.Fatalf("err = %v, want a refusal", err)
	}

	return refusal.Code, refusal.Status
}

func TestRequiredKey(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		header     string
		wantCode   string
		wantStatus int
		wantRuns   int
		required   bool
	}{
		"required and missing": {
			header: "", required: true, wantCode: idempotent.CodeRequired, wantStatus: http.StatusBadRequest, wantRuns: 0,
		},
		"required and sent":     {header: "k1", required: true, wantCode: "", wantStatus: 0, wantRuns: 1},
		"optional and missing":  {header: "", required: false, wantCode: "", wantStatus: 0, wantRuns: 1},
		"optional and key sent": {header: "k2", required: false, wantCode: "", wantStatus: 0, wantRuns: 1},
		"required and not valid": {
			header: "bad\x01", required: true, wantCode: idempotent.CodeInvalidKey, wantStatus: http.StatusBadRequest, wantRuns: 0,
		},
		"optional and not valid": {
			header: "bad\x01", required: false, wantCode: idempotent.CodeInvalidKey, wantStatus: http.StatusBadRequest, wantRuns: 0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mw := newMiddleware(t, idempotencytest.NewMemory(), 1<<20)
			runs := 0
			c := idempotent.Call{
				Principal: caller, Build: noAnswer, SecretAnswer: nil, Required: tc.required,
				Serve: func(w http.ResponseWriter, _ *http.Request) {
					runs++

					w.WriteHeader(http.StatusConflict)
				},
			}

			_, err := callWith(t, mw, c, func(r *http.Request) {
				r.Header.Del(idempotent.Header)

				if tc.header != "" {
					r.Header.Set(idempotent.Header, tc.header)
				}
			})

			code, status := refusalOf(t, err)
			if code != tc.wantCode || status != tc.wantStatus {
				t.Errorf("refusal = %d %q, want %d %q", status, code, tc.wantStatus, tc.wantCode)
			}

			if runs != tc.wantRuns {
				t.Errorf("the handler ran %d times, want %d", runs, tc.wantRuns)
			}
		})
	}
}

func TestIfMatchIsPartOfTheRequest(t *testing.T) {
	t.Parallel()

	store := idempotencytest.NewMemory()
	mw := newMiddleware(t, store, 1<<20)
	answer := idempotency.Answer{Status: http.StatusOK, Headers: nil, Body: nil}
	c := idempotent.Call{
		Principal: caller, Build: builds(answer), SecretAnswer: nil, Required: false,
		Serve: func(w http.ResponseWriter, r *http.Request) {
			finish(t, store, r)
			idempotent.Write(w, answer)
		},
	}
	ifMatch := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("If-Match", v) }
	}

	if _, err := callWith(t, mw, c, ifMatch(`"1"`)); err != nil {
		t.Fatalf("first: %v", err)
	}

	again, err := callWith(t, mw, c, ifMatch(`"1"`))
	if err != nil || again.Header().Get(idempotent.ReplayedHeader) != "true" {
		t.Errorf("same If-Match: %v %v, want a replay", err, again.Header())
	}

	_, err = callWith(t, mw, c, ifMatch(`"2"`))
	if refusal, ok := errors.AsType[*idempotent.Error](err); !ok || refusal.Code != idempotent.CodeReused ||
		refusal.Status != http.StatusUnprocessableEntity {
		t.Errorf("another If-Match: err = %v, want 422 %s", err, idempotent.CodeReused)
	}

	_, err = callWith(t, mw, c, nil)
	if refusal, ok := errors.AsType[*idempotent.Error](err); !ok || refusal.Code != idempotent.CodeReused {
		t.Errorf("no If-Match: err = %v, want %s", err, idempotent.CodeReused)
	}
}

// logged returns a Middleware whose log goes to the returned buffer.
func logged(t *testing.T, store idempotency.Store) (*idempotent.Middleware, *bytes.Buffer) {
	t.Helper()

	var out bytes.Buffer

	mw, err := idempotent.New(idempotent.Config{
		Store: store, Logger: slog.New(slog.NewTextHandler(&out, nil)), Now: nil, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return mw, &out
}

func TestASuccessWithoutFinishIsNotStored(t *testing.T) {
	t.Parallel()

	mw, out := logged(t, idempotencytest.NewMemory())
	runs := 0
	serve := func(w http.ResponseWriter, _ *http.Request) {
		runs++

		w.WriteHeader(http.StatusCreated)
	}

	for range 2 {
		if _, err := call(t, mw, serve); err != nil {
			t.Fatalf("call: %v", err)
		}
	}

	if runs != 2 {
		t.Errorf("the handler ran %d times, want 2: a 2xx answer outside the transaction is not stored", runs)
	}

	if !strings.Contains(out.String(), "level=ERROR") {
		t.Errorf("log = %q, want an error", out.String())
	}
}

func TestAnAnswerOverTheLimitIsNotStored(t *testing.T) {
	t.Parallel()

	mw, out := logged(t, idempotencytest.NewMemory())
	runs := 0
	big := bytes.Repeat([]byte("x"), idempotency.MaxAnswerBytes+1)
	serve := func(w http.ResponseWriter, _ *http.Request) {
		runs++

		w.WriteHeader(http.StatusGone)

		if _, err := w.Write(big); err != nil {
			t.Errorf("write: %v", err)
		}
	}

	for range 2 {
		got, err := call(t, mw, serve)
		if err != nil || got.Code != http.StatusGone || got.Body.Len() != len(big) {
			t.Fatalf("call: %d, %d bytes, %v, want the whole answer", got.Code, got.Body.Len(), err)
		}
	}

	if runs != 2 {
		t.Errorf("the handler ran %d times, want 2: the big answer is not stored", runs)
	}

	if !strings.Contains(out.String(), "over the size limit") {
		t.Errorf("log = %q, want the size limit", out.String())
	}
}
