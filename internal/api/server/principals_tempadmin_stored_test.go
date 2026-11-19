package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

const storedPrincipalWarning = "the temporary administrator read failed after a principal write"

// tempAdminReadError is the error of a failed read of the temporary
// administrator records. Its type is the cause that the log must hold.
type tempAdminReadError struct{}

func (tempAdminReadError) Error() string { return "temp admin store: disk I/O error" }

// markedPrincipalDoc is a principal on the wire with the banner flag.
type markedPrincipalDoc struct {
	ID                     string  `json:"id"`
	Subject                string  `json:"subject"`
	Kind                   string  `json:"kind"`
	TemporaryAdministrator bool    `json:"temporaryAdministrator"`
	TemporaryExpiresAt     *string `json:"temporaryExpiresAt"`
}

// storedPrincipalCase is one row of TestStoredPrincipalOnFailedTempAdminRead.
type storedPrincipalCase struct {
	name    string
	kind    string
	failing bool
	change  bool
	marked  bool
}

// TestStoredPrincipalOnFailedTempAdminRead proves that a principal create and
// a principal change answer the stored principal when the read of the
// temporary administrator records fails: the status is the success status,
// the person is marked (fail closed), the audit log holds one success entry,
// and the log holds one warning with the principal ID and the error type
// only. A working read answers the real flag and writes no warning.
func TestStoredPrincipalOnFailedTempAdminRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []storedPrincipalCase{
		{name: "create person fails closed", kind: "person", failing: true, change: false, marked: true},
		{name: "change person fails closed", kind: "person", failing: true, change: true, marked: true},
		{name: "create service is not marked", kind: "service", failing: true, change: false, marked: false},
		{name: "create reads the flag", kind: "person", failing: false, change: false, marked: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runStoredPrincipalCase(t, tc)
		})
	}
}

// runStoredPrincipalCase runs one row of TestStoredPrincipalOnFailedTempAdminRead.
func runStoredPrincipalCase(t *testing.T, tc storedPrincipalCase) {
	t.Helper()

	svc, st := newTestPrincipalStore(t)
	log := &capturedLog{mu: sync.Mutex{}, records: nil}
	opts := principalHandlerOptions{
		now: nil, authenticator: nil, logger: slog.New(log), onAuditFailure: nil, tempAdmins: nil,
	}

	if tc.failing {
		opts.tempAdmins = failingTempAdmins{err: tempAdminReadError{}}
	}

	h := newReachPrincipalHandlerWith(t, svc, st, routeReach(st), opts)

	var doc markedPrincipalDoc

	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","displayName":"Alice","kind":"` + tc.kind + `"}`,
		http.StatusCreated,
	}, &doc)

	writes := 1

	if tc.change {
		created := doc

		mustCall(t, h, apiCall{
			http.MethodPatch, "/api/server/principals/" + created.ID,
			`{"displayName":"Alice B"}`, http.StatusOK,
		}, &doc)

		if doc.ID != created.ID {
			t.Fatalf("changed ID = %q, want %q", doc.ID, created.ID)
		}

		writes = 2
	}

	if doc.TemporaryAdministrator != tc.marked {
		t.Errorf("temporaryAdministrator = %t, want %t", doc.TemporaryAdministrator, tc.marked)
	}

	if doc.TemporaryExpiresAt != nil {
		t.Errorf("temporaryExpiresAt = %q, want none", *doc.TemporaryExpiresAt)
	}

	want := 0
	if tc.failing {
		want = writes
	}

	checkStoredPrincipalWarnings(t, log, doc.ID, want)
	checkStoredPrincipalEntries(t, st, doc.ID, writes)
}

// TestReadStaysAnErrorOnFailedTempAdminRead proves that a read, which stored
// nothing, still answers 500 when the read of the records fails: the fail-closed
// mark applies only after a committed write.
func TestReadStaysAnErrorOnFailedTempAdminRead(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)
	opts := principalHandlerOptions{
		now: nil, authenticator: nil, logger: slog.New(slog.DiscardHandler), onAuditFailure: nil,
		tempAdmins: failingTempAdmins{err: tempAdminReadError{}},
	}
	h := newReachPrincipalHandlerWith(t, svc, st, routeReach(st), opts)

	for _, path := range []string{"/api/server/principals"} {
		status, body := call(t, h, apiCall{http.MethodGet, path, "", http.StatusInternalServerError})
		if status != http.StatusInternalServerError || !json.Valid([]byte(body)) {
			t.Errorf("GET %s = %d %s, want 500 with a JSON error", path, status, body)
		}
	}
}

// memberDocMarked is a group member on the wire with the banner flag.
type memberDocMarked struct {
	Principal markedPrincipalDoc `json:"principal"`
}

// TestMemberAddOnFailedTempAdminRead proves that a member add answers the
// stored member with the person marked (fail closed) when the read of the
// temporary administrator records fails, and that the list of members of the
// group is a read: it answers 500. It fails when the memberResponse or the
// memberListResponse case of markAllTempAdmin is removed.
func TestMemberAddOnFailedTempAdminRead(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)
	log := &capturedLog{mu: sync.Mutex{}, records: nil}
	opts := principalHandlerOptions{
		now: nil, authenticator: nil, logger: slog.New(log), onAuditFailure: nil,
		tempAdmins: failingTempAdmins{err: tempAdminReadError{}},
	}
	h := newReachPrincipalHandlerWith(t, svc, st, routeReach(st), opts)

	var person markedPrincipalDoc

	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","displayName":"Alice","kind":"person"}`, http.StatusCreated,
	}, &person)

	var group groupDoc

	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"Ops","description":"Runs it"}`, http.StatusCreated,
	}, &group)

	path := "/api/server/groups/" + group.ID + "/members"

	var member memberDocMarked

	mustCall(t, h, apiCall{http.MethodPost, path, `{"principalID":"` + person.ID + `"}`, http.StatusCreated}, &member)

	if member.Principal.ID != person.ID || !member.Principal.TemporaryAdministrator ||
		member.Principal.TemporaryExpiresAt != nil {
		t.Errorf("added member = %+v, want the person marked with no expiry", member.Principal)
	}

	status, body := call(t, h, apiCall{http.MethodGet, path, "", http.StatusInternalServerError})
	if status != http.StatusInternalServerError || !json.Valid([]byte(body)) {
		t.Errorf("GET %s = %d %s, want 500 with a JSON error", path, status, body)
	}
}

// checkStoredPrincipalWarnings proves the warnings of the log: want of them,
// each with the principal ID and the error type only, and no record with the
// text of the failed read.
func checkStoredPrincipalWarnings(t *testing.T, log *capturedLog, id string, want int) {
	t.Helper()

	var warnings []slog.Record

	records := log.snapshot()
	for i := range records {
		if records[i].Message == storedPrincipalWarning && records[i].Level == slog.LevelWarn {
			warnings = append(warnings, records[i])
		}

		records[i].Attrs(func(a slog.Attr) bool {
			if a.Value.String() == (tempAdminReadError{}).Error() {
				t.Errorf("attribute %q holds the text of the failed read", a.Key)
			}

			return true
		})
	}

	if len(warnings) != want {
		t.Fatalf("warnings = %d, want %d", len(warnings), want)
	}

	if want == 0 {
		return
	}

	last := warnings[len(warnings)-1]
	if got := attrOf(last, "principal"); got != "principal:"+id {
		t.Errorf("principal of the warning = %q, want %q", got, "principal:"+id)
	}

	if got := attrOf(last, "cause"); got != "server_test.tempAdminReadError" {
		t.Errorf("cause of the warning = %q, want the error type", got)
	}
}

// checkStoredPrincipalEntries proves that the audit log holds one success
// entry for each write of the principal, and no failure entry.
func checkStoredPrincipalEntries(t *testing.T, st *sqlite.Store, id string, writes int) {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	count := 0

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Object != "principal:"+id || e.Action != string(identity.ActionServerManagePrincipals) {
			continue
		}

		count++

		if e.Outcome != audit.OutcomeSuccess {
			t.Errorf("entry outcome = %q, want success", e.Outcome)
		}
	}

	if count != writes {
		t.Fatalf("entries for the principal = %d, want %d", count, writes)
	}
}

// countingTempAdmins is a temporary administrator reader that counts its
// reads of the list.
type countingTempAdmins struct {
	lists *atomic.Int64
}

func (c countingTempAdmins) GetTempAdminBySubject(context.Context, string) (tempadmin.TempAdmin, error) {
	return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
}

func (c countingTempAdmins) ListTempAdmins(context.Context) ([]tempadmin.TempAdmin, error) {
	c.lists.Add(1)

	return nil, nil
}

// TestBodyWithoutPrincipalsSkipsTempAdminRead proves that a result whose body
// holds no principal does not read the temporary administrator records, and
// that a result with a person reads them once. It fails when the check of the
// body type is removed from markTempAdmin.
func TestBodyWithoutPrincipalsSkipsTempAdminRead(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)
	lists := &atomic.Int64{}
	opts := principalHandlerOptions{
		now: nil, authenticator: nil, logger: slog.New(slog.DiscardHandler), onAuditFailure: nil,
		tempAdmins: countingTempAdmins{lists: lists},
	}
	h := newReachPrincipalHandlerWith(t, svc, st, routeReach(st), opts)

	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"Ops","description":"Runs it"}`, http.StatusCreated,
	}, nil)

	if got := lists.Load(); got != 0 {
		t.Fatalf("reads after a group create = %d, want 0", got)
	}

	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","displayName":"Alice","kind":"person"}`, http.StatusCreated,
	}, nil)

	if got := lists.Load(); got != 1 {
		t.Fatalf("reads after a principal create = %d, want 1", got)
	}
}

// TestMarkAllTempAdminMarksEveryBody proves that the fail-closed mark covers
// each body type that holds a person, and no person of another kind. It fails
// when a case is removed from markAllTempAdmin.
func TestMarkAllTempAdminMarksEveryBody(t *testing.T) {
	t.Parallel()

	marked := apiserver.MarkAllTempAdminForTest(apiserver.PrincipalBodiesForTest()...)

	for i, flag := range apiserver.FlagsOfBodiesForTest(marked) {
		if !flag {
			t.Errorf("body %d (%T): person not marked", i, marked[i])
		}
	}

	if len(marked) != 4 {
		t.Fatalf("bodies = %d, want 4", len(marked))
	}
}
