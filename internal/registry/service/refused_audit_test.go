package service_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/registry/modulecheck"
	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
	"opendustpan.org/opendustpan/internal/registry/releasecheck"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
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
	// want is the error of the refused publish, or nil when it is accepted.
	want error
	// prior is true when the version is published before the refused publish.
	// The release then stays stored.
	prior bool
	// auditErr, when set, makes the audit store fail every append.
	auditErr error
	// beforeCommit, when set, runs at the start of the store write of the
	// publish. It changes the store between the checks and the commit.
	beforeCommit func(t *testing.T, e *env, sg pgpfixture.Signer)
	// checkStored, when set, checks the release that stays in the store.
	checkStored func(t *testing.T, e *env, sg pgpfixture.Signer)
	build       func(t *testing.T, e *env, sg pgpfixture.Signer) *set
}

// refusedCases returns the table of TestRefusedPublishWritesDeniedAuditEntry.
func refusedCases() []refusedCase {
	return []refusedCase{
		{
			name:         "signature from a key that the namespace does not trust",
			reason:       "signature",
			want:         registry.ErrUntrustedKey,
			prior:        false,
			auditErr:     nil,
			beforeCommit: nil,
			checkStored:  nil,
			build: func(t *testing.T, _ *env, _ pgpfixture.Signer) *set {
				t.Helper()

				// The namespace trusts only sg. The set is signed with another key.
				return buildSet(t, newSigner(t), refusedName, refusedVersion, "linux_amd64")
			},
		},
		{
			name:         "archive set refused",
			reason:       "archive-set",
			want:         releasecheck.ErrRefused,
			prior:        false,
			auditErr:     nil,
			beforeCommit: nil,
			checkStored:  nil,
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
			name:         "version exists with other content",
			reason:       "version-exists",
			want:         registry.ErrVersionExists,
			prior:        true,
			auditErr:     nil,
			beforeCommit: nil,
			checkStored:  nil,
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
			name:     "version exists in the commit",
			reason:   "version-exists",
			want:     registry.ErrVersionExists,
			prior:    true,
			auditErr: nil,
			beforeCommit: func(t *testing.T, e *env, sg pgpfixture.Signer) {
				t.Helper()

				e.store.beforeCommit = nil

				other := storedRelease(t, sg, refusedName, refusedVersion)
				err := e.store.Publish(
					t.Context(), other, namespaceOf(acme, e.ids[acme], e.now).Fence(), storedEntry(),
				)
				if err != nil {
					t.Fatalf("store the release of another publish: %v", err)
				}
			},
			checkStored: func(t *testing.T, e *env, sg pgpfixture.Signer) {
				t.Helper()

				got, err := e.store.Get(t.Context(), releaseAddress(refusedName), refusedVersion)
				if err != nil {
					t.Fatalf("stored release: %v", err)
				}

				if want := storedRelease(t, sg, refusedName, refusedVersion); !got.SameContent(want) {
					t.Errorf("stored release = %+v, want the release of the other publish %+v", got, want)
				}
			},
			build: func(t *testing.T, _ *env, sg pgpfixture.Signer) *set {
				t.Helper()

				return buildSet(t, sg, refusedName, refusedVersion, "linux_amd64")
			},
		},
		{
			name:     "signing key removed in the commit",
			reason:   "signature",
			want:     registry.ErrUntrustedKey,
			prior:    false,
			auditErr: nil,
			beforeCommit: func(t *testing.T, e *env, sg pgpfixture.Signer) {
				t.Helper()

				e.store.beforeCommit = nil

				if err := e.svc.RemoveKey(t.Context(), e.caller, acme, sg.Key.Fingerprint); err != nil {
					t.Fatalf("RemoveKey: %v", err)
				}
			},
			checkStored: nil,
			build: func(t *testing.T, _ *env, sg pgpfixture.Signer) *set {
				t.Helper()

				return buildSet(t, sg, refusedName, refusedVersion, "linux_amd64")
			},
		},
		{
			name:         "archive too large for the release",
			reason:       "archive-too-large",
			want:         service.ErrTooLarge,
			prior:        false,
			auditErr:     nil,
			beforeCommit: nil,
			checkStored:  nil,
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
			name:         "audit write fails",
			reason:       "archive-set",
			want:         releasecheck.ErrRefused,
			prior:        false,
			auditErr:     errAuditDown,
			beforeCommit: nil,
			checkStored:  nil,
			build: func(t *testing.T, _ *env, sg pgpfixture.Signer) *set {
				t.Helper()

				s := buildSet(t, sg, refusedName, refusedVersion, "linux_amd64")
				s.archives[s.order[0]] = []byte("other archive bytes")

				return s
			},
		},
		{
			name:         "accepted publish writes no denied entry",
			reason:       "",
			want:         nil,
			prior:        false,
			auditErr:     nil,
			beforeCommit: nil,
			checkStored:  nil,
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

	if tc.beforeCommit != nil {
		e.store.beforeCommit = func() { tc.beforeCommit(t, e, sg) }
	}

	_, err := e.publish(t, acme, s)

	if tc.reason == "" {
		checkAccepted(t, e, err)

		return
	}

	if tc.want != nil && !errors.Is(err, tc.want) {
		t.Errorf("publish error: got %v, want %v", err, tc.want)
	}

	checkRefused(t, e, err, tc)

	// A refusal that runs after move has already copied the staged files to
	// content keys. Those keys stay, as unreferenced keys, so only a refusal
	// before move keeps every key as it was.
	if tc.beforeCommit == nil {
		if keys := e.listing(t, e.ids[acme], ""); !slices.Equal(keys, before) {
			t.Errorf("stored keys: got %v, want %v", keys, before)
		}
	}

	checkRelease(t, e, tc)

	if tc.checkStored != nil {
		tc.checkStored(t, e, sg)
	}
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
		checkAuditLost(t, e, err, tc.auditErr)

		return
	}

	assertDenied(t, e, err, refusedVersion, tc.reason)
}

// checkAuditLost fails the test unless the refused publish keeps the refusal
// text only, writes no entry, and logs and counts the lost entry once. The
// text of the audit store must not reach the answer.
func checkAuditLost(t *testing.T, e *env, err, auditErr error) {
	t.Helper()

	if err == nil {
		t.Fatal("publish: want the refusal, got nil")
	}

	if !errors.Is(err, releasecheck.ErrRefused) {
		t.Errorf("publish error: got %v, want the archive set refusal", err)
	}

	if strings.Contains(err.Error(), auditErr.Error()) || errors.Is(err, errAuditDown) {
		t.Errorf("publish error carries the audit store text: %v", err)
	}

	if entries := auditEntries(e); len(entries) != 0 {
		t.Errorf("audit entries: got %d, want 0", len(entries))
	}

	if *e.failures != 1 {
		t.Errorf("audit failures counted: got %d, want 1", *e.failures)
	}

	if !strings.Contains(e.logs.String(), "audit append failed for a refused publish") {
		t.Errorf("log lines: got %q, want the audit append failure", e.logs.String())
	}
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

// moduleRefusedCase is one module publish of
// TestRefusedModulePublishWritesDeniedAuditEntry.
type moduleRefusedCase struct {
	name string
	// reason is the reason class of the denied entry.
	reason string
	// want is the error of the refused publish.
	want error
	// prior is true when the version is published before the refused publish.
	// The release then stays stored.
	prior bool
	// beforeCommit, when set, runs at the start of the store write of the
	// publish. It changes the store between the checks and the commit.
	beforeCommit func(t *testing.T, e *env, sg pgpfixture.Signer)
	// checkStored, when set, checks the release that stays in the store.
	checkStored func(t *testing.T, e *env)
	build       func(t *testing.T) []byte
}

// moduleRefusedCases returns the table of
// TestRefusedModulePublishWritesDeniedAuditEntry.
func moduleRefusedCases() []moduleRefusedCase {
	return []moduleRefusedCase{
		{
			name:         "module archive refused",
			reason:       "module-archive",
			want:         modulecheck.ErrRefused,
			prior:        false,
			beforeCommit: nil,
			checkStored:  nil,
			build: func(t *testing.T) []byte {
				t.Helper()

				return []byte("not a module archive")
			},
		},
		{
			name:   "version exists in the commit",
			reason: "version-exists",
			want:   registry.ErrVersionExists,
			prior:  true,
			beforeCommit: func(t *testing.T, e *env, _ pgpfixture.Signer) {
				t.Helper()

				e.store.beforeCommit = nil

				other := storedModuleRelease(t, "vpc", "aws", refusedVersion)
				err := e.store.Publish(
					t.Context(), other, namespaceOf(acme, e.ids[acme], e.now).Fence(), storedEntry(),
				)
				if err != nil {
					t.Fatalf("store the release of another publish: %v", err)
				}
			},
			checkStored: func(t *testing.T, e *env) {
				t.Helper()

				got, err := e.store.Get(t.Context(), moduleAddr(acme), refusedVersion)
				if err != nil {
					t.Fatalf("stored module release: %v", err)
				}

				if want := storedModuleRelease(t, "vpc", "aws", refusedVersion); !got.SameContent(want) {
					t.Errorf("stored release = %+v, want the release of the other publish %+v", got, want)
				}
			},
			build: func(t *testing.T) []byte {
				t.Helper()

				return moduleTarGz(t, "one")
			},
		},
		{
			name:   "store refuses the key in the commit",
			reason: "signature",
			want:   registry.ErrUntrustedKey,
			prior:  false,
			beforeCommit: func(t *testing.T, e *env, _ pgpfixture.Signer) {
				t.Helper()

				e.store.beforeCommit = nil

				// A module has no key, so no removal can cause this answer. The
				// store answers the key refusal as a fault, to prove that each
				// classified refusal of the write writes a denied entry.
				e.store.publishErr = registry.ErrKeyNotFound
			},
			checkStored: nil,
			build: func(t *testing.T) []byte {
				t.Helper()

				return moduleTarGz(t, "one")
			},
		},
	}
}

// TestRefusedModulePublishWritesDeniedAuditEntry checks that a refused module
// publish writes exactly one denied entry, and what it stores.
func TestRefusedModulePublishWritesDeniedAuditEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range moduleRefusedCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runModuleRefusedCase(t, tc)
		})
	}
}

// runModuleRefusedCase runs the module publish of tc and checks the audit log,
// the keys and the release that the store holds.
func runModuleRefusedCase(t *testing.T, tc moduleRefusedCase) {
	t.Helper()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	if tc.beforeCommit != nil {
		e.store.beforeCommit = func() { tc.beforeCommit(t, e, sg) }
	}

	staged, err := e.svc.Stage(t.Context(), e.caller, acme, bytes.NewReader(tc.build(t)))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	_, err = e.svc.PublishModule(t.Context(), e.caller, service.ModulePublishRequest{
		Namespace: acme, Name: "vpc", System: "aws", Version: refusedVersion, Upload: staged.Token,
	})

	if !errors.Is(err, tc.want) {
		t.Fatalf("PublishModule = %v, want %v", err, tc.want)
	}

	assertDenied(t, e, err, refusedVersion, tc.reason)

	_, getErr := e.svc.ModuleRelease(t.Context(), acme, "vpc", "aws", refusedVersion)
	if tc.prior {
		if getErr != nil {
			t.Errorf("stored module release: got error %v, want the first release", getErr)
		}
	} else if !errors.Is(getErr, registry.ErrNotFound) {
		t.Errorf("stored module release: got error %v, want %v", getErr, registry.ErrNotFound)
	}

	// A refusal that runs after move has already copied the archive to its
	// content key. That key stays, as an unreferenced key.
	if tc.beforeCommit == nil {
		if keys := e.listing(t, e.ids[acme], ""); len(keys) != 0 {
			t.Errorf("stored keys: got %v, want none", keys)
		}
	}

	if tc.checkStored != nil {
		tc.checkStored(t, e)
	}
}

// storedEntry returns the audit entry of a release that another publish stored.
func storedEntry() audit.Entry {
	var e audit.Entry

	e.At, e.Namespace, e.Subject = fixtureNow(), acme, subject
	e.Action, e.Object = string(identity.ActionRegistryPublish), acme.String()
	e.SourceIP, e.Channel, e.Kind, e.Outcome = "192.0.2.1", audit.ChannelAPI, audit.KindChange, audit.OutcomeSuccess

	return e
}

// storedRelease returns a valid provider release of name and version with
// content that differs from the release of the same version in these tests.
func storedRelease(t *testing.T, sg pgpfixture.Signer, name, version string) registry.Release {
	t.Helper()

	sum := hashOf([]byte("other content"))

	return registry.Release{
		PublishedAt:     fixtureNow(),
		Address:         releaseAddress(name),
		Version:         version,
		SumsFile:        releasecheck.SumsName(name, version),
		SumsSHA256:      sum,
		SignatureFile:   releasecheck.SignatureName(name, version),
		SignatureSHA256: sum,
		KeyFingerprint:  sg.Key.Fingerprint,
		PublishedBy:     subject,
		Protocols:       []string{"5.0"},
		Archives: []registry.Archive{{
			Platform: "linux_arm64",
			FileName: "terraform-provider-" + name + "_" + version + "_linux_arm64.zip",
			SHA256:   sum,
			Size:     int64(len("other content")),
		}},
		Files: nil,
	}
}

// storedModuleRelease returns a valid module release of name and system with
// content that differs from the release of the same version in these tests.
func storedModuleRelease(t *testing.T, name, system, version string) registry.Release {
	t.Helper()

	sum := hashOf([]byte("other module archive"))

	return registry.Release{
		PublishedAt:     fixtureNow(),
		Address:         registry.Address{Namespace: acme, Kind: registry.KindModule, Name: name, System: system},
		Version:         version,
		SumsFile:        "",
		SumsSHA256:      "",
		SignatureFile:   "",
		SignatureSHA256: "",
		KeyFingerprint:  "",
		PublishedBy:     subject,
		Protocols:       nil,
		Archives: []registry.Archive{{
			Platform: "",
			FileName: name + "-" + system + "-" + version + ".zip",
			SHA256:   sum,
			Size:     int64(len("other module archive")),
		}},
		Files: nil,
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
