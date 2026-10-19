package objectcontext_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// recordStore records the fence and the audit entry of the last write.
type recordStore struct {
	objectcontext.Store

	fence namespace.Fence
	entry audit.Entry
}

// SetAPI implements objectcontext.Store.
//
//nolint:revive // The signature of the objectcontext.Store port.
func (s *recordStore) SetAPI(
	_ context.Context, object objectcontext.Ref, _ objectcontext.Patch, _ string, fence namespace.Fence, e audit.Entry,
) (objectcontext.Entry, error) {
	s.fence, s.entry = fence, e

	return *apiEntry(object, fieldsOf("", "", nil, nil)), nil
}

// DeleteAPI implements objectcontext.Store.
func (s *recordStore) DeleteAPI(_ context.Context, _ objectcontext.Ref, fence namespace.Fence, e audit.Entry) error {
	s.fence, s.entry = fence, e

	return objectcontext.ErrNotFound
}

// namespaceStore answers one namespace, acme, at failover version 4.
type namespaceStore struct {
	namespace.Store
}

// GetNamespace implements namespace.Store.
func (namespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name != "acme" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: "ns-aaaaaaaaaaaaaaaaaaaa", CreatedAt: time.Time{}, Name: name, HomeRegion: "test-region",
		FailoverVersion: 4, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// auditBy returns an audit entry that names only the subject.
func auditBy(subject string) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: subject, IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: "",
		Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

func TestWriter(t *testing.T) {
	t.Parallel()

	noPorts := objectcontext.WriterConfig{Store: nil, Namespaces: nil, Now: nil}
	if _, err := objectcontext.NewWriter(noPorts); !errors.Is(err, objectcontext.ErrMissingDependency) {
		t.Fatalf("NewWriter with no port: %v, want ErrMissingDependency", err)
	}

	now := time.UnixMilli(1_800_000_000_000)
	store := &recordStore{Store: nil, fence: namespace.Fence{Namespace: "", FailoverVersion: 0}, entry: auditBy("")}

	w, err := objectcontext.NewWriter(objectcontext.WriterConfig{
		Store: store, Namespaces: namespaceStore{Store: nil}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	ref := objectcontext.NamespaceRef("acme")
	patch := objectcontext.Patch{Summary: nil, Runbook: nil, OnCall: new("pager:x"), Owners: nil, Links: nil}

	if _, err = w.Set(t.Context(), ref, patch, "user:ada", auditBy("user:ada")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if store.fence.FailoverVersion != 4 || store.fence.Namespace != "acme" || !store.entry.At.Equal(now) {
		t.Errorf("Set wrote with fence %+v at %v, want the current fence of acme at the clock time",
			store.fence, store.entry.At)
	}

	if err = w.Clear(t.Context(), ref, auditBy("user:ada")); !errors.Is(err, objectcontext.ErrNotFound) {
		t.Errorf("Clear: %v, want ErrNotFound", err)
	}

	missing := objectcontext.NamespaceRef("absent")
	_, err = w.Set(t.Context(), missing, patch, "user:ada", auditBy("user:ada"))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Errorf("Set in an unknown namespace: %v, want namespace.ErrNotFound", err)
	}
}
