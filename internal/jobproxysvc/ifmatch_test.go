package jobproxysvc_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/jobproxysvc"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
)

func TestProxyRuleSetStaleVersionRefused(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)

	first, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: unitScope(), Document: doc("a", "a.example.com"), IfMatch: "", Entry: entryOf(),
	})
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}

	// A second writer changes the set after the first writer read it.
	second, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: unitScope(), Document: doc("b", "b.example.com"), IfMatch: first.Digest, Entry: entryOf(),
	})
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}

	_, err = svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: unitScope(), Document: doc("c", "c.example.com"), IfMatch: first.Digest, Entry: entryOf(),
	})
	if !errors.Is(err, rulestore.ErrConflict) {
		t.Fatalf("Put with a stale digest: error = %v, want ErrConflict", err)
	}

	got, err := svc.Get(t.Context(), unitScope())
	if err != nil || got.Digest != second.Digest || got.Version != 2 ||
		strings.Contains(string(got.Document), "c.example.com") {
		t.Fatalf("Get = %+v, %v, want the set of the second writer", got, err)
	}

	if n := len(store.auditEntries()); n != 2 {
		t.Fatalf("got %d audit entries, want the 2 of the saves that worked", n)
	}
}

func TestProxyRuleSetIfMatchOfAMissingSetIsRefused(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore())

	_, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: nsScope(), Document: doc("a", "a.example.com"),
		IfMatch: rulestore.DocumentDigest([]byte("x")), Entry: entryOf(),
	})
	if !errors.Is(err, rulestore.ErrConflict) {
		t.Fatalf("Put with a digest for a scope with no set: error = %v, want ErrConflict", err)
	}
}

func TestProxyRuleSetCurrentDigestIsAccepted(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore())

	first, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: nsScope(), Document: doc("a", "a.example.com"), IfMatch: "", Entry: entryOf(),
	})
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}

	second, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: nsScope(), Document: doc("b", "b.example.com"), IfMatch: first.Digest, Entry: entryOf(),
	})
	if err != nil || second.Version != 2 {
		t.Fatalf("Put with the current digest = %+v, %v, want version 2", second, err)
	}
}
