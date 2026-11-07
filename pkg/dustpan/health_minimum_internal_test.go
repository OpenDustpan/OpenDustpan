package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// errMinimumRead stands for a check that cannot read the minimum because the
// store is not available.
var errMinimumRead = errors.New("minimum not readable")

// errChain stands for a restore that the audit chain does not admit.
var errChain = errors.New("chain waits")

// stubMinimum is a minimumChecker that answers err.
type stubMinimum struct {
	err error
}

// CheckBinaryMinimum implements minimumChecker.
func (s stubMinimum) CheckBinaryMinimum(context.Context) error {
	return s.err
}

// TestReadinessReportsABinaryBelowTheMinimum proves that the probe of a
// process whose binary is below the minimum binary version answers 503 and
// names the check binary-minimum, without the text of the
// versions. A process at the minimum, or a store without the check, answers
// 200.
func TestReadinessReportsABinaryBelowTheMinimum(t *testing.T) {
	t.Parallel()

	below := &schema.MinimumError{SchemaMinimum: "v0.5.0", BinaryVersion: "v0.4.0"}

	tests := []struct {
		name       string
		minimum    minimumChecker
		wantStatus int
		wantBody   string
	}{
		{
			name:       "below the minimum",
			minimum:    stubMinimum{err: below},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "binary-minimum",
		},
		{name: "at the minimum", minimum: stubMinimum{err: nil}, wantStatus: http.StatusOK, wantBody: ""},
		{
			name:       "minimum not readable",
			minimum:    stubMinimum{err: errMinimumRead},
			wantStatus: http.StatusOK,
			wantBody:   "",
		},
		{name: "store without the check", minimum: nil, wantStatus: http.StatusOK, wantBody: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rd := newComponentReadiness(readinessSettings{
				streamStop:  nil,
				components:  Components{api: true, worker: false},
				store:       stubPinger{err: nil, block: false},
				blobs:       stubPinger{err: nil, block: false},
				watch:       nil,
				chainAdmits: nil,
				minimum:     tt.minimum,
				writable:    nil,
				timeout:     50 * time.Millisecond,
				failures: metrics.NewRegistry().
					NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0),
				logger: slog.New(slog.DiscardHandler),
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)
			rec := httptest.NewRecorder()

			rd.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("probe = %d %s, want %d with %q", rec.Code, rec.Body.String(), tt.wantStatus, tt.wantBody)
			}

			for _, leak := range []string{"v0.4.0", "v0.5.0"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("probe body %s holds the version %s", rec.Body.String(), leak)
				}
			}
		})
	}
}

// TestRestoreStartCheck proves that the start check of a restore refuses a
// binary below the minimum and names both versions, that it passes a binary at
// the minimum, that a store without the check admits, and that the check of
// the audit chain still runs after it.
func TestRestoreStartCheck(t *testing.T) {
	t.Parallel()

	below := &schema.MinimumError{SchemaMinimum: "v0.5.0", BinaryVersion: "v0.4.0"}

	tests := []struct {
		name    string
		store   any
		chain   func(context.Context) error
		wantErr error
	}{
		{name: "below the minimum", store: stubMinimum{err: below}, chain: nil, wantErr: schema.ErrBinaryBelowMinimum},
		{name: "at the minimum", store: stubMinimum{err: nil}, chain: nil, wantErr: nil},
		{name: "store without the check", store: struct{}{}, chain: nil, wantErr: nil},
		{
			name: "at the minimum, chain waits", store: stubMinimum{err: nil},
			chain: func(context.Context) error { return errChain }, wantErr: errChain,
		},
		{
			name: "below the minimum, chain waits", store: stubMinimum{err: below},
			chain: func(context.Context) error { return errChain }, wantErr: schema.ErrBinaryBelowMinimum,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			admit := admitBoth(restoreMinimumAdmit(tt.store), tt.chain)
			if admit == nil {
				if tt.wantErr != nil {
					t.Fatal("no check, want a refusal")
				}

				return
			}

			if err := admit(t.Context()); !errors.Is(err, tt.wantErr) {
				t.Fatalf("admit = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestStoreDownListsOnlyTheStore proves that, when the store is not
// available, the probe lists only "store" in the failed list. The check of the
// binary minimum cannot read the minimum then, and it does not fail: the check
// of the store already reports the cause. A binary that is known to be below
// the minimum still fails, and then it is the only entry when the store works.
func TestStoreDownListsOnlyTheStore(t *testing.T) {
	t.Parallel()

	below := &schema.MinimumError{SchemaMinimum: "v0.5.0", BinaryVersion: "v0.4.0"}

	tests := []struct {
		name     string
		store    stubPinger
		minimum  stubMinimum
		wantBody string
	}{
		{
			name:  "store down, minimum not readable",
			store: stubPinger{err: errProbeStoreCanary, block: false}, minimum: stubMinimum{err: errMinimumRead},
			wantBody: `"failed":["store"]`,
		},
		{
			name: "store down, minimum not readable, wrapped as the stores return it",
			store: stubPinger{
				err:   errProbeStoreCanary,
				block: false,
			},
			minimum:  stubMinimum{err: fmt.Errorf("read: %w", errMinimumRead)},
			wantBody: `"failed":["store"]`,
		},
		{
			name:  "store works, binary below the minimum",
			store: stubPinger{err: nil, block: false}, minimum: stubMinimum{err: below},
			wantBody: `"failed":["binary-minimum"]`,
		},
		{
			name:  "store down and binary below the minimum",
			store: stubPinger{err: errProbeStoreCanary, block: false}, minimum: stubMinimum{err: below},
			wantBody: `"failed":["store","binary-minimum"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rd := newComponentReadiness(readinessSettings{
				streamStop: nil,
				components: Components{api: true, worker: false}, store: tt.store,
				blobs: stubPinger{err: nil, block: false}, watch: nil, chainAdmits: nil, minimum: tt.minimum,
				writable: nil, timeout: 50 * time.Millisecond,
				failures: metrics.NewRegistry().NewCounter(readinessFailuresName, "Failed readiness probes.", nil, 0),
				logger:   slog.New(slog.DiscardHandler),
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)
			rec := httptest.NewRecorder()

			rd.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("probe = %d %s, want 503 with %s", rec.Code, rec.Body.String(), tt.wantBody)
			}
		})
	}
}
