package server_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// errGrantRead is the error of a failed read of the grants of a reach check.
var errGrantRead = errors.New("grant store is down")

// storedGroupWarning is the message of the warning of a failed protected
// check of a stored group.
const storedGroupWarning = "the protected check of a stored group failed"

// grantCauseType is the type that causeType reports for the error of a failed
// grant read. The reach wraps the plain error, and causeType names the type of
// a chain that holds no typed error.
const grantCauseType = "*fmt.wrapError"

// capturedLog is a slog.Handler that keeps the records of a test handler.
type capturedLog struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled implements slog.Handler.
func (h *capturedLog) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements slog.Handler.
func (h *capturedLog) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r.Clone())

	return nil
}

// WithAttrs implements slog.Handler.
func (h *capturedLog) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implements slog.Handler.
func (h *capturedLog) WithGroup(string) slog.Handler { return h }

// snapshot returns the records of the test handler.
func (h *capturedLog) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]slog.Record, len(h.records))
	copy(out, h.records)

	return out
}

// failingGrantReads fails each read of the grants of a reach check.
type failingGrantReads struct {
	access.GrantLookups

	err error
}

func (f failingGrantReads) GrantsOf(context.Context, access.Subject) ([]access.Grant, error) {
	return nil, f.err
}

func (f failingGrantReads) GrantsOfSubjects(context.Context, []access.Subject) ([]access.Grant, error) {
	return nil, f.err
}

// TestGroupProtectedFlag proves that the group list and the group get answer
// the protected flag of each group, and that a check that cannot read answers
// an error, never protected: false.
func TestGroupProtectedFlag(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)

	handler := newReachPrincipalHandler(t, svc, st, routeReach(st))

	var protected, open groupDoc

	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"admins"}`, http.StatusCreated,
	}, &protected)
	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"readers"}`, http.StatusCreated,
	}, &open)

	// The first group holds an operator grant on the installation.
	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-protected",
		Role: access.RoleInstallationOperator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectGroup, ID: protected.ID},
	}
	fence, e := storetest.GrantWrite(t, st, grt.Object, at, "grant:"+grt.ID)

	if err := st.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	want := map[string]bool{protected.ID: true, open.ID: false}

	var list groupListDoc

	mustCall(t, handler, apiCall{http.MethodGet, "/api/server/groups", "", http.StatusOK}, &list)

	if len(list.Groups) != len(want) {
		t.Fatalf("group list holds %d groups, want %d", len(list.Groups), len(want))
	}

	for i := range list.Groups {
		g := list.Groups[i]

		wantProtected, ok := want[g.ID]
		if !ok {
			t.Fatalf("group list holds the unknown group %q", g.ID)
		}

		if g.Protected != wantProtected {
			t.Errorf("group list: protected of %q = %t, want %t", g.ID, g.Protected, wantProtected)
		}
	}

	for id, wantProtected := range want {
		var got groupDoc

		mustCall(t, handler, apiCall{http.MethodGet, "/api/server/groups/" + id, "", http.StatusOK}, &got)

		if got.Protected != wantProtected {
			t.Errorf("protected of group %q = %t, want %t", id, got.Protected, wantProtected)
		}
	}

	// A check that cannot read the grants answers an error, never a false
	// flag.
	failing := newReachPrincipalHandler(t, svc, st, access.ReachConfig{
		Grants:      failingGrantReads{GrantLookups: st, err: errGrantRead},
		Roles:       st,
		Callers:     st,
		Principals:  st,
		Collections: nil,
		Units:       st,
	})

	var person principalDoc

	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","displayName":"Alice","kind":"person"}`, http.StatusCreated,
	}, &person)

	// The person joins the protected group, so that the groups of a
	// principal hold a group for the failing check to read.
	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/groups/" + protected.ID + "/members",
		`{"principalID":"` + person.ID + `"}`, http.StatusCreated,
	}, nil)

	// A read that cannot check the grants answers an error, never a false
	// flag. A stored write answers the stored group instead: the test below
	// covers it.
	failingCalls := []apiCall{
		{http.MethodGet, "/api/server/groups", "", 0},
		{http.MethodGet, "/api/server/groups/" + protected.ID, "", 0},
		{http.MethodGet, "/api/server/principals/" + person.ID + "/groups", "", 0},
	}

	for _, c := range failingCalls {
		status, body := call(t, failing, c)

		if status != http.StatusInternalServerError {
			t.Errorf("%s %s over a failed check answers %d, want 500 (%s)", c.method, c.path, status, body)
		}

		if strings.Contains(body, `"protected"`) {
			t.Errorf("%s %s over a failed check answers a protected flag: %s", c.method, c.path, body)
		}
	}
}

// storedGroupCase is one group write of the stored-group test.
type storedGroupCase struct {
	name          string
	method        string
	body          string
	wantName      string
	want          int
	wantProtected bool
	failing       bool
	seed          bool
}

// TestStoredGroupOnFailedProtectedCheck proves that a group create and each
// group change answer the stored group when the protected check fails: the
// status is the success status, the body says protected, and the audit log
// holds one success entry for the change. A working check answers the real
// flag.
func TestStoredGroupOnFailedProtectedCheck(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	cases := []storedGroupCase{
		{
			name: "create fails closed", method: http.MethodPost, body: `{"name":"admins"}`,
			want: http.StatusCreated, wantName: "admins", wantProtected: true, failing: true, seed: false,
		},
		{
			name: "rename fails closed", method: http.MethodPatch, body: `{"name":"renamed"}`,
			want: http.StatusOK, wantName: "renamed", wantProtected: true, failing: true, seed: true,
		},
		{
			name: "description fails closed", method: http.MethodPatch, body: `{"description":"changed"}`,
			want: http.StatusOK, wantName: "ops", wantProtected: true, failing: true, seed: true,
		},
		{
			name: "identity provider switch fails closed", method: http.MethodPatch,
			body: `{"closedToIdentityProvider":true}`,
			want: http.StatusOK, wantName: "ops", wantProtected: true, failing: true, seed: true,
		},
		{
			name: "create reads the flag", method: http.MethodPost, body: `{"name":"readers"}`,
			want: http.StatusCreated, wantName: "readers", wantProtected: false, failing: false, seed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runStoredGroupCase(t, tc, at)
		})
	}
}

// runStoredGroupCase runs one row of TestStoredGroupOnFailedProtectedCheck.
func runStoredGroupCase(t *testing.T, tc storedGroupCase, at time.Time) {
	t.Helper()

	svc, st := newTestPrincipalStore(t)

	groupID := "grp-stored"
	if tc.seed {
		seedStoredGroup(t, st, at, groupID)
	}

	reach := routeReach(st)
	if tc.failing {
		reach = failingGrantReach(st)
	}

	log := &capturedLog{mu: sync.Mutex{}, records: nil}

	handler := newReachPrincipalHandlerWithLogger(t, svc, st, reach, slog.New(log))

	path := "/api/server/groups"
	if tc.seed {
		path = "/api/server/groups/" + groupID
	}

	var group groupDoc

	mustCall(t, handler, apiCall{tc.method, path, tc.body, tc.want}, &group)

	if group.Protected != tc.wantProtected {
		t.Errorf("protected = %t, want %t", group.Protected, tc.wantProtected)
	}

	if group.Name != tc.wantName {
		t.Errorf("name = %q, want %q", group.Name, tc.wantName)
	}

	if tc.seed {
		if group.ID != groupID {
			t.Errorf("group ID = %q, want %q", group.ID, groupID)
		}
	} else {
		groupID = group.ID
	}

	if tc.failing {
		checkStoredGroupWarning(t, log, groupID)
	} else {
		checkNoStoredGroupWarning(t, log)
	}

	checkStoredGroupEntries(t, st, groupID)
}

// checkStoredGroupWarning proves that a failed protected check of a stored
// group writes one warning that names the group and the cause type, and that
// no record carries the text of the failed grant read.
func checkStoredGroupWarning(t *testing.T, log *capturedLog, groupID string) {
	t.Helper()

	records := log.snapshot()
	checkNoGrantReadText(t, records)

	warnings := storedGroupWarnings(records)
	if len(warnings) != 1 {
		t.Fatalf("warnings of a failed check = %d, want 1", len(warnings))
	}

	if got := attrOf(warnings[0], "group"); got != groupID {
		t.Errorf("group of the warning = %q, want %q", got, groupID)
	}

	if got := attrOf(warnings[0], "cause"); got != grantCauseType {
		t.Errorf("cause of the warning = %q, want %q", got, grantCauseType)
	}
}

// checkNoStoredGroupWarning proves that a working check writes no warning.
func checkNoStoredGroupWarning(t *testing.T, log *capturedLog) {
	t.Helper()

	records := log.snapshot()
	checkNoGrantReadText(t, records)

	if warnings := storedGroupWarnings(records); len(warnings) != 0 {
		t.Errorf("warnings of a working check = %d, want 0", len(warnings))
	}
}

// checkNoGrantReadText proves that no message and no attribute of records
// holds the text of the failed grant read.
func checkNoGrantReadText(t *testing.T, records []slog.Record) {
	t.Helper()

	for i := range records {
		rec := records[i]

		if strings.Contains(rec.Message, errGrantRead.Error()) {
			t.Errorf("log record %q holds the text of the failed grant read", rec.Message)
		}

		rec.Attrs(func(a slog.Attr) bool {
			if strings.Contains(a.Value.String(), errGrantRead.Error()) {
				t.Errorf("attribute %q of log record %q holds the text of the failed grant read", a.Key, rec.Message)
			}

			return true
		})
	}
}

// storedGroupWarnings returns the warnings of a failed protected check.
func storedGroupWarnings(records []slog.Record) []slog.Record {
	var warnings []slog.Record

	for i := range records {
		if records[i].Level == slog.LevelWarn && records[i].Message == storedGroupWarning {
			warnings = append(warnings, records[i])
		}
	}

	return warnings
}

// attrOf returns the value of the attribute key of rec, or the empty string.
func attrOf(rec slog.Record, key string) string {
	var value string

	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			value = a.Value.String()

			return false
		}

		return true
	})

	return value
}

// seedStoredGroup stores a plain group directly, so that a change row starts
// from a stored group without an entry of the handler.
func seedStoredGroup(t *testing.T, st *sqlite.Store, at time.Time, groupID string) {
	t.Helper()

	group := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: groupID, Name: "ops", Description: "",
		Source: principal.SourceLocal, MemberCount: 0, ClosedToIdentityProvider: false,
	}
	if err := st.CreateGroup(t.Context(), group, storetest.ServerChange(at, "group:"+groupID)); err != nil {
		t.Fatalf("seed group: %v", err)
	}
}

// failingGrantReach returns a reach whose grant read fails.
func failingGrantReach(st *sqlite.Store) access.ReachConfig {
	return access.ReachConfig{
		Grants:      failingGrantReads{GrantLookups: st, err: errGrantRead},
		Roles:       st,
		Callers:     st,
		Principals:  st,
		Collections: nil,
		Units:       st,
	}
}

// checkStoredGroupEntries proves that the audit log holds one success entry
// for the change of the group.
func checkStoredGroupEntries(t *testing.T, st *sqlite.Store, groupID string) {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	var changes []audit.Entry

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Object == "group:"+groupID && e.Action == string(identity.ActionServerManageGroups) {
			changes = append(changes, e)
		}
	}

	if len(changes) != 1 {
		t.Fatalf("entries for the change = %d, want one: %+v", len(changes), changes)
	}

	if changes[0].Kind != audit.KindChange || changes[0].Outcome != audit.OutcomeSuccess {
		t.Errorf("change entry = %+v, want a success change", changes[0])
	}
}
