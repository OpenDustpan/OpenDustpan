package principal_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

func TestValidateSubject(t *testing.T) {
	t.Parallel()

	cases := []struct {
		subject string
		want    error
	}{
		{"alice", nil},
		{"alice@example.com", nil},
		{"a.b+tag@example.com", nil},
		{"spiffe://example.org/ns/ci/sa/deploy", nil},
		{"ci-deploy_2", nil},
		{"administrator", nil},
		{"Admins", nil},
		{strings.Repeat("a", 255), nil},
		{"", principal.ErrInvalid},
		{strings.Repeat("a", 256), principal.ErrInvalid},
		{" alice", principal.ErrInvalid},
		{"alice\n", principal.ErrInvalid},
		{"al ice", principal.ErrInvalid},
		{"-alice", principal.ErrInvalid},
		{".alice", principal.ErrInvalid},
		{"\xff", principal.ErrInvalid},
		{"Ünïcode", principal.ErrInvalid},
		{"\u738b\u5c0f\u660e", principal.ErrInvalid},
		{"dev", nil},
		{"developer", nil},
		{"dev-admin", nil},
		{"dev.admin", nil},
		{"dev:admin", principal.ErrReservedSubject},
		{"dev:", principal.ErrReservedSubject},
		{"dev:someone", principal.ErrReservedSubject},
		{"Dev:admin", principal.ErrReservedSubject},
		{"DEV:ADMIN", principal.ErrReservedSubject},
		{"admin", principal.ErrReservedSubject},
		{"Admin", principal.ErrReservedSubject},
		{"ADMIN", principal.ErrReservedSubject},
		{"admin-2", principal.ErrReservedSubject},
		{"admin-17", principal.ErrReservedSubject},
		{"admin-", principal.ErrReservedSubject},
		{"admin-team", principal.ErrReservedSubject},
		{"admin1", principal.ErrReservedSubject},
		{"admin.", principal.ErrReservedSubject},
		{"admin_1", principal.ErrReservedSubject},
		{"admin@example.com", principal.ErrReservedSubject},
		// Look-alikes are not ASCII, so the pattern refuses them.
		{"\u0430dmin", principal.ErrInvalid},                                         // Cyrillic a
		{"\uff41\uff44\uff4d\uff49\uff4e", principal.ErrInvalid},                     // full width
		{"admin\u3164", principal.ErrInvalid},                                        // Hangul filler
		{"admin\u115f", principal.ErrInvalid},                                        // Hangul choseong filler
		{"admin\u2800", principal.ErrInvalid},                                        // Braille blank
		{"a\u0301dmin", principal.ErrInvalid},                                        // combining acute accent
		{"admin\u034f", principal.ErrInvalid},                                        // combining grapheme joiner
		{"\u0430\u0501\u217f\u0456\u0578", principal.ErrInvalid},                     // all look-alikes
		{"\U0001d68a\U0001d68d\U0001d696\U0001d692\U0001d697", principal.ErrInvalid}, // mathematical monospace
		{"\u1d00\u1d05\u1d0d\u026a\u0274", principal.ErrInvalid},                     // small capitals
		{"ad\u200bmin", principal.ErrInvalid},                                        // zero-width space
		{"admin-\u0663", principal.ErrInvalid},                                       // Arabic-Indic three
	}

	for _, tc := range cases {
		t.Run(tc.subject, func(t *testing.T) {
			t.Parallel()

			err := principal.ValidateSubject(tc.subject)
			if tc.want == nil && err != nil {
				t.Fatalf("ValidateSubject(%q) = %v, want nil", tc.subject, err)
			}

			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("ValidateSubject(%q) = %v, want %v", tc.subject, err, tc.want)
			}
		})
	}
}

func TestParseKind(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"person", "service", "workload"} {
		if k, err := principal.ParseKind(s); err != nil || string(k) != s {
			t.Fatalf("ParseKind(%q) = %q, %v", s, k, err)
		}
	}

	for _, s := range []string{"", "Person", "robot", "group"} {
		if _, err := principal.ParseKind(s); !errors.Is(err, principal.ErrInvalid) {
			t.Fatalf("ParseKind(%q) = %v, want ErrInvalid", s, err)
		}
	}
}

func TestValidateGroupName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ops", "Platform_Team", "team-1.0"} {
		if err := principal.ValidateGroupName(name); err != nil {
			t.Fatalf("ValidateGroupName(%q) = %v, want nil", name, err)
		}
	}

	for _, name := range []string{"", "a b", "-ops", ".ops", "ops/dev", strings.Repeat("a", 101)} {
		if err := principal.ValidateGroupName(name); !errors.Is(err, principal.ErrInvalid) {
			t.Fatalf("ValidateGroupName(%q) = %v, want ErrInvalid", name, err)
		}
	}
}

func TestNewServiceNeedsStore(t *testing.T) {
	t.Parallel()

	_, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      nil, Guard: principal.AdminOnlyGuard{}, Namespaces: nil, Tokens: nil, GroupReach: nil,
		Now: nil, NewID: nil,
	})
	if !errors.Is(err, principal.ErrMissingDependency) {
		t.Fatalf("NewService without a store = %v, want ErrMissingDependency", err)
	}
}

func TestDisplayNameRefusesFormatCharacters(t *testing.T) {
	t.Parallel()

	p := principal.Principal{
		CreatedAt:   time.Time{},
		UpdatedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          "prn-1",
		Subject:     "alice",
		DisplayName: "Ali\u200bce",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0, BreakGlass: false,
	}
	if err := p.Validate(); !errors.Is(err, principal.ErrInvalid) {
		t.Fatalf("display name with a zero-width space = %v, want ErrInvalid", err)
	}

	p.DisplayName = "Ünïcode Name 王"
	if err := p.Validate(); err != nil {
		t.Fatalf("display name in Unicode = %v, want nil", err)
	}
}

func TestPaging(t *testing.T) {
	t.Parallel()

	svc := newServiceOverStore(t)
	e := testEntry()

	for _, subject := range []string{"a1", "a2", "a3", "az", "b1"} {
		in := principal.Input{Subject: subject, DisplayName: "", Kind: principal.KindPerson}
		if _, err := svc.CreatePrincipal(t.Context(), e, in); err != nil {
			t.Fatalf("create %q: %v", subject, err)
		}
	}

	var got []string

	req := principal.PageRequest{Prefix: "a", Cursor: "", Limit: 2}

	for range 5 {
		page, err := svc.ListPrincipals(t.Context(), req, "")
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		for i := range page.Items {
			got = append(got, page.Items[i].Subject)
		}

		if page.NextCursor == "" {
			break
		}

		req.Cursor = page.NextCursor
	}

	if want := []string{"a1", "a2", "a3", "az"}; !slices.Equal(got, want) {
		t.Fatalf("pages = %q, want %q", got, want)
	}

	for _, bad := range []principal.PageRequest{
		{Prefix: "", Cursor: "", Limit: -1},
		{Prefix: "", Cursor: "", Limit: principal.MaxLimit + 1},
		{Prefix: "", Cursor: "not base64!", Limit: 0},
		{Prefix: "\xff", Cursor: "", Limit: 0},
		{Prefix: "a\x00", Cursor: "", Limit: 0},
		{Prefix: "", Cursor: "YQA", Limit: 0}, // "a" and a NUL
		{Prefix: "", Cursor: strings.Repeat("YW", principal.MaxCursorLength), Limit: 0},
	} {
		if _, err := svc.ListPrincipals(t.Context(), bad, ""); !errors.Is(err, principal.ErrInvalid) {
			t.Errorf("list %+v = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestListByKind(t *testing.T) {
	t.Parallel()

	svc := newServiceOverStore(t)

	everything := principal.PageRequest{Prefix: "", Cursor: "", Limit: 0}
	if _, err := svc.ListPrincipals(t.Context(), everything, "robot"); !errors.Is(err, principal.ErrInvalid) {
		t.Errorf("list of the kind robot = %v, want ErrInvalid", err)
	}

	page, err := svc.ListPrincipals(t.Context(), everything, principal.KindWorkload)
	if err != nil || len(page.Items) != 0 || page.Total != 0 {
		t.Errorf("list of the workloads = %+v, %v; want none", page, err)
	}
}

func TestServiceEntries(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	at := time.Date(2026, time.October, 13, 23, 0, 0, 0, time.UTC)

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      principal.AdminOnlyGuard{}, Namespaces: nil, Tokens: nil, GroupReach: nil,
		Now:   func() time.Time { return at },
		NewID: func(prefix string) string { return prefix + "fixed" },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	in := principal.Input{Subject: "alice", DisplayName: "", Kind: principal.KindPerson}
	if _, err = svc.CreatePrincipal(t.Context(), testEntry(), in); err != nil {
		t.Fatalf("create: %v", err)
	}

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil || len(listing.Entries) != 1 {
		t.Fatalf("entries = %+v, %v; want one", listing.Entries, err)
	}

	got := listing.Entries[0]
	if got.Object != "principal:prn-fixed" || !got.At.Equal(at) || got.Kind != audit.KindChange ||
		got.Outcome != audit.OutcomeSuccess {
		t.Fatalf("entry = %+v, want the change of prn-fixed at the Service time", got)
	}
}

// TestUpdateGroupSwitchEntries proves that each change of the switch that
// closes a group to the identity provider writes the new permission of the
// identity provider in the reason of the entry, and that a change of the
// same value writes no reason.
func TestUpdateGroupSwitchEntries(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	at := time.Date(2026, time.October, 13, 23, 0, 0, 0, time.UTC)

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      principal.AdminOnlyGuard{}, Namespaces: nil, Tokens: nil, GroupReach: nil,
		Now:   func() time.Time { return at },
		NewID: func(prefix string) string { return prefix + "fixed" },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	entry := testEntry()
	entry.Action = "server:manage-groups"

	g, err := svc.CreateGroup(t.Context(), entry, principal.GroupInput{Name: "ops", Description: ""})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}

	closed, open := true, false

	steps := []switchStep{
		{name: "close the group", value: &closed, reason: "the identity provider may add members: off"},
		{name: "open the group", value: &open, reason: "the identity provider may add members: on"},
		{name: "set the same value again", value: &open, reason: ""},
	}

	fx := switchFixture{svc: svc, st: st, entry: entry, id: g.ID}
	for _, step := range steps {
		fx.check(t, step)
	}
}

// switchFixture holds the service, its store, the entry of the caller and
// the ID of the group whose switch a step changes.
type switchFixture struct {
	svc   *principal.Service
	st    *sqlite.Store
	entry audit.Entry
	id    string
}

// switchStep is one change of the switch of a group and the reason that its
// audit entry must carry.
type switchStep struct {
	name   string
	value  *bool
	reason string
}

// check sets the switch of the group to the value of step with one change
// and proves that the group reads the value and that the newest entry of the
// scope @server carries the reason of step.
func (fx switchFixture) check(t *testing.T, step switchStep) {
	t.Helper()

	caller := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "tester", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		Kind: "", Credential: "", JobID: "",
	}

	changed, err := fx.svc.UpdateGroup(t.Context(), fx.entry, caller, fx.id, principal.GroupUpdate{
		Name: nil, Description: nil, ClosedToIdentityProvider: step.value,
	})
	if err != nil {
		t.Fatalf("%s: %v", step.name, err)
	}

	if changed.ClosedToIdentityProvider != *step.value {
		t.Fatalf("%s: the group reads %v, want %v", step.name, changed.ClosedToIdentityProvider, *step.value)
	}

	listing, err := fx.st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil || len(listing.Entries) == 0 {
		t.Fatalf("%s: entries = %+v, %v; want at least one", step.name, listing.Entries, err)
	}

	got := listing.Entries[0]
	if got.Action != "server:manage-groups" || got.Object != principal.GroupRef(fx.id) ||
		got.Kind != audit.KindChange || got.Outcome != audit.OutcomeSuccess || got.Reason != step.reason {
		t.Fatalf("%s: entry = %+v, want a change of %s with the reason %q", step.name, got, fx.id, step.reason)
	}
}

func TestRefs(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		principal.Ref("prn-abc"):                      "principal:prn-abc",
		principal.Ref("../etc/passwd"):                "principal:invalid",
		principal.Ref("grp-abc"):                      "principal:invalid",
		principal.GroupRef(strings.Repeat("g", 70)):   "group:invalid",
		principal.MemberRef("grp-a", "prn-b"):         "group:grp-a#member:prn-b",
		principal.MemberRef("grp-a", "prn-b\nforged"): "group:grp-a#member:invalid",
	}

	for got, want := range cases {
		if got != want {
			t.Errorf("ref = %q, want %q", got, want)
		}
	}
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

func newServiceOverStore(t *testing.T) *principal.Service {
	t.Helper()

	svc, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      openStore(t),
			Guard:      principal.AdminOnlyGuard{},
			Namespaces: nil,
			Tokens:     nil,
			GroupReach: nil,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// testEntry is the entry of a caller, as the HTTP adapter builds it.
func testEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "server:manage-principals", Object: "", Version: "", ValuePath: "", ValueCount: nil,
		Changes:  nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "", Outcome: "",
		RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
		ObjectID:      "",
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}
