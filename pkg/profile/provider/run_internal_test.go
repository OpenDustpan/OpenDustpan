package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

// endingSource signs a token and, when ends is set, ends the block while it
// signs: it closes done, the done channel of the recorder, before it returns.
type endingSource struct {
	done chan struct{}
	tok  profile.Token
	once sync.Once
	ends bool
}

func (s *endingSource) Token(context.Context, string, time.Duration) (profile.Token, error) {
	if s.ends {
		s.once.Do(func() { close(s.done) })
	}

	return s.tok, nil
}

// endingRecorder returns the recorder of block "a", which is not closed, its
// source, and the entries that it gives to late.
func endingRecorder() (*recordingTokens, *endingSource, *[]Issued) {
	var tok profile.Token

	tok.Value, tok.ID, tok.Audience = "value-1", "jti-1", "aud"

	done := make(chan struct{})
	src := &endingSource{done: done, tok: tok, once: sync.Once{}, ends: false}
	late := &[]Issued{}
	rec := &recordingTokens{
		next: src, late: func(i Issued) { *late = append(*late, i) }, done: done, block: "a",
		values: nil, issued: nil, mu: sync.Mutex{}, closed: false,
	}

	return rec, src, late
}

// TestRecordingTokensEndDuringIssuance runs the recorder of one block with no
// goroutine, so the order of the end of the block and the record is fixed:
// the context ends while the issuer signs, and the recorder is not closed.
func TestRecordingTokensEndDuringIssuance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		end       bool
		wantValue string
	}{
		{name: "the context ends while the issuer signs", end: true, wantValue: ""},
		{name: "the context stays open", end: false, wantValue: "value-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec, src, late := endingRecorder()
			src.ends = tt.end

			tok, err := rec.Token(t.Context(), "aud", time.Minute)
			checkDelivery(t, &tok, err, tt.wantValue)

			if len(*late) != 0 {
				t.Fatalf("late got %d entries, want none: the recorder is not closed", len(*late))
			}

			issued, values := rec.close()
			if len(issued) != 1 || len(values) != 1 {
				t.Fatalf("close = %d entries and %d values, want the one signed token", len(issued), len(values))
			}

			delivered := tt.wantValue != ""
			if i := issued[0]; i.ID != "jti-1" || i.Block != "a" || i.Delivered != delivered {
				t.Fatalf("entry = %+v, want jti-1 of block a with Delivered %t", i, delivered)
			}
		})
	}
}

// checkDelivery checks that Token gave the value want, and that an empty want
// comes with ErrUnavailable.
func checkDelivery(t *testing.T, tok *profile.Token, err error, want string) {
	t.Helper()

	if tok.Value != want {
		t.Fatalf("Token value = %q, want %q (err %v)", tok.Value, want, err)
	}

	if want == "" && !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Token = %v, want ErrUnavailable", err)
	}

	if want != "" && err != nil {
		t.Fatalf("Token = %v, want no error", err)
	}
}
