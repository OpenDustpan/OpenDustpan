package gatetest

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// objectRunners returns the runners of the cases that hold for each kind of
// gate.Objects.
func objectRunners() map[string]runner {
	return map[string]runner{
		"put-new-has-version-1":            eachKind(caseNewVersion),
		"put-new-of-existing-id-conflicts": eachKind(caseNewExists),
		"put-replace-increments-version":   eachKind(caseReplace),
		"put-stale-version-conflicts":      eachKind(caseStale),
		"put-replace-unknown-not-found":    eachKind(caseReplaceUnknown),
		"same-id-in-two-scopes":            eachKind(caseSameIDTwoScopes),
		"unit-name-with-slash":             eachKind(caseUnitNameSlash),
		"get-other-scope-not-found":        eachKind(caseGetOtherScope),
		"list-own-scope-by-id":             eachKind(caseListOwnScope),
		"list-every-scope-by-id":           eachKind(caseListEveryScope),
		"delete-removes":                   eachKind(caseDeleteRemoves),
		"delete-stale-version-conflicts":   eachKind(caseDeleteStale),
		"delete-unknown-not-found":         eachKind(caseDeleteUnknown),
		"delete-other-scope-not-found":     eachKind(caseDeleteOtherScope),
		"installation-takes-zero-fence":    eachKind(caseInstallationFence),
		"stale-fence-changes-nothing":      eachKind(caseStaleFence),
		"fence-of-other-namespace-refused": eachKind(caseOtherFence),
		"entry-of-other-scope-refused":     eachKind(caseOtherEntry),
		"entry-in-same-transaction":        eachKind(caseEntryCount),
		"scope-without-routes-refused":     eachKind(caseNoRoutes),
		"scope-limit":                      eachKind(caseScopeLimit),
	}
}

func caseNewVersion(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put(new)", err)

	if v != 1 {
		t.Fatalf("Put(new) = version %d, want 1", v)
	}

	got, err := k.get(e, e.fx.acme, "a")
	mustNil(t, "Get()", err)

	if got.Version != 1 || got.Scope != e.fx.acme || got.ID != "a" {
		t.Fatalf("Get() = %s, want acme/a@1", describe(got))
	}
}

func caseNewExists(t *testing.T, e *env, k kindOps) {
	t.Helper()

	_, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put(new)", err)

	_, err = k.put(e, e.fx.acme, "a", "two", 0)
	wantErr(t, "Put(prev 0, exists)", err, gate.ErrConflict)

	got, err := k.get(e, e.fx.acme, "a")
	mustNil(t, "Get()", err)

	if got.Version != 1 {
		t.Fatalf("Get() after the refused put = %s, want version 1", describe(got))
	}

	e.wantEntries("acme", 1)
}

func caseReplace(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put(new)", err)

	next, err := k.put(e, e.fx.acme, "a", "two", v)
	mustNil(t, "Put(replace)", err)

	if next != v+1 {
		t.Fatalf("Put(replace) = version %d, want %d", next, v+1)
	}
}

func caseStale(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put(new)", err)

	_, err = k.put(e, e.fx.acme, "a", "two", v)
	mustNil(t, "Put(replace)", err)

	_, err = k.put(e, e.fx.acme, "a", "three", v)
	wantErr(t, "Put(stale prev)", err, gate.ErrConflict)
	e.wantEntries("acme", 2)
}

func caseReplaceUnknown(t *testing.T, e *env, k kindOps) {
	t.Helper()

	_, err := k.put(e, e.fx.acme, "a", "one", 1)
	wantErr(t, "Put(prev 1, unknown)", err, gate.ErrNotFound)
}

func caseSameIDTwoScopes(t *testing.T, e *env, k kindOps) {
	t.Helper()

	for _, scope := range []gate.Scope{e.fx.acmeNet, e.fx.globexNet} {
		_, err := k.put(e, scope, "a", scope.Namespace().String(), 0)
		mustNil(t, "Put("+scope.Key()+")", err)
	}

	for _, scope := range []gate.Scope{e.fx.acmeNet, e.fx.globexNet} {
		got, err := k.get(e, scope, "a")
		mustNil(t, "Get("+scope.Key()+")", err)

		if got.Scope != scope {
			t.Fatalf("Get(%s) = %s, want the object of that scope", scope, describe(got))
		}
	}
}

func caseUnitNameSlash(t *testing.T, e *env, k kindOps) {
	t.Helper()

	_, err := k.put(e, e.fx.webProd, "a", "one", 0)
	mustNil(t, "Put(web/prod)", err)

	got, err := k.get(e, e.fx.webProd, "a")
	mustNil(t, "Get(web/prod)", err)

	if got.Scope != e.fx.webProd {
		t.Fatalf("Get(web/prod) = %s, want scope %s", describe(got), e.fx.webProd)
	}

	other, err := k.list(e, e.fx.acmeNet)
	mustNil(t, "List(net)", err)

	if len(other) != 0 {
		t.Fatalf("List(net) = %d objects, want none", len(other))
	}
}

func caseGetOtherScope(t *testing.T, e *env, k kindOps) {
	t.Helper()

	_, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put(acme)", err)

	_, err = k.get(e, e.fx.globex, "a")
	wantErr(t, "Get(other scope)", err, gate.ErrNotFound)
}

func caseListOwnScope(t *testing.T, e *env, k kindOps) {
	t.Helper()

	scopes := []gate.Scope{e.fx.installation, e.fx.acme, e.fx.prod}
	ids := map[gate.Scope][]gate.ID{
		e.fx.installation: {"i2", "i1"},
		e.fx.acme:         {"a3", "a1", "a2"},
		e.fx.prod:         {"p1"},
	}

	for _, scope := range scopes {
		for _, id := range ids[scope] {
			_, err := k.put(e, scope, id, "one", 0)
			mustNil(t, "Put("+scope.Key()+"/"+string(id)+")", err)
		}
	}

	for _, scope := range scopes {
		got, err := k.list(e, scope)
		mustNil(t, "List("+scope.Key()+")", err)

		want := slices.Clone(ids[scope])
		slices.Sort(want)

		var have []gate.ID

		for _, ref := range got {
			if ref.Scope != scope {
				t.Fatalf("List(%s) holds %s", scope, describe(ref))
			}

			have = append(have, ref.ID)
		}

		if !slices.Equal(have, want) {
			t.Fatalf("List(%s) = %v, want %v", scope, have, want)
		}
	}
}

// caseListEveryScope lists the objects of one kind of the installation, of
// the installation scope and of two namespaces, and checks that the objects
// of another kind stay out of the list.
func caseListEveryScope(t *testing.T, e *env, k kindOps) {
	t.Helper()

	scopes := []gate.Scope{e.fx.installation, e.fx.acme, e.fx.globex}
	ids := map[gate.Scope][]gate.ID{
		e.fx.installation: {"i2", "i1"},
		e.fx.acme:         {"a3", "a1", "a2"},
		e.fx.globex:       {"g1"},
	}

	for _, scope := range scopes {
		for _, id := range ids[scope] {
			_, err := k.put(e, scope, id, "one", 0)
			mustNil(t, "Put("+scope.Key()+"/"+string(id)+")", err)
		}
	}

	for _, other := range allKinds(t, e.h.Store) {
		if other.kind() == k.kind() {
			continue
		}

		_, err := other.put(e, e.fx.acme, "other", "one", 0)
		mustNil(t, "Put(other kind)", err)

		break
	}

	got, err := k.listEveryScope(e)
	mustNil(t, "ListEveryScope()", err)

	have := make([]string, 0, len(got))

	for _, ref := range got {
		have = append(have, ref.Scope.Key()+"/"+string(ref.ID))
	}

	want := make([]string, 0, len(have))

	for _, scope := range scopes {
		for _, id := range ids[scope] {
			want = append(want, scope.Key()+"/"+string(id))
		}
	}

	slices.Sort(have)
	slices.Sort(want)

	if !slices.Equal(have, want) {
		t.Fatalf("ListEveryScope() = %v, want %v", have, want)
	}
}

func caseDeleteRemoves(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put()", err)

	mustNil(t, "Delete()", k.del(e, ref(e.fx.acme, v), e.acme, entry("acme")))

	_, err = k.get(e, e.fx.acme, "a")
	wantErr(t, "Get(after delete)", err, gate.ErrNotFound)

	e.wantEntries("acme", 2)
}

func caseDeleteStale(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put()", err)

	err = k.del(e, ref(e.fx.acme, v+1), e.acme, entry("acme"))
	wantErr(t, "Delete(stale version)", err, gate.ErrConflict)

	_, err = k.get(e, e.fx.acme, "a")
	mustNil(t, "Get(after the refused delete)", err)
}

func caseDeleteUnknown(t *testing.T, e *env, k kindOps) {
	t.Helper()

	err := k.del(e, ref(e.fx.acme, 1), e.acme, entry("acme"))
	wantErr(t, "Delete(unknown)", err, gate.ErrNotFound)
}

func caseDeleteOtherScope(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put(acme)", err)

	err = k.del(e, ref(e.fx.globex, v), e.globex, entry("globex"))
	wantErr(t, "Delete(other scope)", err, gate.ErrNotFound)

	_, err = k.get(e, e.fx.acme, "a")
	mustNil(t, "Get(after the refused delete)", err)
}

func caseInstallationFence(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.installation, "a", "one", 0)
	mustNil(t, "Put(installation)", err)

	if v != 1 {
		t.Fatalf("Put(installation) = version %d, want 1", v)
	}

	got, ok := e.entries(audit.ServerScope)
	if ok && len(got) != 1 {
		t.Fatalf("audit entries of the installation = %d, want 1", len(got))
	}

	_, err = k.putWith(
		e,
		&write{scope: e.fx.installation, id: "b", salt: "one", prev: 0, fence: e.acme, entry: serverEntry()},
	)
	wantErr(t, "Put(installation, fence of acme)", err, gate.ErrInvalid)

	_, err = k.putWith(
		e,
		&write{scope: e.fx.installation, id: "c", salt: "one", prev: 0, fence: zeroFence(), entry: entry("acme")},
	)
	wantErr(t, "Put(installation, entry of acme)", err, audit.ErrInvalidEntry)
}

func caseStaleFence(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.prod, "a", "one", 0)
	mustNil(t, "Put()", err)

	stale := e.acme

	_, err = e.h.Domain.BumpFence(t.Context(), e.acme)
	mustNil(t, "BumpFence()", err)

	_, err = k.putWith(e, &write{scope: e.fx.prod, id: "b", salt: "one", prev: 0, fence: stale, entry: entry("acme")})
	wantErr(t, "Put(stale fence)", err, namespace.ErrFenced)

	err = k.del(e, ref(e.fx.prod, v), stale, entry("acme"))
	wantErr(t, "Delete(stale fence)", err, namespace.ErrFenced)

	_, err = k.get(e, e.fx.prod, "b")
	wantErr(t, "Get(refused put)", err, gate.ErrNotFound)

	_, err = k.get(e, e.fx.prod, "a")
	mustNil(t, "Get(refused delete)", err)

	e.wantEntries("acme", 1)
}

func caseOtherFence(t *testing.T, e *env, k kindOps) {
	t.Helper()

	cases := []struct {
		scope gate.Scope
		fence namespace.Fence
	}{
		{e.fx.acme, e.globex}, {e.fx.acme, zeroFence()}, {e.fx.globex, e.acme}, {e.fx.prod, e.globex},
	}

	for _, c := range cases {
		_, err := k.putWith(
			e,
			&write{scope: c.scope, id: "a", salt: "one", prev: 0, fence: c.fence, entry: entry(c.scope.Namespace())},
		)
		wantErr(t, fmt.Sprintf("Put(%s, fence %+v)", c.scope, c.fence), err, gate.ErrInvalid)
	}

	_, err := k.get(e, e.fx.acme, "a")
	wantErr(t, "Get(refused put)", err, gate.ErrNotFound)
	e.wantEntries("acme", 0)
}

func caseOtherEntry(t *testing.T, e *env, k kindOps) {
	t.Helper()

	invalid := entry("acme")
	invalid.Subject = ""

	entries := []audit.Entry{entry("globex"), invalid, serverEntry()}
	for i := range entries {
		_, err := k.putWith(
			e,
			&write{scope: e.fx.acme, id: "a", salt: "one", prev: 0, fence: e.acme, entry: entries[i]},
		)
		wantErr(t, "Put(entry of "+entries[i].Namespace.String()+")", err, audit.ErrInvalidEntry)
	}

	_, err := k.get(e, e.fx.acme, "a")
	wantErr(t, "Get(refused put)", err, gate.ErrNotFound)
	e.wantEntries("acme", 0)
	e.wantEntries("globex", 0)
}

func caseEntryCount(t *testing.T, e *env, k kindOps) {
	t.Helper()

	v, err := k.put(e, e.fx.acme, "a", "one", 0)
	mustNil(t, "Put(new)", err)
	e.wantEntries("acme", 1)

	_, err = k.put(e, e.fx.acme, "a", "two", 0)
	wantErr(t, "Put(exists)", err, gate.ErrConflict)
	e.wantEntries("acme", 1)

	v, err = k.put(e, e.fx.acme, "a", "two", v)
	mustNil(t, "Put(replace)", err)
	e.wantEntries("acme", 2)

	mustNil(t, "Delete()", k.del(e, ref(e.fx.acme, v), e.acme, entry("acme")))
	e.wantEntries("acme", 3)

	if got, ok := e.entries("acme"); ok {
		for i := range got {
			if got[i].Action != "namespace:manage-gates" {
				t.Fatalf("entry %d = %q, want the action of the write", i, got[i].Action)
			}
		}
	}
}

func caseNoRoutes(t *testing.T, e *env, k kindOps) {
	t.Helper()

	for _, scope := range []gate.Scope{e.fx.blueprintWeb, e.fx.profileBase} {
		_, err := k.putWith(e, &write{scope: scope, id: "a", salt: "one", prev: 0, fence: e.acme, entry: entry("acme")})
		wantErr(t, "Put("+scope.Key()+")", err, gate.ErrInvalid)

		err = k.del(e, ref(scope, 1), e.acme, entry("acme"))
		wantErr(t, "Delete("+scope.Key()+")", err, gate.ErrInvalid)
	}
}

func caseScopeLimit(t *testing.T, e *env, k kindOps) {
	t.Helper()

	for i := range k.limit() {
		_, err := k.put(e, e.fx.acme, gate.ID(fmt.Sprintf("o%d", i)), "one", 0)
		mustNil(t, fmt.Sprintf("Put(o%d)", i), err)
	}

	_, err := k.put(e, e.fx.acme, "over", "one", 0)
	wantErr(t, "Put(above the limit)", err, gate.ErrLimit)

	_, err = k.put(e, e.fx.acme, "o0", "two", 1)
	mustNil(t, "Put(replace at the limit)", err)

	_, err = k.put(e, e.fx.globex, "over", "one", 0)
	mustNil(t, "Put(another scope at the limit of acme)", err)
}

// concurrently runs each fn in its own goroutine, after all have started, and
// returns their errors in the order of fns.
func concurrently(fns ...func() error) []error {
	errs := make([]error, len(fns))
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i, fn := range fns {
		wg.Go(func() {
			<-start

			errs[i] = fn()
		})
	}

	close(start)
	wg.Wait()

	return errs
}
