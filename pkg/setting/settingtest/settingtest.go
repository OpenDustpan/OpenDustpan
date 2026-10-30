// Package settingtest is the conformance suite of the Store port of package
// setting. A new adapter runs Run with a Factory.
//
// Each case states one rule of the port.
package settingtest

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace called name and returns its current fence. Audit returns the
// audit entries of a scope, oldest first: audit.ServerScope for the
// installation.
type Harness struct {
	Store     setting.Store
	Namespace func(t *testing.T, name string) namespace.Fence
	Audit     func(t *testing.T, scope namespace.Name) []audit.Entry
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// CaseNames returns the name of each case, in order.
func CaseNames() []string {
	cases := cases()
	names := make([]string, 0, len(cases))

	for _, tc := range cases {
		names = append(names, tc.name)
	}

	return names
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.test(t, newHarness(t))
		})
	}
}

func cases() []suiteCase {
	return slices.Concat(readCases(), writeCases(), lockCases(), namespaceCases(), syncCases())
}

// The counts that the cases name.
const (
	revision2 int64 = 2
	entries2  int   = 2
	writers   int   = 8
)

// installation returns the scope of the installation.
func installation() setting.Scope { return setting.InstallationScope() }

// update runs Update on the installation with no fence.
func update(t *testing.T, h Harness, in setting.ChangeInput) (setting.Result, error) {
	t.Helper()

	return h.Store.Update(t.Context(), installation(), namespace.Fence{Namespace: "", FailoverVersion: 0}, in)
}

// mustUpdate runs update and fails the test on an error.
func mustUpdate(t *testing.T, h Harness, in setting.ChangeInput) setting.Result {
	t.Helper()

	res, err := update(t, h, in)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	return res
}

// item returns the item of key in res.
func item(t *testing.T, res setting.Resource, key string) setting.Item {
	t.Helper()

	i := slices.IndexFunc(res.Items, func(it setting.Item) bool { return it.Key == key })
	if i < 0 {
		t.Fatalf("the resource has no item %s", key)
	}

	return res.Items[i]
}

// loaded reads the stored state of scope.
func loaded(t *testing.T, h Harness, scope setting.Scope) setting.Stored {
	t.Helper()

	stored, err := h.Store.Load(t.Context(), scope)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	return stored
}

// unchanged fails the test unless the installation holds revision rev, the
// values want, and wantEntries audit entries.
func unchanged(t *testing.T, h Harness, rev int64, want map[string]string, wantEntries int) {
	t.Helper()

	stored := loaded(t, h, installation())
	if stored.Revision != rev || !mapsEqual(stored.Values, want) {
		t.Fatalf("stored state = revision %d, %v; want revision %d, %v", stored.Revision, stored.Values, rev, want)
	}

	if got := len(h.Audit(t, audit.ServerScope)); got != wantEntries {
		t.Fatalf("audit entries = %d, want %d", got, wantEntries)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}

	return true
}

func readCases() []suiteCase {
	return []suiteCase{
		{"a new store holds no value at revision 0", testNewStore},
		{"load of a namespace that does not exist returns not found", testLoadMissingNamespace},
	}
}

func testNewStore(t *testing.T, h Harness) {
	t.Helper()

	unchanged(t, h, 0, map[string]string{}, 0)
}

func testLoadMissingNamespace(t *testing.T, h Harness) {
	t.Helper()

	_, err := h.Store.Load(t.Context(), setting.NamespaceScope("missing"))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("load = %v, want ErrNotFound", err)
	}
}

func writeCases() []suiteCase {
	return []suiteCase{
		{"update stores the value, the next revision and one audit entry", testUpdateStores},
		{"update with a stale revision changes nothing", testStaleRevision},
		{"update that changes no value writes nothing", testNoOp},
		{"update with null deletes the value", testDelete},
		{"delete of a key with no value writes nothing", testDeleteAbsent},
		{"update with a refused edit changes none of the edits", testAllOrNone},
		{"update with a value that fails its kind or check changes nothing", testInvalidValue},
		{"update of a key that is not declared changes nothing", testUnknownKey},
		{"update of a static-only key answers static only", testStaticOnly},
		{"a change that reduces security needs a fresh authentication", testFresh},
		{"update with an entry of another scope changes nothing", testEntryScope},
		{"two updates with one revision: one wins", testConcurrent},
	}
}

func testUpdateStores(t *testing.T, h Harness) {
	t.Helper()

	res := mustUpdate(t, h, input(nil, installation(), 0, false, set(KeyLimit, "42"), set(KeyLabel, "blue")))

	if res.Resource.Revision != 1 || !slices.Equal(res.Changed, []string{KeyLabel, KeyLimit}) {
		t.Fatalf("result = revision %d, changed %v", res.Resource.Revision, res.Changed)
	}

	limit := item(t, res.Resource, KeyLimit)
	if limit.Value != "42" || limit.Source != setting.SourceDatabase || limit.Locked || limit.Default != "10" {
		t.Fatalf("item = %+v", limit)
	}

	unchanged(t, h, 1, map[string]string{KeyLimit: "42", KeyLabel: "blue"}, 1)

	checkStoredEntry(t, h.Audit(t, audit.ServerScope)[0])
}

// checkStoredEntry checks the audit entry of the change of testUpdateStores.
func checkStoredEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Action != actionOf(installation()) || e.Kind != audit.KindChange || e.Namespace != audit.ServerScope {
		t.Fatalf("entry = %+v", e)
	}

	var found bool

	for i := range e.Changes {
		c := e.Changes[i]
		if c.Key == KeyLimit {
			found = c.New == "42" && c.Old != nil && *c.Old == "10" && c.Layer == "database"
		}
	}

	if !found {
		t.Fatalf("entry changes = %+v, want the limit from 10 to 42 in the layer database", e.Changes)
	}
}

func testStaleRevision(t *testing.T, h Harness) {
	t.Helper()

	mustUpdate(t, h, input(nil, installation(), 0, false, set(KeyLimit, "42")))

	_, err := update(t, h, input(nil, installation(), 0, false, set(KeyLimit, "43")))
	if !errors.Is(err, setting.ErrPreconditionFailed) {
		t.Fatalf("update = %v, want ErrPreconditionFailed", err)
	}

	unchanged(t, h, 1, map[string]string{KeyLimit: "42"}, 1)
}

func testNoOp(t *testing.T, h Harness) {
	t.Helper()

	mustUpdate(t, h, input(nil, installation(), 0, false, set(KeyLimit, "42")))

	res := mustUpdate(t, h, input(nil, installation(), 1, false, set(KeyLimit, "42")))
	if res.Resource.Revision != 1 || len(res.Changed) != 0 {
		t.Fatalf("result = revision %d, changed %v; want revision 1, nothing", res.Resource.Revision, res.Changed)
	}

	unchanged(t, h, 1, map[string]string{KeyLimit: "42"}, 1)
}

func testDelete(t *testing.T, h Harness) {
	t.Helper()

	mustUpdate(t, h, input(nil, installation(), 0, false, set(KeyLimit, "42")))

	res := mustUpdate(t, h, input(nil, installation(), 1, false, del(KeyLimit)))

	limit := item(t, res.Resource, KeyLimit)
	if limit.Value != "10" || limit.Source != setting.SourceDefault || limit.HasDatabaseValue {
		t.Fatalf("item = %+v, want the default", limit)
	}

	unchanged(t, h, revision2, map[string]string{}, entries2)
}

func testDeleteAbsent(t *testing.T, h Harness) {
	t.Helper()

	res := mustUpdate(t, h, input(nil, installation(), 0, false, del(KeyLimit)))
	if len(res.Changed) != 0 {
		t.Fatalf("changed = %v", res.Changed)
	}

	unchanged(t, h, 0, map[string]string{}, 0)
}

func testAllOrNone(t *testing.T, h Harness) {
	t.Helper()

	_, err := update(t, h, input(nil, installation(), 0, false, set(KeyLimit, "42"), set(KeyFixed, "y")))
	if !errors.Is(err, setting.ErrStaticOnly) {
		t.Fatalf("update = %v, want ErrStaticOnly", err)
	}

	unchanged(t, h, 0, map[string]string{}, 0)
}

func testInvalidValue(t *testing.T, h Harness) {
	t.Helper()

	for _, bad := range []string{"x", "1.5", "-1", "99999", "01", " 1"} {
		_, err := update(t, h, input(nil, installation(), 0, false, set(KeyLimit, bad)))
		if !errors.Is(err, setting.ErrInvalidValue) {
			t.Fatalf("update limit %q = %v, want ErrInvalidValue", bad, err)
		}
	}

	for _, bad := range []string{"yes", "TRUE", ""} {
		_, err := update(t, h, input(nil, installation(), 0, false, set(KeyEnabled, bad)))
		if !errors.Is(err, setting.ErrInvalidValue) {
			t.Fatalf("update enabled %q = %v, want ErrInvalidValue", bad, err)
		}
	}

	_, err := update(t, h, input(nil, installation(), 0, false, set(KeyLabel, "a\nb")))
	if !errors.Is(err, setting.ErrInvalidValue) {
		t.Fatalf("update with a line break = %v, want ErrInvalidValue", err)
	}

	unchanged(t, h, 0, map[string]string{}, 0)
}

func testUnknownKey(t *testing.T, h Harness) {
	t.Helper()

	for _, key := range []string{"nope", KeyNSFlag} {
		_, err := update(t, h, input(nil, installation(), 0, false, set(key, "true")))
		if !errors.Is(err, setting.ErrUnknownKey) {
			t.Fatalf("update %s = %v, want ErrUnknownKey", key, err)
		}
	}

	unchanged(t, h, 0, map[string]string{}, 0)
}

func testStaticOnly(t *testing.T, h Harness) {
	t.Helper()

	for _, edit := range []setting.Edit{set(KeySecret, "hunter2"), del(KeySecret), set(KeyFixed, "y"), del(KeyFixed)} {
		_, err := update(t, h, input(nil, installation(), 0, false, edit))
		if !errors.Is(err, setting.ErrStaticOnly) {
			t.Fatalf("update %s = %v, want ErrStaticOnly", edit.Key, err)
		}
	}

	unchanged(t, h, 0, map[string]string{}, 0)
}

func testFresh(t *testing.T, h Harness) {
	t.Helper()

	mustUpdate(t, h, input(nil, installation(), 0, false, set(KeyEnabled, "true")))

	_, err := update(t, h, input(nil, installation(), 1, false, set(KeyEnabled, "false")))
	if !errors.Is(err, setting.ErrFreshStepRequired) {
		t.Fatalf("update = %v, want ErrFreshStepRequired", err)
	}

	unchanged(t, h, 1, map[string]string{KeyEnabled: "true"}, 1)

	// A change that does not reduce security needs no fresh step.
	mustUpdate(t, h, input(nil, installation(), 1, false, set(KeyLimit, "7")))

	res := mustUpdate(t, h, input(nil, installation(), revision2, true, set(KeyEnabled, "false")))
	if item(t, res.Resource, KeyEnabled).Value != "false" || res.Resource.Revision != 3 {
		t.Fatalf("resource = %+v", res.Resource)
	}
}

func testEntryScope(t *testing.T, h Harness) {
	t.Helper()

	in := input(nil, installation(), 0, false, set(KeyLimit, "42"))
	in.Entry.Namespace = "acme"

	_, err := update(t, h, in)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("update = %v, want ErrInvalidEntry", err)
	}

	unchanged(t, h, 0, map[string]string{}, 0)
}

func testConcurrent(t *testing.T, h Harness) {
	t.Helper()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)

	for i := range writers {
		wg.Go(func() {
			value := strconv.Itoa(i)

			_, err := update(t, h, input(nil, installation(), 0, false, set(KeyLabel, value)))

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				wins++
			case !errors.Is(err, setting.ErrPreconditionFailed):
				t.Errorf("update = %v, want success or ErrPreconditionFailed", err)
			default:
			}
		})
	}

	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d updates won, want 1", wins)
	}

	if got := len(h.Audit(t, audit.ServerScope)); got != 1 {
		t.Fatalf("audit entries = %d, want 1", got)
	}
}

func lockCases() []suiteCase {
	return []suiteCase{
		{"a key that the static layer sets is locked", testLocked},
		{"null on a locked key deletes the value that the static layer shadows", testShadowed},
		{"a secret shows whether it is set and never its value", testSecret},
	}
}

func testLocked(t *testing.T, h Harness) {
	t.Helper()

	static := StaticMap{KeyLimit: File("3")}

	_, err := update(t, h, input(static, installation(), 0, false, set(KeyLabel, "blue"), set(KeyLimit, "42")))
	if !errors.Is(err, setting.ErrLocked) {
		t.Fatalf("update = %v, want ErrLocked", err)
	}

	unchanged(t, h, 0, map[string]string{}, 0)
}

func testShadowed(t *testing.T, h Harness) {
	t.Helper()

	res := mustUpdate(t, h, input(nil, installation(), 0, false, set(KeyLimit, "42")))
	if item(t, res.Resource, KeyLimit).Shadowed {
		t.Fatal("an unlocked value is shadowed")
	}

	static := StaticMap{KeyLimit: File("3")}

	// Another instance now has a file that sets the key: the database value
	// is shadowed, and it shows so.
	stored := loaded(t, h, installation())
	shown := setting.Read(Registry(), installation(), static, stored)

	limit := item(t, shown, KeyLimit)
	if limit.Value != "3" || limit.Source != setting.SourceFile || !limit.Locked || !limit.Shadowed ||
		limit.DatabaseValue != "42" {
		t.Fatalf("item = %+v, want the file value over the shadowed database value 42", limit)
	}

	res = mustUpdate(t, h, input(static, installation(), 1, false, del(KeyLimit)))
	if limit := item(t, res.Resource, KeyLimit); limit.Shadowed || limit.HasDatabaseValue || limit.Value != "3" {
		t.Fatalf("item = %+v", limit)
	}

	unchanged(t, h, revision2, map[string]string{}, entries2)
}

func testSecret(t *testing.T, h Harness) {
	t.Helper()

	const secret = "hunter2-canary"

	static := StaticMap{KeySecret: {Source: setting.SourceEnv, Value: secret, By: "variable"}}
	res := mustUpdate(t, h, input(static, installation(), 0, false, set(KeyLabel, "blue")))

	it := item(t, res.Resource, KeySecret)
	if !it.Secret || !it.Set || it.Value != "" || it.Default != "" || it.DatabaseValue != "" || !it.Locked {
		t.Fatalf("item = %+v, want a set secret with no value", it)
	}

	checkNoSecretInAudit(t, h, secret)
	checkUnsetSecret(t, h)
}

// checkNoSecretInAudit checks that no audit entry of the installation holds
// the key or the text of the secret.
func checkNoSecretInAudit(t *testing.T, h Harness, secret string) {
	t.Helper()

	entries := h.Audit(t, audit.ServerScope)
	for i := range entries {
		for j := range entries[i].Changes {
			c := entries[i].Changes[j]
			if c.Key == KeySecret || c.New == secret || (c.Old != nil && *c.Old == secret) {
				t.Fatalf("the audit entry holds the secret: %+v", c)
			}
		}
	}
}

// checkUnsetSecret checks that a secret with no static value shows as not set.
func checkUnsetSecret(t *testing.T, h Harness) {
	t.Helper()

	shown := item(t, setting.Read(Registry(), installation(), nil, loaded(t, h, installation())), KeySecret)
	if shown.Set {
		t.Fatalf("a secret with no static value is set: %+v", shown)
	}
}

func namespaceCases() []suiteCase {
	return []suiteCase{
		{"a namespace keeps its own values and revision", testNamespaceOwn},
		{"a write with a stale fence changes nothing", testStaleFence},
		{"a write to a namespace that does not exist returns not found", testMissingNamespace},
		{"a write with the entry of another scope changes nothing", testNamespaceEntryScope},
		{"a reducing change in a namespace needs a fresh authentication", testNamespaceFresh},
	}
}

func nsUpdate(
	t *testing.T, h Harness, name string, fence namespace.Fence, in setting.ChangeInput,
) (setting.Result, error) {
	t.Helper()

	return h.Store.Update(t.Context(), setting.NamespaceScope(namespace.Name(name)), fence, in)
}

func testNamespaceOwn(t *testing.T, h Harness) {
	t.Helper()

	acme, other := h.Namespace(t, "acme"), h.Namespace(t, "other")
	scope := setting.NamespaceScope("acme")

	res, err := nsUpdate(t, h, "acme", acme, input(nil, scope, 0, false, set(KeyNSLimit, "9")))
	if err != nil || res.Resource.Revision != 1 {
		t.Fatalf("update = %+v, %v", res, err)
	}

	if got := loaded(t, h, setting.NamespaceScope("other")); got.Revision != 0 || len(got.Values) != 0 {
		t.Fatalf("other namespace = %+v", got)
	}

	if got := loaded(t, h, installation()); got.Revision != 0 || len(got.Values) != 0 {
		t.Fatalf("installation = %+v", got)
	}

	otherIn := input(nil, setting.NamespaceScope("other"), 0, false, set(KeyNSLimit, "8"))
	if _, err := nsUpdate(t, h, "other", other, otherIn); err != nil {
		t.Fatalf("update of the other namespace: %v", err)
	}

	entries := h.Audit(t, "acme")
	if len(entries) != 1 || entries[0].Namespace != "acme" ||
		entries[0].Action != actionOf(setting.NamespaceScope("acme")) {
		t.Fatalf("audit entries of acme = %+v", entries)
	}

	if len(h.Audit(t, audit.ServerScope)) != 0 {
		t.Fatal("a namespace change wrote an entry in the server scope")
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	scope := setting.NamespaceScope("acme")

	_, err := nsUpdate(t, h, "acme", staleFence(fence), input(nil, scope, 0, false, set(KeyNSLimit, "9")))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("update = %v, want ErrFenced", err)
	}

	_, err = nsUpdate(t, h, "acme", namespace.Fence{Namespace: "other", FailoverVersion: fence.FailoverVersion},
		input(nil, scope, 0, false, set(KeyNSLimit, "9")))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("update with the fence of another namespace = %v, want ErrFenced", err)
	}

	if got := loaded(t, h, scope); got.Revision != 0 || len(got.Values) != 0 || len(h.Audit(t, "acme")) != 0 {
		t.Fatalf("state = %+v", got)
	}
}

func testMissingNamespace(t *testing.T, h Harness) {
	t.Helper()

	scope := setting.NamespaceScope("missing")

	_, err := nsUpdate(t, h, "missing", namespace.Fence{Namespace: "missing", FailoverVersion: 1},
		input(nil, scope, 0, false, set(KeyNSLimit, "9")))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("update = %v, want ErrNotFound", err)
	}
}

func testNamespaceEntryScope(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	scope := setting.NamespaceScope("acme")

	in := input(nil, scope, 0, false, set(KeyNSLimit, "9"))
	in.Entry.Namespace = audit.ServerScope

	_, err := nsUpdate(t, h, "acme", fence, in)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("update = %v, want ErrInvalidEntry", err)
	}

	if got := loaded(t, h, scope); got.Revision != 0 || len(got.Values) != 0 {
		t.Fatalf("state = %+v", got)
	}
}

func testNamespaceFresh(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	scope := setting.NamespaceScope("acme")

	if _, err := nsUpdate(t, h, "acme", fence, input(nil, scope, 0, false, set(KeyNSFlag, "true"))); err != nil {
		t.Fatalf("update: %v", err)
	}

	_, err := nsUpdate(t, h, "acme", fence, input(nil, scope, 1, false, set(KeyNSFlag, "false")))
	if !errors.Is(err, setting.ErrFreshStepRequired) {
		t.Fatalf("update = %v, want ErrFreshStepRequired", err)
	}

	if _, err := nsUpdate(t, h, "acme", fence, input(nil, scope, 1, true, set(KeyNSFlag, "false"))); err != nil {
		t.Fatalf("update with a fresh step: %v", err)
	}
}

func syncCases() []suiteCase {
	return []suiteCase{
		{"the first sync stores the digest and keeps revision 0", testFirstSync},
		{"a sync with the same digest changes nothing", testSameSync},
		{"a sync with another digest changes the revision", testChangedSync},
	}
}

func syncOnce(t *testing.T, h Harness, digest string) (int64, bool) {
	t.Helper()

	rev, changed, err := h.Store.Sync(t.Context(), digest)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	return rev, changed
}

func testFirstSync(t *testing.T, h Harness) {
	t.Helper()

	if rev, changed := syncOnce(t, h, "d1"); rev != 0 || changed {
		t.Fatalf("sync = %d, %v; want 0, false", rev, changed)
	}

	if got := loaded(t, h, installation()); got.Digest != "d1" || got.Revision != 0 {
		t.Fatalf("stored = %+v", got)
	}
}

func testSameSync(t *testing.T, h Harness) {
	t.Helper()

	syncOnce(t, h, "d1")

	if rev, changed := syncOnce(t, h, "d1"); rev != 0 || changed {
		t.Fatalf("sync = %d, %v; want 0, false", rev, changed)
	}
}

func testChangedSync(t *testing.T, h Harness) {
	t.Helper()

	syncOnce(t, h, "d1")

	if rev, changed := syncOnce(t, h, "d2"); rev != 1 || !changed {
		t.Fatalf("sync = %d, %v; want 1, true", rev, changed)
	}

	_, err := update(t, h, input(nil, installation(), 0, false, set(KeyLimit, "42")))
	if !errors.Is(err, setting.ErrPreconditionFailed) {
		t.Fatalf("update with the old revision = %v, want ErrPreconditionFailed", err)
	}

	mustUpdate(t, h, input(nil, installation(), 1, false, set(KeyLimit, "42")))

	if got := loaded(t, h, installation()); got.Digest != "d2" || got.Revision != 2 {
		t.Fatalf("stored = %+v, want the digest kept and revision 2", got)
	}
}
