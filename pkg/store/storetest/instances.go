package storetest

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/installation"
)

// InstanceFactory returns a fresh, empty store for one subtest of
// RunInstances.
type InstanceFactory func(t *testing.T) installation.InstanceStore

// RunInstances executes the conformance suite of installation.InstanceStore
// against stores produced by newStore.
func RunInstances(t *testing.T, newStore InstanceFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, s installation.InstanceStore)
		name string
	}{
		{name: "register stores a row that list returns", test: testInstanceRegister},
		{name: "register again replaces the row", test: testInstanceRegisterReplaces},
		{name: "register refuses an invalid row", test: testInstanceRegisterInvalid},
		{name: "list orders by start time then ID", test: testInstanceListOrder},
		{name: "list on an empty store is empty", test: testInstanceListEmpty},
		{name: "unregister deletes only its own row", test: testInstanceUnregister},
		{name: "unregister of a missing row is not an error", test: testInstanceUnregisterMissing},
		{name: "unregister refuses an ID that is not allowed", test: testInstanceUnregisterInvalid},
		{name: "delete removes the row", test: testInstanceDelete},
		{name: "delete of a missing row is ErrInstanceNotFound", test: testInstanceDeleteMissing},
		{name: "delete refuses an ID that is not allowed", test: testInstanceDeleteInvalid},
		{name: "register at the same time keeps one row for each ID", test: testInstanceRegisterConcurrent},
		{name: "calls on a closed store are ErrUnavailable", test: testInstanceUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// instanceAt returns an instance whose start time is a whole number of
// milliseconds, the precision that the stores keep.
func instanceAt(id, version string, offset time.Duration) installation.Instance {
	return installation.Instance{
		StartedAt:     time.UnixMilli(1_700_000_000_000).Add(offset).UTC(),
		ID:            id,
		BinaryVersion: version,
	}
}

func listInstances(t *testing.T, s installation.InstanceStore) []installation.Instance {
	t.Helper()

	rows, err := s.ListInstances(t.Context())
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}

	return rows
}

func sameInstance(a, b installation.Instance) bool {
	return a.ID == b.ID && a.BinaryVersion == b.BinaryVersion && a.StartedAt.UnixMilli() == b.StartedAt.UnixMilli()
}

func instanceIDs(rows []installation.Instance) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}

	return ids
}

func requireInstanceIDs(t *testing.T, s installation.InstanceStore, want ...string) {
	t.Helper()

	got := instanceIDs(listInstances(t, s))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instance IDs = %v, want %v", got, want)
	}
}

func testInstanceRegister(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	want := instanceAt("i-1", "v0.2.0", 0)

	if err := s.RegisterInstance(t.Context(), want); err != nil {
		t.Fatalf("register: %v", err)
	}

	rows := listInstances(t, s)
	if len(rows) != 1 || !sameInstance(rows[0], want) {
		t.Fatalf("list = %+v, want [%+v]", rows, want)
	}

	if rows[0].StartedAt.Location() != time.UTC {
		t.Fatalf("started at location = %v, want UTC", rows[0].StartedAt.Location())
	}
}

func testInstanceRegisterReplaces(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	ctx := t.Context()
	second := instanceAt("i-1", "v0.3.0", time.Hour)

	for _, inst := range []installation.Instance{instanceAt("i-1", "v0.2.0", 0), second} {
		if err := s.RegisterInstance(ctx, inst); err != nil {
			t.Fatalf("register %+v: %v", inst, err)
		}
	}

	rows := listInstances(t, s)
	if len(rows) != 1 || !sameInstance(rows[0], second) {
		t.Fatalf("list = %+v, want [%+v]", rows, second)
	}
}

func testInstanceRegisterInvalid(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	long := strings.Repeat("a", installation.MaxInstanceFieldLength+1)

	tests := []struct {
		name string
		inst installation.Instance
	}{
		{name: "empty ID", inst: instanceAt("", "v0.2.0", 0)},
		{name: "empty version", inst: instanceAt("i-1", "", 0)},
		{name: "ID with a quote", inst: instanceAt("i-1'; DROP TABLE instance_versions; --", "v0.2.0", 0)},
		{name: "version with a newline", inst: instanceAt("i-1", "v0.2.0\nx", 0)},
		{name: "ID too long", inst: instanceAt(long, "v0.2.0", 0)},
	}

	for _, tt := range tests {
		err := s.RegisterInstance(t.Context(), tt.inst)
		if !errors.Is(err, installation.ErrInvalidInstance) {
			t.Fatalf("%s: register err = %v, want ErrInvalidInstance", tt.name, err)
		}
	}

	requireInstanceIDs(t, s)
}

func testInstanceListOrder(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	for _, inst := range []installation.Instance{
		instanceAt("c", "v0.2.0", 2*time.Second),
		instanceAt("b", "v0.2.0", time.Second),
		instanceAt("a", "v0.2.0", time.Second),
		instanceAt("z", "v0.2.0", 0),
	} {
		if err := s.RegisterInstance(t.Context(), inst); err != nil {
			t.Fatalf("register %s: %v", inst.ID, err)
		}
	}

	requireInstanceIDs(t, s, "z", "a", "b", "c")
}

func testInstanceListEmpty(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	if rows := listInstances(t, s); len(rows) != 0 {
		t.Fatalf("list on an empty store = %+v, want none", rows)
	}
}

func testInstanceUnregister(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	ctx := t.Context()

	for _, inst := range []installation.Instance{instanceAt("a", "v0.2.0", 0), instanceAt("b", "v0.2.0", time.Second)} {
		if err := s.RegisterInstance(ctx, inst); err != nil {
			t.Fatalf("register %s: %v", inst.ID, err)
		}
	}

	if err := s.UnregisterInstance(ctx, "a"); err != nil {
		t.Fatalf("unregister a: %v", err)
	}

	requireInstanceIDs(t, s, "b")
}

func testInstanceUnregisterMissing(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	if err := s.UnregisterInstance(t.Context(), "never-registered"); err != nil {
		t.Fatalf("unregister of a missing row: %v", err)
	}
}

func testInstanceUnregisterInvalid(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	for _, id := range []string{"", "a b", "a' OR '1'='1"} {
		if err := s.UnregisterInstance(t.Context(), id); !errors.Is(err, installation.ErrInvalidInstance) {
			t.Fatalf("unregister %q: err = %v, want ErrInvalidInstance", id, err)
		}
	}
}

func testInstanceDelete(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	ctx := t.Context()

	for _, inst := range []installation.Instance{instanceAt("a", "v0.2.0", 0), instanceAt("b", "v0.2.0", time.Second)} {
		if err := s.RegisterInstance(ctx, inst); err != nil {
			t.Fatalf("register %s: %v", inst.ID, err)
		}
	}

	if err := s.DeleteInstance(ctx, "a"); err != nil {
		t.Fatalf("delete a: %v", err)
	}

	requireInstanceIDs(t, s, "b")

	if err := s.DeleteInstance(ctx, "a"); !errors.Is(err, installation.ErrInstanceNotFound) {
		t.Fatalf("delete a again: err = %v, want ErrInstanceNotFound", err)
	}
}

func testInstanceDeleteMissing(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	if err := s.DeleteInstance(t.Context(), "never-registered"); !errors.Is(err, installation.ErrInstanceNotFound) {
		t.Fatalf("delete of a missing row: err = %v, want ErrInstanceNotFound", err)
	}
}

func testInstanceDeleteInvalid(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	ctx := t.Context()

	if err := s.RegisterInstance(ctx, instanceAt("a", "v0.2.0", 0)); err != nil {
		t.Fatalf("register a: %v", err)
	}

	for _, id := range []string{"", "a b", "a' OR '1'='1", "%", strings.Repeat("a", installation.MaxInstanceFieldLength+1)} {
		if err := s.DeleteInstance(ctx, id); !errors.Is(err, installation.ErrInvalidInstance) {
			t.Fatalf("delete %q: err = %v, want ErrInvalidInstance", id, err)
		}
	}

	requireInstanceIDs(t, s, "a")
}

func testInstanceRegisterConcurrent(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	const callers = 8

	errs := make([]error, callers)
	done := make(chan struct{}, callers)

	for i := range callers {
		go func() {
			defer func() { done <- struct{}{} }()

			id := "i-" + string(rune('a'+i%2))
			errs[i] = s.RegisterInstance(t.Context(), instanceAt(id, "v0.2.0", time.Duration(i)*time.Second))
		}()
	}

	for range callers {
		<-done
	}

	for i, err := range errs {
		if err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
	}

	// The start times differ by caller, so the list order depends on which
	// call ran last. The test compares the set of IDs.
	got := instanceIDs(listInstances(t, s))
	slices.Sort(got)

	if strings.Join(got, ",") != "i-a,i-b" {
		t.Fatalf("instance IDs = %v, want [i-a i-b]", got)
	}
}

func testInstanceUnavailable(t *testing.T, s installation.InstanceStore) {
	t.Helper()

	ctx := t.Context()
	closeStore(t, s)

	calls := []struct {
		call func() error
		name string
	}{
		{name: "register", call: func() error { return s.RegisterInstance(ctx, instanceAt("a", "v0.2.0", 0)) }},
		{name: "unregister", call: func() error { return s.UnregisterInstance(ctx, "a") }},
		{name: "list", call: func() error { _, err := s.ListInstances(ctx); return err }},
		{name: "delete", call: func() error { return s.DeleteInstance(ctx, "a") }},
	}

	for _, tc := range calls {
		err := tc.call()
		if !errors.Is(err, installation.ErrUnavailable) {
			t.Fatalf("%s on a closed store: err = %v, want ErrUnavailable", tc.name, err)
		}

		for _, text := range []string{"sql: database is closed", "closed pool", "SQLITE", "sqlite3", "pgx"} {
			if strings.Contains(err.Error(), text) {
				t.Fatalf("%s on a closed store: error %q holds driver text %q", tc.name, err, text)
			}
		}
	}
}
