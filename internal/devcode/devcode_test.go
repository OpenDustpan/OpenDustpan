package devcode_test

import (
	"encoding/base64"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/devcode"
)

func TestRedeem(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		// steps are the attempts in order. A step may move the clock first.
		steps []step
	}{
		{name: "the code works once", steps: []step{
			{candidate: codeText, advance: 0, want: true},
			{candidate: codeText, advance: 0, want: false},
		}},
		{name: "a wrong candidate does not spend the code", steps: []step{
			{candidate: "wrong", advance: 0, want: false},
			{candidate: "", advance: 0, want: false},
			{candidate: codeText, advance: 0, want: true},
		}},
		{name: "the code works just before its lifetime ends", steps: []step{
			{candidate: codeText, advance: devcode.Lifetime - time.Nanosecond, want: true},
		}},
		{name: "the code stops at the end of its lifetime", steps: []step{
			{candidate: codeText, advance: devcode.Lifetime, want: false},
		}},
		{name: "a spent code stays spent", steps: []step{
			{candidate: codeText, advance: 0, want: true},
			{candidate: codeText, advance: time.Minute, want: false},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := start
			code, text, err := devcode.Make(func() time.Time { return clock })
			if err != nil {
				t.Fatalf("make code: %v", err)
			}

			for i, s := range tt.steps {
				clock = clock.Add(s.advance)

				candidate := s.candidate
				if candidate == codeText {
					candidate = text
				}

				if got := code.Redeem(candidate); got != s.want {
					t.Fatalf("step %d: Redeem = %v, want %v", i, got, s.want)
				}
			}
		})
	}
}

// codeText marks the real text of the code in a step.
const codeText = "<the code>"

type step struct {
	candidate string
	advance   time.Duration
	want      bool
}

func TestMakeText(t *testing.T) {
	t.Parallel()

	_, first, err := devcode.Make(nil)
	if err != nil {
		t.Fatalf("make first code: %v", err)
	}

	_, second, err := devcode.Make(nil)
	if err != nil {
		t.Fatalf("make second code: %v", err)
	}

	if first == second {
		t.Fatal("two codes have the same text")
	}

	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("decode code text %q: %v", first, err)
	}

	const want = 32
	if len(raw) != want {
		t.Fatalf("code holds %d bytes, want %d", len(raw), want)
	}
}

// TestRedeemConcurrent proves that callers at the same time spend the code
// once.
func TestRedeemConcurrent(t *testing.T) {
	t.Parallel()

	code, text, err := devcode.Make(nil)
	if err != nil {
		t.Fatalf("make code: %v", err)
	}

	const callers = 16

	var (
		wg   sync.WaitGroup
		wins atomic.Int64
	)

	for range callers {
		wg.Go(func() {
			if code.Redeem(text) {
				wins.Add(1)
			}
		})
	}

	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("%d callers spent the code, want 1", got)
	}
}
