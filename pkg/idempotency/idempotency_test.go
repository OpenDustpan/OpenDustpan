package idempotency_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
)

func TestParseKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		key  string
		ok   bool
	}{
		{"uuid", "3f2b8c1e-4d5a-4b6c-9d7e-1a2b3c4d5e6f", true},
		{"one character", "a", true},
		{"printable symbols and a space", "a b~!", true},
		{"255 characters", strings.Repeat("a", 255), true},
		{"256 characters", strings.Repeat("a", 256), false},
		{"empty", "", false},
		{"newline", "a\nb", false},
		{"non-ASCII", "clé", false},
		{"delete", "a\x7f", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := idempotency.ParseKey(tc.key)
			if tc.ok {
				if err != nil || string(got) != tc.key {
					t.Fatalf("ParseKey = %q, %v, want the key", got, err)
				}

				return
			}

			if !errors.Is(err, idempotency.ErrInvalidKey) {
				t.Fatalf("ParseKey err = %v, want ErrInvalidKey", err)
			}
		})
	}
}

func TestStorable(t *testing.T) {
	t.Parallel()

	for status, want := range map[int]bool{
		200: true, 201: true, 204: true, 400: false, 401: false, 403: false, 404: false, 409: false,
		410: true, 412: false, 413: true, 422: false, 423: false, 429: false, 500: false, 503: false, 302: false,
	} {
		if got := idempotency.Storable(status); got != want {
			t.Errorf("Storable(%d) = %t, want %t", status, got, want)
		}
	}
}

func TestFingerprint(t *testing.T) {
	t.Parallel()

	a := idempotency.Fingerprint([]byte("k1"), []byte("body"))

	if !bytes.Equal(a, idempotency.Fingerprint([]byte("k1"), []byte("body"))) {
		t.Error("the same key and material give another fingerprint")
	}

	if bytes.Equal(a, idempotency.Fingerprint([]byte("k2"), []byte("body"))) {
		t.Error("another server key gives the same fingerprint")
	}

	if strings.Contains(string(a), "body") {
		t.Error("the fingerprint holds the material")
	}
}

func TestCompletionFinish(t *testing.T) {
	t.Parallel()

	built := idempotency.Answer{Status: 201, Headers: nil, Body: []byte("x")}
	c := idempotency.NewCompletion(emptyRef(), "tok", func(result any) (idempotency.Answer, error) {
		if result != "r" {
			return idempotency.Answer{}, errUnexpectedResult
		}

		return built, nil
	})

	ctx := idempotency.WithCompletion(context.Background(), c)

	got, ok := idempotency.CompletionFrom(ctx)
	if !ok || got != c {
		t.Fatal("the context does not carry the completion")
	}

	if _, found := idempotency.CompletionFrom(context.Background()); found {
		t.Fatal("an empty context carries a completion")
	}

	lost := func(string, idempotency.Answer) error { return idempotency.ErrLost }
	if err := c.Finish("r", lost); !errors.Is(err, idempotency.ErrLost) || c.Stored() {
		t.Fatalf("Finish with a lost record: err = %v, stored = %t", err, c.Stored())
	}

	var token string

	if err := c.Finish("r", func(tok string, _ idempotency.Answer) error { token = tok; return nil }); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if token != "tok" || !c.Stored() {
		t.Fatalf("token = %q, stored = %t, want tok, true", token, c.Stored())
	}

	if err := c.Finish("other", lost); err == nil {
		t.Fatal("Finish with a result that Build refuses: err = nil")
	}
}

func TestCompletionFinishRefusesAnAnswerOverTheLimit(t *testing.T) {
	t.Parallel()

	big := idempotency.Answer{Status: 201, Headers: nil, Body: make([]byte, idempotency.MaxAnswerBytes+1)}
	c := idempotency.NewCompletion(emptyRef(), "tok", func(any) (idempotency.Answer, error) { return big, nil })
	stored := false

	err := c.Finish("r", func(string, idempotency.Answer) error { stored = true; return nil })
	if !errors.Is(err, idempotency.ErrInvalid) || stored || c.Stored() {
		t.Fatalf("Finish: err = %v, stored = %t, Stored() = %t, want ErrInvalid and nothing stored",
			err, stored, c.Stored())
	}

	big.Body = big.Body[:idempotency.MaxAnswerBytes]

	if err = c.Finish("r", func(string, idempotency.Answer) error { return nil }); err != nil {
		t.Fatalf("Finish at the limit: %v", err)
	}
}

// TestRetryAfterReopenReplays proves that a record in the durable data of a
// store answers a retry with the stored answer. The e2e test restarts the
// real server binary.
func TestRetryAfterReopenReplays(t *testing.T) {
	t.Parallel()

	store := idempotencytest.NewMemory()
	ref := startRef()
	material := []byte(`{"operation":"plan"}`)
	at := time.UnixMilli(1_700_000_000_000).UTC()
	answer := idempotency.Answer{
		Status:  201,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"id":"job-1"}`),
	}

	first, err := store.Begin(t.Context(), ref, material, at)
	if err != nil || first.Outcome != idempotency.Started {
		t.Fatalf("begin: outcome = %d, err = %v, want Started", first.Outcome, err)
	}

	if err = store.Complete(t.Context(), ref, first.Token, answer); err != nil {
		t.Fatalf("complete: %v", err)
	}

	got, err := store.Begin(t.Context(), ref, material, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("begin after the reopen: %v", err)
	}

	if got.Outcome != idempotency.Replayed {
		t.Fatalf("outcome after the reopen = %d, want Replayed", got.Outcome)
	}

	if got.Answer.Status != answer.Status || !bytes.Equal(got.Answer.Body, answer.Body) {
		t.Fatalf("answer after the reopen = %+v, want the stored answer", got.Answer)
	}
}

// TestRecordExpiresAfter24h proves that the retention of a job-start record is
// 24 hours: the record answers a retry at the end of the retention and the key
// is new after it.
func TestRecordExpiresAfter24h(t *testing.T) {
	t.Parallel()

	retention := idempotency.JobStartRetention()
	if retention.Period() != 24*time.Hour {
		t.Fatalf("job-start retention = %s, want 24h", retention.Period())
	}

	if _, err := idempotency.NewRetention(retention.Period() - time.Nanosecond); !errors.Is(
		err, idempotency.ErrInvalidRetention,
	) {
		t.Fatalf("NewRetention below the minimum: err = %v, want ErrInvalidRetention", err)
	}

	if _, err := idempotency.NewRetention(retention.Period()); err != nil {
		t.Fatalf("NewRetention at the minimum: %v", err)
	}

	store := idempotencytest.NewMemory()
	ref := startRef()
	material := []byte(`{"operation":"plan"}`)
	created := time.UnixMilli(1_700_000_000_000).UTC()

	first, err := store.Begin(t.Context(), ref, material, created)
	if err != nil || first.Outcome != idempotency.Started {
		t.Fatalf("begin: outcome = %d, err = %v, want Started", first.Outcome, err)
	}

	empty := idempotency.Answer{Status: 201, Headers: nil, Body: nil}
	if err = store.Complete(t.Context(), ref, first.Token, empty); err != nil {
		t.Fatalf("complete: %v", err)
	}

	end := created.Add(retention.Period())
	if got := beginAt(t, store, ref, material, end); got.Outcome != idempotency.Replayed {
		t.Fatalf("outcome at the end of the retention = %d, want Replayed", got.Outcome)
	}

	late := beginAt(t, store, ref, material, end.Add(time.Millisecond))
	if late.Outcome != idempotency.Started {
		t.Fatalf("outcome past the retention = %d, want Started", late.Outcome)
	}

	if late.Token == first.Token {
		t.Fatal("a start past the retention holds the token of the old record")
	}
}

// startRef returns the record of the job start of the tests.
func startRef() idempotency.Ref {
	return idempotency.Ref{
		Principal: "agent", Method: "POST", Path: "/api/dustpan/v1/namespaces/dev/jobs",
		Key: idempotency.Key("start-1"),
	}
}

// beginAt begins the request of ref at the given time and fails the test on
// an error.
func beginAt(
	t *testing.T, store idempotency.Store, ref idempotency.Ref, material []byte, at time.Time,
) idempotency.Result {
	t.Helper()

	res, err := store.Begin(t.Context(), ref, material, at)
	if err != nil {
		t.Fatalf("begin at %v: %v", at, err)
	}

	return res
}

var errUnexpectedResult = errors.New("unexpected result")

func emptyRef() idempotency.Ref {
	return idempotency.Ref{Principal: "", Method: "", Path: "", Key: ""}
}
