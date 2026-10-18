package httpexec_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/providerkit/httpexec"
)

var errNet = errors.New("connection reset")

func reply(status int, retryAfter string) httpexec.Reply {
	h := http.Header{}
	if retryAfter != "" {
		h.Set("Retry-After", retryAfter)
	}

	return httpexec.Reply{Status: status, Header: h, Body: nil}
}

func TestRetryPolicyNext(t *testing.T) {
	t.Parallel()

	none := httpexec.Reply{Status: 0, Header: nil, Body: nil}

	tests := []struct {
		name  string
		try   httpexec.Attempt
		retry bool
		wait  time.Duration
	}{
		{name: "503 after the first GET", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: reply(503, ""), Err: nil, CtxErr: nil,
		}, retry: true, wait: time.Second},
		{name: "503 after the third GET", try: httpexec.Attempt{
			Number: 3, Method: http.MethodGet, Idempotent: false, Reply: reply(503, ""), Err: nil, CtxErr: nil,
		}, retry: true, wait: 4 * time.Second},
		{name: "503 after the last attempt", try: httpexec.Attempt{
			Number: 4, Method: http.MethodGet, Idempotent: false, Reply: reply(503, ""), Err: nil, CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "400 is final", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: reply(400, ""), Err: nil, CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "429 with Retry-After", try: httpexec.Attempt{
			Number: 1, Method: http.MethodPut, Idempotent: false, Reply: reply(429, "7"), Err: nil, CtxErr: nil,
		}, retry: true, wait: 7 * time.Second},
		{name: "429 with Retry-After above the limit", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: reply(429, "3600"), Err: nil, CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "network error on a DELETE", try: httpexec.Attempt{
			Number: 1, Method: http.MethodDelete, Idempotent: false, Reply: none, Err: errNet, CtxErr: nil,
		}, retry: true, wait: time.Second},
		{name: "network error on a POST", try: httpexec.Attempt{
			Number: 1, Method: http.MethodPost, Idempotent: false, Reply: none, Err: errNet, CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "503 on an idempotent POST", try: httpexec.Attempt{
			Number: 1, Method: http.MethodPost, Idempotent: true, Reply: reply(503, ""), Err: nil, CtxErr: nil,
		}, retry: true, wait: time.Second},
		{name: "canceled context", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: none, Err: context.Canceled,
			CtxErr: context.Canceled,
		}, retry: false, wait: 0},
		{name: "operation deadline", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: none, Err: context.DeadlineExceeded,
			CtxErr: context.DeadlineExceeded,
		}, retry: false, wait: 0},
		{name: "timeout of one GET attempt", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: none, Err: context.DeadlineExceeded,
			CtxErr: nil,
		}, retry: true, wait: time.Second},
		{name: "timeout of one POST attempt", try: httpexec.Attempt{
			Number: 1, Method: http.MethodPost, Idempotent: false, Reply: none, Err: context.DeadlineExceeded,
			CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "503 after the operation ended", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: reply(503, ""), Err: nil,
			CtxErr: context.Canceled,
		}, retry: false, wait: 0},
		{name: "reply too large", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: none, Err: httpexec.ErrTooLarge,
			CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "another origin", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: none, Err: httpexec.ErrOtherOrigin,
			CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "Retry-After at the largest int64", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: reply(429, "9223372036854775807"), Err: nil,
			CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "Retry-After above the largest int64", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: reply(429, "99999999999999999999999"),
			Err: nil, CtxErr: nil,
		}, retry: false, wait: 0},
		{name: "Retry-After of 30 seconds", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: reply(503, "30"), Err: nil, CtxErr: nil,
		}, retry: true, wait: 30 * time.Second},
		{name: "negative Retry-After", try: httpexec.Attempt{
			Number: 1, Method: http.MethodGet, Idempotent: false, Reply: reply(503, "-5"), Err: nil, CtxErr: nil,
		}, retry: true, wait: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wait, retry := httpexec.DefaultRetry().Next(tt.try)
			if retry != tt.retry || wait != tt.wait {
				t.Fatalf("Next() = %v, %v, want %v, %v", wait, retry, tt.wait, tt.retry)
			}
		})
	}
}

func TestRetryPolicyWaitStopsAtMaxWait(t *testing.T) {
	t.Parallel()

	p := httpexec.DefaultRetry()
	p.MaxAttempts = 100

	wait, retry := p.Next(httpexec.Attempt{
		Number: 60, Method: http.MethodGet, Idempotent: false, Reply: reply(503, ""), Err: nil, CtxErr: nil,
	})
	if !retry || wait != p.MaxWait {
		t.Fatalf("Next() = %v, %v, want %v, true", wait, retry, p.MaxWait)
	}
}
