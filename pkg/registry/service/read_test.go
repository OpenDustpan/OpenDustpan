package service_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/pgpfixture"
	"opendustpan.org/opendustpan/pkg/registry/service"
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

	// A later stage, after the grace period, removes the old file.
	later := e.now.Add(service.StagingGrace + time.Hour)

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
	sg := pgpfixture.NewSigner(t)
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
	sg := pgpfixture.NewSigner(t)
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

func TestAddKeyRefusals(t *testing.T) {
	t.Parallel()

	e := newEnv(t, false)

	for name, armored := range map[string][]byte{"garbage": []byte("not a key"), "empty": nil} {
		if _, err := e.svc.AddKey(context.Background(), e.caller, acme, armored); !errors.Is(err, registry.ErrBadKey) {
			t.Fatalf("%s: AddKey = %v, want ErrBadKey", name, err)
		}
	}

	_, err := e.svc.AddKey(context.Background(), e.caller, "gamma", pgpfixture.NewSigner(t).Key.Armored)
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
