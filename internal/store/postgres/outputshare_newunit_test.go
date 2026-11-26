package postgres_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// newUnitRuleAction is the action of the audit entry of the rule of a new unit.
const newUnitRuleAction = "unit:manage-sharing"

// newUnitStore returns a store over a fresh schema, or skips the test when no
// PostgreSQL URL is set.
func newUnitStore(t *testing.T) (*postgres.Store, string) {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL tests", envURL)
	}

	schemaURL := isolatedURL(t, base)

	return openURL(t, schemaURL), schemaURL
}

// newUnitSetupEntry returns a valid audit entry of a change in ns.
func newUnitSetupEntry(at time.Time, ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: at, Namespace: ns, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "test:setup", Object: ns.String(), ObjectID: "", Version: "",
		ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		Reason: "", PrincipalKind: "", JobID: "", Origin: nil,
	}
}

// newUnitRuleEntries returns the audit entries of the rules of new units in ns.
func newUnitRuleEntries(t *testing.T, s *postgres.Store, ns namespace.Name) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	all := allAuditEntries(t, s, ns)

	for i := range all {
		if all[i].Action == newUnitRuleAction {
			out = append(out, all[i])
		}
	}

	return out
}

// TestNewUnitRuleEntryNamesTheCauseOfThePlacement proves that the audit entry
// of the first rule of a placed unit keeps the server as its subject and copies
// the request ID and the channel of the placement entry. The reason names the
// creator and the namespace.
func TestNewUnitRuleEntryNamesTheCauseOfThePlacement(t *testing.T) {
	t.Parallel()

	s, schemaURL := newUnitStore(t)
	fence := ensureNamespace(t, s, "acme")
	plantSettingRow(t, schemaURL, "acme", "namespace")

	at := time.UnixMilli(1_700_000_000_000).UTC()
	col := collection.Collection{
		CreatedAt: at, ID: ident.NewID("col-"), Ref: collection.Ref{Namespace: "acme", Name: "payments"},
	}

	if err := s.CreateCollection(t.Context(), col, fence, newUnitSetupEntry(at, "acme")); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	placement := newUnitSetupEntry(at, "acme")
	placement.Subject = "alice"
	placement.RequestID = "req-7"
	placement.Channel = audit.ChannelConsole

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	st := stack.Stack{
		NamespaceID: "", CreatedAt: at, UpdatedAt: at, Tags: map[string]string{},
		Ref: ref, ID: stack.ID(ident.NewID("stk-")), Version: 0,
	}

	err = s.CreateStackPlaced(t.Context(), st, stack.Placement{
		Collection: col.Ref, CollectionID: col.ID, Entry: placement,
	}, fence)
	if err != nil {
		t.Fatalf("create stack: %v", err)
	}

	name, err := workspace.ParseName("ws")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	w := workspace.Workspace{
		NamespaceID: "", InCollection: false, CreatedAt: at, UpdatedAt: at,
		Lock: workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}, LastRelease: workspace.NoRelease(),
		ID: workspace.ID(ident.NewID("ws-")), Ref: workspace.Ref{Namespace: "acme", Name: name}, Revision: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		Origin:    identity.StateOrigin{Job: "", Commit: ""},
	}

	err = s.CreateWorkspacePlaced(t.Context(), w, workspace.Placement{
		Collection: col.Ref, CollectionID: col.ID, Entry: placement,
	}, fence)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	entries := newUnitRuleEntries(t, s, "acme")
	if len(entries) != 2 {
		t.Fatalf("rule entries = %d, want 2: one for the stack and one for the workspace", len(entries))
	}

	for i := range entries {
		wantPlacedRuleEntry(t, &entries[i])
	}
}

// wantPlacedRuleEntry reports each field of the rule entry e that is not the
// server, the cause of the placement of TestNewUnitRuleEntryNamesTheCauseOfThePlacement
// or its reason.
func wantPlacedRuleEntry(t *testing.T, e *audit.Entry) {
	t.Helper()

	if e.Subject != history.ServerSubject {
		t.Errorf("Subject = %q, want %q", e.Subject, history.ServerSubject)
	}

	if e.RequestID != "req-7" {
		t.Errorf("RequestID = %q, want the request ID of the placement", e.RequestID)
	}

	if e.Channel != audit.ChannelConsole {
		t.Errorf("Channel = %q, want the channel of the placement", e.Channel)
	}

	if !strings.Contains(e.Reason, "alice") || !strings.Contains(e.Reason, "acme") {
		t.Errorf("Reason = %q, want the creator alice and the namespace acme", e.Reason)
	}
}

// TestNewUnitRuleEntryIsCheckedAgainstTheFence proves the layer of the audit
// entry alone: a create whose unit is in another namespace than its fence is
// refused with audit.ErrInvalidEntry, and writes no unit and no rule.
func TestNewUnitRuleEntryIsCheckedAgainstTheFence(t *testing.T) {
	t.Parallel()

	s, schemaURL := newUnitStore(t)
	acme := ensureNamespace(t, s, "acme")
	ensureNamespace(t, s, "globex")
	plantSettingRow(t, schemaURL, "globex", "namespace")

	at := time.UnixMilli(1_700_000_000_000).UTC()

	ref, err := stack.ParseRef("globex", "web", "prod")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	st := stack.Stack{
		NamespaceID: "", CreatedAt: at, UpdatedAt: at, Tags: map[string]string{},
		Ref: ref, ID: stack.ID(ident.NewID("stk-")), Version: 0,
	}

	if err = s.CreateStack(t.Context(), st, acme); !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("CreateStack of a globex unit under the fence of acme = %v, want audit.ErrInvalidEntry", err)
	}

	if _, getErr := s.GetStack(t.Context(), ref); !errors.Is(getErr, stack.ErrNotFound) {
		t.Fatalf("GetStack after the refused create = %v, want stack.ErrNotFound", getErr)
	}

	if got := newUnitRuleEntries(t, s, "globex"); len(got) != 0 {
		t.Fatalf("rule entries in globex = %d, want 0", len(got))
	}
}

// TestNewUnitCreateFailsClosedWithoutNamespaceRow proves that a create in a
// namespace with no row writes no unit and returns an error.
func TestNewUnitCreateFailsClosedWithoutNamespaceRow(t *testing.T) {
	t.Parallel()

	s, _ := newUnitStore(t)

	at := time.UnixMilli(1_700_000_000_000).UTC()

	ref, err := stack.ParseRef("ghost", "web", "prod")
	if err != nil {
		t.Fatalf("parse stack reference: %v", err)
	}

	st := stack.Stack{
		NamespaceID: "", CreatedAt: at, UpdatedAt: at, Tags: map[string]string{},
		Ref: ref, ID: stack.ID(ident.NewID("stk-")), Version: 0,
	}

	if err = s.CreateStack(t.Context(), st, namespace.Fence{Namespace: "ghost", FailoverVersion: 1}); err == nil {
		t.Fatal("CreateStack in a namespace with no row answered no error")
	}

	if _, getErr := s.GetStack(t.Context(), ref); !errors.Is(getErr, stack.ErrNotFound) {
		t.Fatalf("GetStack after the refused create = %v, want stack.ErrNotFound", getErr)
	}
}
