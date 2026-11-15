package jobproxysvc_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/jobproxysvc"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func TestJoinNamespaceAndUnitProxyRuleSets(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)

	// The unit is saved first, so the order of the result is not the order of the saves.
	// The two sets use one id: the scope tells the entries apart.
	if _, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: unitScope(), Document: doc("api", "unit.example.com"), IfMatch: "", Entry: entryOf(),
	}); err != nil {
		t.Fatalf("Put unit: %v", err)
	}

	if _, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: nsScope(), Document: doc("api", "ns.example.com"), IfMatch: "", Entry: entryOf(),
	}); err != nil {
		t.Fatalf("Put namespace: %v", err)
	}

	got, err := svc.Joined(t.Context(), nsAcme, unitWeb)
	if err != nil {
		t.Fatalf("Joined: %v", err)
	}

	if len(got) != 2 || got[0].Scope != jobproxysvc.ScopeNamespace || got[0].Rule.Host != "ns.example.com" ||
		got[1].Scope != jobproxysvc.ScopeUnit || got[1].Rule.Host != "unit.example.com" {
		t.Fatalf("Joined = %+v, want the namespace entry and then the unit entry", got)
	}

	if got[0].Rule.ID != "api" || got[1].Rule.ID != "api" || got[0].Rule.Port != jobproxy.DefaultPort {
		t.Fatalf("Joined = %+v, want both ids api and the default port", got)
	}
}

func TestEmptyJoinedSetHasNoRules(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore())

	got, err := svc.Joined(t.Context(), nsAcme, unitWeb)
	if err != nil {
		t.Fatalf("Joined: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("Joined = %+v, want no rules", got)
	}

	set, err := svc.Get(t.Context(), nsScope())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if set.Digest != "" || set.Version != 0 || len(set.Document) != 0 {
		t.Fatalf("Get = %+v, want an empty set with no digest", set)
	}
}

func TestSaveWritesTheOldAndTheNewDigestToTheAuditEntry(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)

	first, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: nsScope(), Document: doc("a", "a.example.com"), IfMatch: "", Entry: entryOf(),
	})
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}

	second, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: nsScope(), Document: doc("b", "b.example.com"), IfMatch: first.Digest, Entry: entryOf(),
	})
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}

	if second.Digest == first.Digest || second.Digest != rulestore.DocumentDigest(doc("b", "b.example.com")) {
		t.Fatalf("digests %q and %q, want a new digest for the new document", first.Digest, second.Digest)
	}

	entries := store.auditEntries()
	if len(entries) != 2 {
		t.Fatalf("got %d audit entries, want 2", len(entries))
	}

	check := func(i int, wantOld *string, wantNew string) {
		t.Helper()

		changes := entries[i].Changes
		last := len(changes) - 1

		if last != 1 || changes[0].Key != "session" || changes[last].Key != "digest" || changes[last].New != wantNew {
			t.Fatalf("entry %d changes = %+v, want the change of the caller and one digest change to %q", i, changes, wantNew)
		}

		if (wantOld == nil) != (changes[last].Old == nil) || (wantOld != nil && *wantOld != *changes[last].Old) {
			t.Fatalf("entry %d old = %v, want %v", i, changes[last].Old, wantOld)
		}

		if entries[i].Namespace != nsAcme || entries[i].At.IsZero() {
			t.Fatalf("entry %d = %+v, want the namespace and the time of the service", i, entries[i])
		}
	}

	none := ""

	check(0, &none, first.Digest)
	check(1, &first.Digest, second.Digest)
}

func TestSequentialSavesWithoutIfMatchKeepTheLastWrite(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store)

	for _, host := range []string{"a.example.com", "b.example.com"} {
		if _, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
			Scope: unitScope(), Document: doc("a", host), IfMatch: "", Entry: entryOf(),
		}); err != nil {
			t.Fatalf("Put %s: %v", host, err)
		}
	}

	got, err := svc.Get(t.Context(), unitScope())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !strings.Contains(string(got.Document), "b.example.com") || got.Version != 2 {
		t.Fatalf("Get = %+v, want the second document at version 2", got)
	}

	if n := len(store.auditEntries()); n != 2 {
		t.Fatalf("got %d audit entries, want one for each save", n)
	}
}

func TestRenderedViewHoldsTheRulesOfOneOperation(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore())

	for _, tc := range []struct {
		scope rulestore.Scope
		doc   []byte
	}{
		{nsScope(), doc("plan-only", "plan.example.com", jobproxy.OperationPlan)},
		{unitScope(), doc("apply-too", "apply.example.com", jobproxy.OperationPlan, jobproxy.OperationApply)},
	} {
		if _, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
			Scope: tc.scope, Document: tc.doc, IfMatch: "", Entry: entryOf(),
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	plan, err := svc.Rendered(t.Context(), nsAcme, unitWeb, jobproxy.OperationPlan)
	if err != nil || len(plan) != 2 {
		t.Fatalf("Rendered(plan) = %+v, %v, want two rules", plan, err)
	}

	apply, err := svc.Rendered(t.Context(), nsAcme, unitWeb, jobproxy.OperationApply)
	if err != nil || len(apply) != 1 || apply[0].Rule.ID != "apply-too" || apply[0].Rule.Paths[0] != "/v1/**" {
		t.Fatalf("Rendered(apply) = %+v, %v, want the one rule for apply with its stored pattern", apply, err)
	}

	if _, err := svc.Rendered(t.Context(), nsAcme, unitWeb, "deploy"); !errors.Is(err, jobproxysvc.ErrOperation) {
		t.Fatalf("Rendered(deploy) error = %v, want ErrOperation", err)
	}
}

func TestUnknownScopesAreNotFound(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore())
	other := rulestore.Scope{Namespace: "other", UnitID: ""}
	missingUnit := rulestore.Scope{Namespace: nsAcme, UnitID: "u-none"}

	for _, scope := range []rulestore.Scope{other, missingUnit} {
		_, getErr := svc.Get(t.Context(), scope)
		_, putErr := svc.Put(t.Context(), jobproxysvc.PutRequest{
			Scope: scope, Document: doc("a", "a.example.com"), IfMatch: "", Entry: entryOf(),
		})

		for _, err := range []error{getErr, putErr} {
			if !errors.Is(err, namespace.ErrNotFound) && !errors.Is(err, inventory.ErrUnitNotFound) {
				t.Fatalf("scope %+v: error = %v, want not found", scope, err)
			}
		}
	}

	if _, err := svc.Joined(t.Context(), nsAcme, "u-none"); !errors.Is(err, inventory.ErrUnitNotFound) {
		t.Fatalf("Joined of a missing unit: error = %v, want ErrUnitNotFound", err)
	}
}

// TestPutRefusesHostileDocuments sends each hostile document straight to the
// service, as if the handler had not checked it. Each save fails and stores
// nothing.
func TestPutRefusesHostileDocuments(t *testing.T) {
	t.Parallel()

	manyPaths := make([]string, 0, 4)
	for i := range 4 {
		paths := make([]string, 0, 51)
		for j := range 51 {
			paths = append(paths, fmt.Sprintf(`"/p%d/%d"`, i, j))
		}

		manyPaths = append(manyPaths, fmt.Sprintf(
			`{"id":"r%d","operations":["plan"],"host":"h%d.example.com","mode":"inspect","methods":["GET"],"paths":[%s]}`,
			i, i, strings.Join(paths, ",")))
	}

	cases := map[string]string{
		"upper case host":   `{"rules":[{"id":"a","operations":["plan"],"host":"API.example.com","mode":"tunnel"}]}`,
		"ip host":           `{"rules":[{"id":"a","operations":["plan"],"host":"203.0.113.9","mode":"tunnel"}]}`,
		"wildcard host":     `{"rules":[{"id":"a","operations":["plan"],"host":"*.example.com","mode":"tunnel"}]}`,
		"trailing dot":      `{"rules":[{"id":"a","operations":["plan"],"host":"example.com.","mode":"tunnel"}]}`,
		"unknown operation": `{"rules":[{"id":"a","operations":["deploy"],"host":"a.example.com","mode":"tunnel"}]}`,
		"no operations":     `{"rules":[{"id":"a","operations":[],"host":"a.example.com","mode":"tunnel"}]}`,
		"bad port":          `{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","port":70000,"mode":"tunnel"}]}`,
		"unknown field":     `{"rules":[],"extra":1}`,
		"bad id":            `{"rules":[{"id":"a b","operations":["plan"],"host":"a.example.com","mode":"tunnel"}]}`,
		"duplicate id": `{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"tunnel"},` +
			`{"id":"a","operations":["plan"],"host":"b.example.com","mode":"tunnel"}]}`,
		"bad method": `{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect",` +
			`"methods":["CONNECT"],"paths":["/a"]}]}`,
		"dot dot path": `{"rules":[{"id":"a","operations":["plan"],"host":"a.example.com","mode":"inspect",` +
			`"methods":["GET"],"paths":["/a/../b"]}]}`,
		"over the pattern limit": `{"rules":[` + strings.Join(manyPaths, ",") + `]}`,
		"not json":               `not json`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			svc := newService(t, store)

			_, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
				Scope: nsScope(), Document: []byte(raw), IfMatch: "", Entry: entryOf(),
			})
			if !errors.Is(err, jobproxy.ErrInvalid) {
				t.Fatalf("Put error = %v, want ErrInvalid", err)
			}

			if _, getErr := store.Get(t.Context(), nsScope()); !errors.Is(getErr, rulestore.ErrNotFound) {
				t.Fatalf("the store holds a set after a refused save: %v", getErr)
			}
		})
	}
}

func TestPutFailsClosedOnAStoreError(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.failPut = errors.New("audit append failed")
	svc := newService(t, store)

	_, err := svc.Put(t.Context(), jobproxysvc.PutRequest{
		Scope: nsScope(), Document: doc("a", "a.example.com"), IfMatch: "", Entry: entryOf(),
	})
	if err == nil {
		t.Fatal("Put succeeded although the store failed")
	}

	got, getErr := svc.Get(t.Context(), nsScope())
	if getErr != nil || got.Version != 0 {
		t.Fatalf("Get = %+v, %v, want the old empty set", got, getErr)
	}
}
