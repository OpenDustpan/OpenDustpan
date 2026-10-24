package idempotent_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
)

// replayStore is a store whose Begin answers a replay of a stored answer
// that holds a secret, with the object ID id.
type replayStore struct {
	*idempotencytest.Memory

	id string
}

// Begin answers the replay.
func (s replayStore) Begin(context.Context, idempotency.Ref, []byte, time.Time) (idempotency.Result, error) {
	answer := idempotency.Answer{
		Status: http.StatusCreated, Headers: map[string]string{"X-Dustpan-Replay-Object": s.id}, Body: nil,
	}

	return idempotency.Result{Answer: answer, Token: "", Outcome: idempotency.Replayed}, nil
}

// lockedBuffer is a bytes.Buffer that the log handler and the test can share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write log: %w", err)
	}

	return n, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// TestRefusalWithoutCatalogAnswerIsLogged proves that a refusal whose code the
// catalog refuses answers 500 with server.internal, and that Handle logs the
// code and the cause. An object ID over the identifier limit makes the catalog
// refuse the answer of a replayed secret. A valid object ID answers its own
// code and logs nothing.
func TestRefusalWithoutCatalogAnswerIsLogged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		id         string
		wantStatus int
		wantCode   string
		wantLog    bool
	}{
		{
			name: "object ID over the limit", id: strings.Repeat("a", errcode.MaxIdentifier+1),
			wantStatus: http.StatusInternalServerError, wantCode: errcode.Internal().Code(), wantLog: true,
		},
		{
			name: "valid object ID", id: "obj-1",
			wantStatus: http.StatusConflict, wantCode: idempotent.CodeReplayedSecret, wantLog: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logs := &lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}}
			mw, err := idempotent.New(idempotent.Config{
				Store:  replayStore{Memory: idempotencytest.NewMemory(), id: tt.id},
				Logger: slog.New(slog.NewTextHandler(logs, nil)), Now: time.Now, MaxBodyBytes: 1024,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body1))
			req.Header.Set(idempotent.Header, key1)

			err = mw.Handle(httptest.NewRecorder(), req, plainCall(noAnswer, nil))

			refusal, ok := errors.AsType[*idempotent.Error](err)
			if !ok || refusal == nil {
				t.Fatalf("Handle = %v, want an *idempotent.Error", err)
			}

			if refusal.Status != tt.wantStatus || refusal.Answer.Code() != tt.wantCode {
				t.Fatalf("refusal = %d %q, want %d %q",
					refusal.Status, refusal.Answer.Code(), tt.wantStatus, tt.wantCode)
			}

			logged := strings.Contains(logs.String(), idempotent.CodeReplayedSecret)
			if logged != tt.wantLog {
				t.Fatalf("log names the code %t, want %t: %q", logged, tt.wantLog, logs.String())
			}
		})
	}
}
