package notifications_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/subscriptionsvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// errDiskFull stands for an error that no classify function knows.
var errDiskFull = errors.New("disk full")

// TestErrorsHaveCodes proves that each error of the notifications API answers
// its status with a code of the catalog and the arguments of the code.
func TestErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   string
		args   map[string]any
	}{
		{"too many sign-ins", identity.ErrRateLimited, http.StatusTooManyRequests, errcode.AuthRateLimited, nil},
		{"no credential", identity.ErrUnauthenticated, http.StatusUnauthorized, errcode.AuthUnauthenticated, nil},
		{"no CSRF header", sessionauth.ErrCSRF, http.StatusForbidden, errcode.SessionCSRF, nil},
		{"no permission", identity.ErrDenied, http.StatusForbidden, errcode.AccessDenied, nil},
		{
			"invalid namespace name", fmt.Errorf("parse: %w", namespace.ErrInvalidName),
			http.StatusBadRequest, errcode.NamespaceInvalidName, nil,
		},
		{"unknown namespace", namespace.ErrNotFound, http.StatusNotFound, errcode.NamespaceNotFound, nil},
		{"fenced write", namespace.ErrFenced, http.StatusConflict, errcode.NamespaceFenced, nil},
		{"other region", namespace.ErrNotHome, http.StatusMisdirectedRequest, errcode.NamespaceNotHome, nil},
		{
			"maintenance mode", maintenance.ErrInMaintenance,
			http.StatusServiceUnavailable, errcode.MaintenanceInMaintenance, nil,
		},
		{"unknown sink", notify.ErrNotFound, http.StatusNotFound, errcode.SinkNotFound, nil},
		{"changed sink", notify.ErrConflict, http.StatusConflict, errcode.SinkConflict, nil},
		{"no sender", sinks.ErrNoSender, http.StatusUnprocessableEntity, errcode.SinkNoSender, nil},
		{"no URL", notify.ErrNoURL, http.StatusUnprocessableEntity, errcode.SinkNoURL, nil},
		{
			"too many tests", fmt.Errorf("test: %w", &sinks.RateLimitedError{RetryAfter: 1500 * time.Millisecond}),
			http.StatusTooManyRequests, errcode.SinkTestRateLimited,
			map[string]any{"retryAfterSeconds": int64(2)},
		},
		{
			"limits cannot count", fmt.Errorf("test: %w: %w", sinks.ErrLimitUnavailable, errDiskFull),
			http.StatusServiceUnavailable, errcode.SinkTestLimitUnavailable, nil,
		},
		{"access token", sinks.ErrAccessToken, http.StatusBadRequest, errcode.SinkAccessToken, nil},
		{"platform host", sinks.ErrPlatformHost, http.StatusBadRequest, errcode.SinkPlatformHost, nil},
		{"invalid sink", notify.ErrInvalid, http.StatusBadRequest, errcode.SinkInvalid, nil},
		{"unknown error", errDiskFull, http.StatusInternalServerError, errcode.ServerInternal, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			notifications.WriteError(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: tc.args, RequestBody: nil})
		})
	}
}

// TestLimitUnavailableWithStoreOutage proves that a test limit whose counter
// wraps the error of a store that does not answer gives 503
// namespace.unavailable with Retry-After 5, not the code of the limit.
func TestLimitUnavailableWithStoreOutage(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	notifications.WriteError(t.Context(), rec,
		fmt.Errorf("test: %w: %w", sinks.ErrLimitUnavailable, namespace.ErrUnavailable))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After = %q, want 5", got)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusServiceUnavailable, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.NamespaceUnavailable, Args: nil, RequestBody: nil})
}

// TestRequestErrorsHaveCodes proves that the refusals of a request body and
// of a header answer 400 with their code of the catalog.
func TestRequestErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	f := newFixtureWithoutKeys(t)

	tests := []struct {
		name string
		req  request
		code string
	}{
		{"body that is no JSON", send("admin", http.MethodPost, base, "{"), errcode.RequestBodyInvalid},
		{"body with no kind", send("admin", http.MethodPost, base, `{"name":"a"}`), errcode.SinkInvalid},
		{
			"key on a server with no keys", keyed(send("admin", http.MethodPost, base, webhookBody(t, "a")), "k1"),
			errcode.RequestIdempotencyKeyUnsupported,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := f.do(t, tc.req)

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, []byte(got.body),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: []byte(tc.req.body)})
		})
	}
}

// TestSubscriptionErrorsHaveCodes proves that each error of the subscription
// and delivery routes answers its status with a code of the catalog.
func TestSubscriptionErrorsHaveCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		write  func(context.Context, http.ResponseWriter, error)
		err    error
		status int
		code   string
	}{
		{
			"unknown subscription", notifications.WriteSubscriptionError, notify.ErrNotFound,
			http.StatusNotFound, errcode.SinkSubscriptionNotFound,
		},
		{
			"changed subscription", notifications.WriteSubscriptionError, notify.ErrConflict,
			http.StatusConflict, errcode.SinkSubscriptionConflict,
		},
		{
			"invalid subscription", notifications.WriteSubscriptionError, notify.ErrInvalid,
			http.StatusBadRequest, errcode.SinkSubscriptionInvalid,
		},
		{
			"sink of another namespace", notifications.WriteSubscriptionError,
			fmt.Errorf("create: %w", subscriptionsvc.ErrSinkNotInNamespace),
			http.StatusUnprocessableEntity, errcode.SinkSubscriptionSinkNotFound,
		},
		{
			"fenced subscription write", notifications.WriteSubscriptionError, namespace.ErrFenced,
			http.StatusConflict, errcode.NamespaceFenced,
		},
		{
			"unknown delivery", notifications.WriteDeliveryError, notify.ErrNotFound,
			http.StatusNotFound, errcode.SinkDeliveryNotFound,
		},
		{
			"delivery that cannot be sent again", notifications.WriteDeliveryError,
			fmt.Errorf("redeliver: %w", errors.Join(subscriptionsvc.ErrNotRedeliverable, notify.ErrConflict)),
			http.StatusConflict, errcode.SinkDeliveryNotRedeliverable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			tc.write(t.Context(), rec, tc.err)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}
