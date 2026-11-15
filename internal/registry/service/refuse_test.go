package service_test

import (
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/audit"
)

// errStoreDown is the failure of a store in these tests.
const errStoreDown = testError("the store is down")

// TestRefusalReason checks the reason class of each refusal, and that a store
// error names no class.
func TestRefusalReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
		ok   bool
	}{
		{
			name: "integrity",
			err:  fmt.Errorf("store the file: %w", service.ErrIntegrity),
			want: "integrity",
			ok:   true,
		},
		{
			name: "no signing key",
			err:  fmt.Errorf("check the release: %w", service.ErrNoKeys),
			want: "no-signing-key",
			ok:   true,
		},
		{
			name: "store error",
			err:  fmt.Errorf("read the published release: %w", errStoreDown),
			want: "",
			ok:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reason, ok := service.ReasonOfRefusal(tc.err)
			if reason != tc.want || ok != tc.ok {
				t.Fatalf("ReasonOfRefusal = %q, %t, want %q, %t", reason, ok, tc.want, tc.ok)
			}
		})
	}
}

// refusalAuditCase is one refused publish of
// TestRefusedPublishWritesClassifiedAuditEntry.
type refusalAuditCase struct {
	name string
	// flip changes the staged bytes on their second read.
	flip bool
	// reason is the reason class of the denied entry.
	reason string
	want   error
	build  func(t *testing.T, e *env) error
}

// refusalAuditCases returns the table of
// TestRefusedPublishWritesClassifiedAuditEntry.
func refusalAuditCases() []refusalAuditCase {
	return []refusalAuditCase{
		{
			name:   "provider bytes change before the copy",
			flip:   true,
			reason: "integrity",
			want:   service.ErrIntegrity,
			build: func(t *testing.T, e *env) error {
				t.Helper()

				sg := newSigner(t)
				e.trust(t, acme, sg)

				_, err := e.publish(t, acme, buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))

				return err
			},
		},
		{
			name:   "module bytes change before the copy",
			flip:   true,
			reason: "integrity",
			want:   service.ErrIntegrity,
			build: func(t *testing.T, e *env) error {
				t.Helper()

				_, err := e.publishModule("1.0.0", moduleTarGz(t, "x"))

				return err
			},
		},
		{
			name:   "namespace without a trusted key",
			flip:   false,
			reason: "no-signing-key",
			want:   service.ErrNoKeys,
			build: func(t *testing.T, e *env) error {
				t.Helper()

				_, err := e.publish(t, acme, buildSet(t, newSigner(t), "payments", "1.0.0", "linux_amd64"))

				return err
			},
		},
	}
}

// TestRefusedPublishWritesClassifiedAuditEntry checks that an integrity refusal
// and a no-keys refusal each write exactly one denied entry with their reason
// class, and that the answer is the same error as before.
func TestRefusedPublishWritesClassifiedAuditEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range refusalAuditCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, tc.flip)

			err := tc.build(t, e)
			if !errors.Is(err, tc.want) {
				t.Fatalf("publish = %v, want %v", err, tc.want)
			}

			entries := auditEntries(e)
			if len(entries) != 1 {
				t.Fatalf("audit entries: got %d, want 1", len(entries))
			}

			entry := entries[0]
			if entry.Outcome != audit.OutcomeDenied || entry.Reason != tc.reason {
				t.Fatalf("audit entry = %+v, want denied with reason %q", entry, tc.reason)
			}
		})
	}
}
