package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/registry/pgpfixture"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// stageError stages body as caller in ns and returns the error.
func (e *env) stageError(caller *service.Caller, ns namespace.Name, body io.Reader) error {
	_, err := e.svc.Stage(context.Background(), caller, ns, body)

	return err
}

func TestStageLimits(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	err := e.stageError(e.caller, acme, bytes.NewReader(make([]byte, 1<<20+1)))
	if !errors.Is(err, service.ErrTooLarge) {
		t.Fatalf("Stage of a large file = %v, want ErrTooLarge", err)
	}

	err = e.stageError(e.caller, acme, bytes.NewReader(nil))
	if !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("Stage of an empty file = %v, want ErrBadRequest", err)
	}

	err = e.stageError(e.caller, "gamma", strings.NewReader("x"))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("Stage in an unknown namespace = %v, want ErrNotFound", err)
	}

	if got := e.listing(t, e.ids[acme], "staging"); len(got) != 0 {
		t.Fatalf("a refused stage left files: %v", got)
	}

	st, err := e.svc.Stage(context.Background(), e.caller, acme, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	if st.SHA256 != hashOf([]byte("hello")) || st.Size != 5 {
		t.Fatalf("Stage = %+v, want the server's own hash and size", st)
	}
}

func TestStageSweepsOldFiles(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	first, err := e.svc.Stage(context.Background(), e.caller, acme, strings.NewReader("old"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	if got := e.listing(t, e.ids[acme], "staging"); len(got) != 1 {
		t.Fatalf("staged files = %v, want 1", got)
	}

	// A later stage, after the grace period, removes the old file. The age of
	// a staged file is its modification time in the file system, which is the
	// real time, so the later clock starts from the real time too.
	later := time.Now().Add(service.StagingGrace + time.Hour)

	ns := nsStub{}
	for name, id := range e.ids {
		ns[name] = namespaceOf(name, id, e.now)
	}

	svc, err := service.New(config(e.store, e.blobs, ns, later))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	if _, err = svc.Stage(context.Background(), e.caller, acme, strings.NewReader("new")); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	got := e.listing(t, e.ids[acme], "staging")
	if len(got) != 1 || strings.HasSuffix(got[0], first.Token) {
		t.Fatalf("staged files = %v, want only the new one", got)
	}
}

// TestOpenRefusals holds the rule that a content key opens only through a
// published release of the same namespace.
func TestOpenRefusals(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	rel, err := e.publish(t, acme, buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	staged, err := e.svc.Stage(context.Background(), e.caller, acme, strings.NewReader("secret staged bytes"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	known := rel.Archives[0].SHA256

	tests := []struct {
		name               string
		ns                 namespace.Name
		provider, ver, sum string
	}{
		{"the hash of a staged file", acme, "payments", "1.0.0", staged.SHA256},
		{"a release of another namespace", beta, "payments", "1.0.0", known},
		{"another version", acme, "payments", "2.0.0", known},
		{"another provider", acme, "ledger", "1.0.0", known},
		{"a hash that is not a hash", acme, "payments", "1.0.0", "../../etc/passwd"},
		{"an unknown namespace", "gamma", "payments", "1.0.0", known},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, r, openErr := e.svc.Open(context.Background(), tc.ns, tc.provider, tc.ver, tc.sum)
			if r != nil {
				closeQuietly(r)
			}

			if !errors.Is(openErr, registry.ErrNotFound) && !errors.Is(openErr, registry.ErrInvalid) {
				t.Fatalf("Open = %v, want ErrNotFound or ErrInvalid", openErr)
			}
		})
	}
}

func TestSigningKeyFailsClosedAfterRemoval(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	rel, err := e.publish(t, acme, buildSet(t, sg, "payments", "1.0.0", "linux_amd64"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	key, keyErr := e.svc.SigningKey(context.Background(), rel)
	if keyErr != nil || key.Fingerprint != sg.Key.Fingerprint {
		t.Fatalf("SigningKey = %+v, %v", key, keyErr)
	}

	if err = e.svc.RemoveKey(context.Background(), e.caller, acme, sg.Key.Fingerprint); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	if _, err = e.svc.SigningKey(context.Background(), rel); !errors.Is(err, registry.ErrKeyNotFound) {
		t.Fatalf("SigningKey after removal = %v, want ErrKeyNotFound", err)
	}

	if err = e.svc.RemoveKey(context.Background(), e.caller, acme, "../x"); !errors.Is(err, registry.ErrInvalid) {
		t.Fatalf("RemoveKey with a bad fingerprint = %v, want ErrInvalid", err)
	}
}

// TestServableReleasesLeaveOutARemovedKey holds the rule that the version list
// of the registry protocol offers no release whose signing key was removed, so
// a client picks an older release that it can download.
func TestServableReleasesLeaveOutARemovedKey(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	old, current := newSigner(t), newSigner(t)
	e.trust(t, acme, old)
	e.trust(t, acme, current)

	for version, sg := range map[string]pgpfixture.Signer{"1.0.0": old, "2.0.0": current} {
		if _, err := e.publish(t, acme, buildSet(t, sg, "payments", version, "linux_amd64")); err != nil {
			t.Fatalf("Publish %s: %v", version, err)
		}
	}

	versions := func() []string {
		rels, err := e.svc.ServableReleases(context.Background(), acme, "payments")
		if err != nil {
			return nil
		}

		out := make([]string, 0, len(rels))
		for _, rel := range rels {
			out = append(out, rel.Version)
		}

		return out
	}

	if got := versions(); !slices.Equal(got, []string{"1.0.0", "2.0.0"}) {
		t.Fatalf("versions = %v, want both", got)
	}

	if err := e.svc.RemoveKey(context.Background(), e.caller, acme, current.Key.Fingerprint); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	if got := versions(); !slices.Equal(got, []string{"1.0.0"}) {
		t.Fatalf("versions after the removal = %v, want only 1.0.0", got)
	}

	if err := e.svc.RemoveKey(context.Background(), e.caller, acme, old.Key.Fingerprint); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	_, err := e.svc.ServableReleases(context.Background(), acme, "payments")
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("ServableReleases with no trusted key = %v, want ErrNotFound", err)
	}
}

func TestAddKeyRefusals(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	for name, armored := range map[string][]byte{"garbage": []byte("not a key"), "empty": nil} {
		if _, err := e.svc.AddKey(context.Background(), e.caller, acme, armored); !errors.Is(err, registry.ErrBadKey) {
			t.Fatalf("%s: AddKey = %v, want ErrBadKey", name, err)
		}
	}

	_, err := e.svc.AddKey(context.Background(), e.caller, "gamma", newSigner(t).Key.Armored)
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("AddKey in an unknown namespace = %v, want ErrNotFound", err)
	}

	if len(e.store.keys[acme]) != 0 {
		t.Fatal("a refused key was stored")
	}
}

func TestCallerWithoutSubject(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	anon := callerOf("")

	if err := e.stageError(anon, acme, strings.NewReader("x")); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("Stage = %v, want ErrUnauthenticated", err)
	}

	if err := e.stageError(nil, acme, strings.NewReader("x")); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("Stage with no caller = %v, want ErrUnauthenticated", err)
	}
}

func TestProviders(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	// Two versions of one provider list the provider once.
	for _, rel := range []struct{ name, version string }{
		{"vault", "1.0.0"}, {"dns", "1.0.0"}, {"dns", "1.1.0"},
	} {
		if _, err := e.publish(t, acme, buildSet(t, sg, rel.name, rel.version, "linux_amd64")); err != nil {
			t.Fatalf("publish %s %s: %v", rel.name, rel.version, err)
		}
	}

	tests := []struct {
		name    string
		ns      namespace.Name
		want    []string
		wantErr error
	}{
		{"providers ordered by name", acme, []string{"dns", "vault"}, nil},
		{"a namespace with no provider", beta, []string{}, nil},
		{"a namespace that does not exist", "gamma", nil, registry.ErrNotFound},
		{"a name that is not a namespace", "../acme", nil, registry.ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, next, err := e.svc.Providers(t.Context(), tt.ns, "", registry.MaxAddressPage)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Providers(%q) error = %v, want %v", tt.ns, err, tt.wantErr)
			}

			if tt.wantErr == nil && (!slices.Equal(got, tt.want) || next != "") {
				t.Errorf("Providers(%q) = %v, %q, want %v, no cursor", tt.ns, got, next, tt.want)
			}
		})
	}
}

// TestProvidersReadsPages holds the rule that Providers reads one page of the
// store per call: with more providers than one page, a walk over the pages
// holds every provider once, in order, and each store read returns at most
// one address more than the page size.
func TestProvidersReadsPages(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)
	sg := newSigner(t)
	e.trust(t, acme, sg)

	// More providers than one page, with two versions each. A second version
	// of one provider does not make a second address.
	const count = 60

	want := make([]string, 0, count)

	for i := range count {
		name := fmt.Sprintf("provider-%02d", i)
		want = append(want, name)

		for _, version := range []string{"1.0.0", "1.1.0"} {
			if _, err := e.publish(t, acme, buildSet(t, sg, name, version, "linux_amd64")); err != nil {
				t.Fatalf("publish %s %s: %v", name, version, err)
			}
		}
	}

	const pageSize = 7

	if got := walkProviders(t, e, pageSize, count); !slices.Equal(got, want) {
		t.Fatalf("Providers = %v, want %v", got, want)
	}

	checkAddressCalls(t, e.store.addressCalls, pageSize)
}

// walkProviders reads the providers of acme page by page with the page size,
// and returns the names in page order. It fails when a page is empty before
// the last one, or when a cursor is not the last name of its page.
func walkProviders(t *testing.T, e *env, pageSize, maxPages int) []string {
	t.Helper()

	var (
		got   []string
		after string
	)

	for pages := 0; ; pages++ {
		if pages > maxPages {
			t.Fatalf("the pages did not end after %d pages", pages)
		}

		page, next, err := e.svc.Providers(t.Context(), acme, after, pageSize)
		if err != nil {
			t.Fatalf("Providers(after %q): %v", after, err)
		}

		if len(page) == 0 {
			t.Fatalf("Providers(after %q) = no provider before the last page", after)
		}

		got = append(got, page...)

		if next == "" {
			return got
		}

		if next != page[len(page)-1] {
			t.Fatalf("next cursor = %q, want the last name %q", next, page[len(page)-1])
		}

		after = next
	}
}

// checkAddressCalls fails when a store read did not use the page size, or
// when it read more address rows than one page and the one row that tells
// whether a next page exists.
func checkAddressCalls(t *testing.T, calls []addressCall, pageSize int) {
	t.Helper()

	if len(calls) < 2 {
		t.Fatalf("the store read %d page(s), want more than one", len(calls))
	}

	for i, call := range calls {
		if call.limit != pageSize {
			t.Fatalf("store read %d used limit %d, want %d", i, call.limit, pageSize)
		}

		if call.rows > call.limit+1 {
			t.Fatalf("store read %d read %d rows, want at most %d", i, call.rows, call.limit+1)
		}

		if call.returned > call.limit {
			t.Fatalf("store read %d returned %d addresses, want at most %d", i, call.returned, call.limit)
		}
	}
}
