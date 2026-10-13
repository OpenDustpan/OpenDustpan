package principal_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
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
		{"Ünïcode Name", nil},
		{"admin-team", nil},
		{"admin-", nil},
		{"administrator", nil},
		{strings.Repeat("a", 255), nil},
		{"", principal.ErrInvalid},
		{strings.Repeat("a", 256), principal.ErrInvalid},
		{" alice", principal.ErrInvalid},
		{"alice\n", principal.ErrInvalid},
		{"al\tice", principal.ErrInvalid},
		{"\xff", principal.ErrInvalid},
		{"admin", principal.ErrReservedSubject},
		{"admin-2", principal.ErrReservedSubject},
		{"admin-17", principal.ErrReservedSubject},
		{"Admin", principal.ErrReservedSubject},
		{"ADMIN", principal.ErrReservedSubject},
		{"ADMIN-3", principal.ErrReservedSubject},
		{"\u0430dmin", principal.ErrReservedSubject},                     // Cyrillic a
		{"adm\u0456n", principal.ErrReservedSubject},                     // Cyrillic i
		{"\uff41\uff44\uff4d\uff49\uff4e", principal.ErrReservedSubject}, // full width
		{"\uff21\uff24\uff2d\uff29\uff2e\uff0d\uff13", principal.ErrReservedSubject},
		{"admin-\u0663", principal.ErrReservedSubject},      // Arabic-Indic three
		{"admin\u2010\u0663", principal.ErrReservedSubject}, // hyphen and Arabic-Indic three
		{"ad\u200bmin", principal.ErrInvalid},               // zero-width space
		{"ad\u00admin", principal.ErrInvalid},               // soft hyphen
		{"admin\u2060", principal.ErrInvalid},               // word joiner
		{"\ufeffadmin", principal.ErrInvalid},               // byte order mark
		{"\u738b\u5c0f\u660e\u5148\u751f", nil},             // five CJK letters
		{"Admins", nil},
		{"admin-x", nil},
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

	_, err := principal.NewService(principal.Config{Store: nil, Now: nil, NewID: nil})
	if !errors.Is(err, principal.ErrMissingDependency) {
		t.Fatalf("NewService without a store = %v, want ErrMissingDependency", err)
	}
}

func TestPaging(t *testing.T) {
	t.Parallel()

	svc := newServiceOverStore(t)
	e := testEntry()

	for _, subject := range []string{"a1", "a2", "a3", "a\U0010FFFF", "b1"} {
		in := principal.Input{Subject: subject, DisplayName: "", Kind: principal.KindPerson}
		if _, err := svc.CreatePrincipal(t.Context(), e, in); err != nil {
			t.Fatalf("create %q: %v", subject, err)
		}
	}

	var got []string

	req := principal.PageRequest{Prefix: "a", Cursor: "", Limit: 2}

	for range 5 {
		page, err := svc.ListPrincipals(t.Context(), req)
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

	if want := []string{"a1", "a2", "a3", "a\U0010FFFF"}; !slices.Equal(got, want) {
		t.Fatalf("pages = %q, want %q", got, want)
	}

	for _, bad := range []principal.PageRequest{
		{Prefix: "", Cursor: "", Limit: -1},
		{Prefix: "", Cursor: "", Limit: principal.MaxLimit + 1},
		{Prefix: "", Cursor: "not base64!", Limit: 0},
		{Prefix: "\xff", Cursor: "", Limit: 0},
	} {
		if _, err := svc.ListPrincipals(t.Context(), bad); !errors.Is(err, principal.ErrInvalid) {
			t.Errorf("list %+v = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestServiceEntries(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	at := time.Date(2026, time.October, 13, 23, 0, 0, 0, time.UTC)

	svc, err := principal.NewService(principal.Config{
		Store: st, Now: func() time.Time { return at }, NewID: func(prefix string) string { return prefix + "fixed" },
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

	svc, err := principal.NewService(principal.Config{Store: openStore(t), Now: nil, NewID: nil})
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
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "", Outcome: "",
		RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
	}
}
