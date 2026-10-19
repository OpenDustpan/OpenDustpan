package idempotency_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/idempotency"
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

var errUnexpectedResult = errors.New("unexpected result")

func emptyRef() idempotency.Ref {
	return idempotency.Ref{Principal: "", Method: "", Path: "", Key: ""}
}
