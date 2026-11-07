package service_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/registry"
)

const (
	refusedName    = "app"
	refusedVersion = "1.0.0"
)

// testError is a static error of these tests.
type testError string

func (e testError) Error() string { return string(e) }

const errAuditDown = testError("audit is down")

// refusedCase is one publish of the table of TestRefusedPublishWritesDeniedAuditEntry.
type refusedCase struct {
	name string
	// reason is the reason class of the denied entry. Empty means the publish
	// is accepted, and no denied entry is written.
	reason string
	// prior is true when the version is published before the refused publish.
	// The release then stays stored.
	prior bool
	// auditErr, when set, makes the audit store fail every append.
	auditErr error
	build    func(t *testing.T, e *env, sg pgpfixture.Signer) *set
}

// refusedCases returns the table of TestRefusedPublishWritesDeniedAuditEntry.
func refusedCases() []refusedCase {
	return []refusedCase{
		{
			name:     "signature from a key that the namespace does not trust",
			reason:   "signature",
			prior:    false,
			auditErr: nil,
			build: func(t *testing.T, _ *env, _ pgpfixture.Signer) *set {
				t.Helper()

				// The namespace trusts only sg. The set is signed with another key.
				return buildSet(t, newSigner(t), refusedName, refusedVersion, "linux_amd64")
			},
		},
		{
			name:     "archive set refused",
			reason:   "archive-set",
			prior:    false,
			auditErr: nil,
			build: func(t *testing.T, _ *env, sg pgpfixture.Signer) *set {
				t.Helper()

				s := buildSet(t, sg, refusedName, refusedVersion, "linux_amd64")
				// The checksums keep the hash of the first archive, so the check of
				// the archive set refuses the publish.
				s.archives[s.order[0]] = []byte("other archive bytes")

				return s
			},
		},
		{
			name:     "version exists with other content",
			reason:   "version-exists",
			prior:    true,
			auditErr: nil,
			build: func(t *testing.T, e *env, sg pgpfixture.Signer) *set {
				t.Helper()

				first := buildSet(t, sg, refusedName, refusedVersion, "linux_amd64")
				if _, err := e.publish(t, acme, first); err != nil {
					t.Fatalf("first publish: %v", err)
				}

				// The second set has other archives for the same version.
				return buildSet(t, sg, refusedName, refusedVersion, "linux_arm64")
			},
		},
		{
			name:     "archive too large for the release",
			reason:   "archive-too-large",
			prior:    false,
			auditErr: nil,
			build: func(t *testing.T, _ *env, sg pgpfixture.Signer) *set {
				t.Helper()

				// Four archives, each just under the 1 MiB archive limit of the test
				// config, sum to more than its 3 MiB release limit.
				s := buildSet(t, sg, refusedName, refusedVersion,
					"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64")
				big := bytes.Repeat([]byte("x"), (1<<20)-1)

				for _, file := range s.order {
					s.archives[file] = big
				}

				return s
			},
		},
		{
			name:     "audit write fails",
			reason:   "archive-set",
			prior:    false,
			auditErr: errAuditDown,
			build: func(t *testing.T, _ *env, sg pgpfixture.Signer) *set {
				t.Helper()

				s := buildSet(t, sg, refusedName, refusedVersion, "linux_amd64")
				s.archives[s.order[0]] = []byte("other archive bytes")

				return s
			},
		},
		{
			name:     "accepted publish writes no denied entry",
			reason:   "",
			prior:    false,
			auditErr: nil,
			build: func(t *testing.T, _ *env, sg pgpfixture.Signer) *set {
				t.Helper()

				return buildSet(t, sg, refusedName, refusedVersion, "linux_amd64")
			},
		},
	}
}

// TestRefusedPublishWritesDeniedAuditEntry checks that a refused publish writes
// exactly one denied entry to the namespace audit log, that it stores no
// release and no content key, and that a failed audit write fails the publish.
func TestRefusedPublishWritesDeniedAuditEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range refusedCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runRefusedCase(t, tc)
		})
	}
}

// runRefusedCase runs the publish of tc and checks the audit log, the keys and
// the release that the store holds.
func runRefusedCase(t *testing.T, tc refusedCase) {
	t.Helper()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	s := tc.build(t, e, sg)

	// The keys before the publish. A refused publish must leave them as they are.
	before := e.listing(t, e.ids[acme], "")

	e.aud.err = tc.auditErr

	_, err := e.publish(t, acme, s)

	if tc.reason == "" {
		checkAccepted(t, e, err)

		return
	}

	checkRefused(t, e, err, tc)

	if keys := e.listing(t, e.ids[acme], ""); !slices.Equal(keys, before) {
		t.Errorf("stored keys: got %v, want %v", keys, before)
	}

	checkRelease(t, e, tc)
}

// checkAccepted fails the test unless the publish succeeded and wrote no
// denied entry.
func checkAccepted(t *testing.T, e *env, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if entries := auditEntries(e); len(entries) != 0 {
		t.Errorf("audit entries: got %d, want 0", len(entries))
	}
}

// checkRefused fails the test unless the refused publish returned an error. An
// audit store that fails makes the error wrap its own error and writes no
// entry. Otherwise the audit log holds one denied entry with the reason of tc.
func checkRefused(t *testing.T, e *env, err error, tc refusedCase) {
	t.Helper()

	if tc.auditErr != nil {
		if !errors.Is(err, tc.auditErr) {
			t.Errorf("publish error: got %v, want it to wrap %v", err, tc.auditErr)
		}

		if entries := auditEntries(e); len(entries) != 0 {
			t.Errorf("audit entries: got %d, want 0", len(entries))
		}

		return
	}

	assertDenied(t, e, err, refusedVersion, tc.reason)
}

// checkRelease fails the test unless the store holds the release of the
// version when tc.prior is set, and holds no release otherwise.
func checkRelease(t *testing.T, e *env, tc refusedCase) {
	t.Helper()

	_, err := e.store.Get(t.Context(), releaseAddress(refusedName), refusedVersion)
	if tc.prior {
		if err != nil {
			t.Errorf("stored release: got error %v, want the first release", err)
		}

		return
	}

	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("stored release: got error %v, want %v", err, registry.ErrNotFound)
	}
}

// assertDenied checks that the publish returned an error and that the audit
// log holds exactly one denied entry with the given reason, for the version.
func assertDenied(t *testing.T, e *env, err error, version, reason string) {
	t.Helper()

	if err == nil {
		t.Fatal("publish: want an error, got nil")
	}

	entries := auditEntries(e)
	if len(entries) != 1 {
		t.Fatalf("audit entries: got %d, want 1", len(entries))
	}

	entry := entries[0]
	if entry.Outcome != audit.OutcomeDenied {
		t.Errorf("outcome: got %q, want %q", entry.Outcome, audit.OutcomeDenied)
	}

	if entry.Reason != reason {
		t.Errorf("reason: got %q, want %q", entry.Reason, reason)
	}

	if entry.Version != version {
		t.Errorf("version: got %q, want %q", entry.Version, version)
	}

	if entry.Namespace != acme {
		t.Errorf("namespace: got %q, want %q", entry.Namespace, acme)
	}

	if entry.Subject != e.caller.Principal.Subject {
		t.Errorf("subject: got %q, want %q", entry.Subject, e.caller.Principal.Subject)
	}
}

// TestRefusedModulePublishWritesDeniedAuditEntry checks that a module archive
// that the module check refuses writes one denied entry, and that nothing is
// stored for it.
func TestRefusedModulePublishWritesDeniedAuditEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	staged, err := e.svc.Stage(t.Context(), e.caller, acme, bytes.NewReader([]byte("not a module archive")))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	_, err = e.svc.PublishModule(t.Context(), e.caller, service.ModulePublishRequest{
		Namespace: acme, Name: "vpc", System: "aws", Version: refusedVersion, Upload: staged.Token,
	})

	assertDenied(t, e, err, refusedVersion, "module-archive")

	_, getErr := e.svc.ModuleRelease(t.Context(), acme, "vpc", "aws", refusedVersion)
	if !errors.Is(getErr, registry.ErrNotFound) {
		t.Errorf("stored module release: got error %v, want %v", getErr, registry.ErrNotFound)
	}

	if keys := e.listing(t, e.ids[acme], ""); len(keys) != 0 {
		t.Errorf("stored keys: got %v, want none", keys)
	}
}

// auditEntries returns a copy of the entries of the audit store of e.
func auditEntries(e *env) []audit.Entry {
	e.aud.mu.Lock()
	defer e.aud.mu.Unlock()

	return append([]audit.Entry(nil), e.aud.entries...)
}

// releaseAddress returns the address of the provider name in the acme namespace.
func releaseAddress(name string) registry.Address {
	return registry.Address{Namespace: acme, Kind: registry.KindProvider, Name: name, System: ""}
}
