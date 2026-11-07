package scopeaudit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	nsA = namespace.ID("ns-a")
	nsB = namespace.ID("ns-b")
)

// errAppendFailed is the failure of the audit store in the fail-closed test.
var errAppendFailed = errors.New("append failed")

// itemFamily is a family of the tests.
func itemFamily() scopeaudit.Family {
	return scopeaudit.Family{Blobs: "item key blob", Dir: "item-keys"}
}

// fakeServerStore is an audit.ServerStore that keeps the entries.
type fakeServerStore struct {
	err     error
	entries []audit.Entry
}

func (f *fakeServerStore) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if f.err != nil {
		return audit.Entry{}, f.err
	}

	f.entries = append(f.entries, e)

	return e, nil
}

// checkSecurityEntry checks one entry of a refusal.
func checkSecurityEntry(t *testing.T, e *audit.Entry, at time.Time) {
	t.Helper()

	if err := e.Validate(); err != nil {
		t.Errorf("entry %q is not valid: %v", e.Reason, err)
	}

	if e.Kind != audit.KindSecurity || e.Outcome != audit.OutcomeDenied || e.Namespace != audit.ServerScope {
		t.Errorf("kind, outcome or scope of %+v", e)
	}

	if e.Action != scopeaudit.ActionOutOfScope || e.Object != string(nsA) || !e.At.Equal(at) {
		t.Errorf("action, object or time of %+v", e)
	}

	if strings.Contains(e.Reason, "namespaces/") || strings.Contains(e.Reason, string(nsB)) {
		t.Errorf("reason %q holds a key", e.Reason)
	}
}

func TestAuditWritesASecurityEntryForEachOperation(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC)
	store := &fakeServerStore{err: nil, entries: nil}

	rec, err := scopeaudit.New(store, itemFamily(), func() time.Time { return at })
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	for _, op := range []string{"put", "get", "delete", "list"} {
		if recErr := rec.RecordOutOfScope(t.Context(), nsA, op, scopeaudit.OutsideNamespace); recErr != nil {
			t.Fatalf("record %s: %v", op, recErr)
		}
	}

	if len(store.entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(store.entries))
	}

	for i := range store.entries {
		checkSecurityEntry(t, &store.entries[i], at)
	}
}

// TestAuditReasonAndChannel proves that the reason of the entry names where
// the key was, and that the channel is the one that the context of the call
// carries: console for a console request, api for an API request, for a call
// with none and for any channel that is not a request channel.
func TestAuditReasonAndChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     context.Context //nolint:containedctx // The test table holds the context of each case.
		where   scopeaudit.Where
		channel audit.Channel
		reason  string
	}{
		{
			"api request, outside the namespace", audit.WithChannel(t.Context(), audit.ChannelAPI),
			scopeaudit.OutsideNamespace, audit.ChannelAPI, "item key blob get refused: key outside the namespace",
		},
		{
			"console request, outside the directory", audit.WithChannel(t.Context(), audit.ChannelConsole),
			scopeaudit.OutsideDirectory, audit.ChannelConsole,
			"item key blob get refused: key outside the item-keys directory of the namespace",
		},
		{
			"no channel, outside the unit directory",
			t.Context(),
			scopeaudit.OutsideUnitDirectory,
			audit.ChannelAPI,
			"item key blob get refused: key outside the unit directory",
		},
		{
			"startup is not a request channel", audit.WithChannel(t.Context(), audit.ChannelStartup),
			scopeaudit.OutsideNamespace, audit.ChannelAPI, "item key blob get refused: key outside the namespace",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeServerStore{err: nil, entries: nil}

			rec, err := scopeaudit.New(store, itemFamily(), nil)
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			if recErr := rec.RecordOutOfScope(tc.ctx, nsA, "get", tc.where); recErr != nil {
				t.Fatalf("record %s: %v", tc.where, recErr)
			}

			if len(store.entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(store.entries))
			}

			if got := store.entries[0]; got.Reason != tc.reason || got.Channel != tc.channel {
				t.Errorf("reason %q on channel %q, want %q on %q", got.Reason, got.Channel, tc.reason, tc.channel)
			}
		})
	}
}

// TestNewRefusesAFamilyThatIsNotValid proves that the constructor fails closed:
// a family name with a line break, a capital, a path or a length over the bound
// is refused, because the name goes into the reason of an entry.
func TestNewRefusesAFamilyThatIsNotValid(t *testing.T) {
	t.Parallel()

	store := &fakeServerStore{err: nil, entries: nil}

	badFamilies := []scopeaudit.Family{
		{Blobs: "", Dir: "item-keys"},
		{Blobs: "item key blob", Dir: ""},
		{Blobs: "item key blob\nforged", Dir: "item-keys"},
		{Blobs: "item key blob", Dir: "item-keys/../x"},
		{Blobs: "Item key blob", Dir: "item-keys"},
		{Blobs: "-item", Dir: "item-keys"},
		{Blobs: strings.Repeat("a", 33), Dir: "item-keys"},
	}

	for _, family := range badFamilies {
		if _, err := scopeaudit.New(store, family, nil); !errors.Is(err, scopeaudit.ErrInvalidFamily) {
			t.Errorf("family %q: err = %v, want ErrInvalidFamily", family, err)
		}
	}

	if _, err := scopeaudit.New(nil, itemFamily(), nil); !errors.Is(err, scopeaudit.ErrMissingDependency) {
		t.Errorf("nil store err = %v, want ErrMissingDependency", err)
	}
}

// TestAuditRefusesHostileInput proves that the recorder writes nothing for an
// operation, a place or a namespace ID that the entry cannot hold, and returns
// the failure of the audit store.
func TestAuditRefusesHostileInput(t *testing.T) {
	t.Parallel()

	store := &fakeServerStore{err: nil, entries: nil}

	rec, err := scopeaudit.New(store, itemFamily(), nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	for _, where := range []scopeaudit.Where{"", "elsewhere", "namespace\nforged"} {
		recErr := rec.RecordOutOfScope(t.Context(), nsA, "get", where)
		if !errors.Is(recErr, scopeaudit.ErrInvalidRecord) {
			t.Errorf("place %q: err = %v, want ErrInvalidRecord", where, recErr)
		}
	}

	opErr := rec.RecordOutOfScope(t.Context(), nsA, "get\nforged entry", scopeaudit.OutsideNamespace)
	if !errors.Is(opErr, scopeaudit.ErrInvalidRecord) {
		t.Errorf("hostile op err = %v, want ErrInvalidRecord", opErr)
	}

	recErr := rec.RecordOutOfScope(t.Context(), "ns-A\nx", "get", scopeaudit.OutsideNamespace)
	if !errors.Is(recErr, namespace.ErrInvalidID) {
		t.Errorf("hostile namespace err = %v, want ErrInvalidID", recErr)
	}

	if len(store.entries) != 0 {
		t.Errorf("entries = %d, want 0", len(store.entries))
	}

	store.err = errAppendFailed

	recErr = rec.RecordOutOfScope(t.Context(), nsA, "get", scopeaudit.OutsideNamespace)
	if !errors.Is(recErr, errAppendFailed) {
		t.Errorf("append failure err = %v, want the store error", recErr)
	}
}

// TestPlaceOf proves the place of a refused key in a view: a valid key in the
// namespace of the view is outside the directory; every other key is outside
// the namespace.
func TestPlaceOf(t *testing.T) {
	t.Parallel()

	v, err := blob.NewScoped(nilStore{Store: nil}, nsA)
	if err != nil {
		t.Fatalf("view: %v", err)
	}

	tests := map[string]struct {
		key  blob.Key
		want scopeaudit.Where
	}{
		"own namespace, other directory": {"namespaces/ns-a/tofu/x/y", scopeaudit.OutsideDirectory},
		"another namespace":              {"namespaces/ns-b/tofu/x/y", scopeaudit.OutsideNamespace},
		"old layout":                     {"pulumi/x/y", scopeaudit.OutsideNamespace},
		"parent segment":                 {"namespaces/ns-a/../ns-b/x", scopeaudit.OutsideNamespace},
		"empty key":                      {"", scopeaudit.OutsideNamespace},
	}

	for name, tc := range tests {
		if got := scopeaudit.PlaceOf(v, tc.key); got != tc.want {
			t.Errorf("%s: place = %q, want %q", name, got, tc.want)
		}
	}
}

// nilStore is a blob store for a view that the test never calls through.
type nilStore struct{ blob.Store }

func TestAuditRecordMismatch(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
	store := &fakeServerStore{err: nil, entries: nil}

	rec, err := scopeaudit.New(store, itemFamily(), func() time.Time { return at })
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if recErr := rec.RecordMismatch(t.Context(), nsA, "get"); recErr != nil {
		t.Fatalf("record mismatch: %v", recErr)
	}

	if len(store.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(store.entries))
	}

	e := store.entries[0]
	if validErr := e.Validate(); validErr != nil {
		t.Errorf("entry is not valid: %v", validErr)
	}

	if e.Action != scopeaudit.ActionMismatch || e.Kind != audit.KindSecurity ||
		e.Outcome != audit.OutcomeDenied || e.Namespace != audit.ServerScope || e.Object != string(nsA) ||
		!e.At.Equal(at) || !strings.HasPrefix(e.Reason, "item key blob get refused") {
		t.Errorf("entry = %+v", e)
	}
}

func TestAuditRecordMismatchRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		storeErr error
		wantErr  error
		name     string
		ns       namespace.ID
		op       string
	}{
		{name: "a put", ns: nsA, op: "put", storeErr: nil, wantErr: scopeaudit.ErrInvalidRecord},
		{name: "an unknown operation", ns: nsA, op: "get\nx", storeErr: nil, wantErr: scopeaudit.ErrInvalidRecord},
		{name: "a bad namespace ID", ns: "../x", op: "get", storeErr: nil, wantErr: nil},
		{name: "a failed append", ns: nsA, op: "get", storeErr: errAppendFailed, wantErr: errAppendFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeServerStore{err: tc.storeErr, entries: nil}

			rec, err := scopeaudit.New(store, itemFamily(), nil)
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			err = rec.RecordMismatch(t.Context(), tc.ns, tc.op)
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("record mismatch err = %v, want %v", err, tc.wantErr)
			}

			if len(store.entries) != 0 {
				t.Errorf("entries = %d, want 0", len(store.entries))
			}
		})
	}
}
