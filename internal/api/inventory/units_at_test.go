package inventory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memHistory is an in-memory inventory.HistoryReader over the change log of
// the namespace acme.
type memHistory struct {
	rows []coreinventory.Change
}

func (m *memHistory) ListChanges(
	context.Context, namespace.Name, time.Time, *coreinventory.ChangeCursor, int,
) ([]coreinventory.Change, error) {
	return m.rows, nil
}

func (m *memHistory) UnitsAt(_ context.Context, _ namespace.Name, at time.Time) ([]coreinventory.Entry, error) {
	return coreinventory.ReplayAt(m.rows, at), nil
}

func (m *memHistory) ViewAt(_ context.Context, _ namespace.Name, at time.Time) (coreinventory.UnitsView, error) {
	return coreinventory.ReplayView(m.rows, at), nil
}

func (m *memHistory) UnitNames(
	_ context.Context, _ namespace.Name, tool, unitID string, at time.Time,
) (coreinventory.Names, error) {
	return coreinventory.NamesAt(m.rows, tool, unitID, at), nil
}

// atTime is the time that the rows of the view tests count from.
func atTime(minute int) time.Time {
	return time.Date(2026, time.October, 10, 9, minute, 0, 0, time.UTC)
}

// row returns a change log row of the namespace acme with the sequence minute.
func row(tool, id, name string, kind coreinventory.ChangeKind, minute int) coreinventory.Change {
	return coreinventory.Change{
		At: atTime(minute), Seq: int64(minute), Namespace: "acme", Tool: tool, UnitID: id, Name: name,
		DisplayName: name, Status: "", Collection: "", Kind: kind, ResourceCount: nil,
	}
}

// seededHistory holds a renamed stack, a workspace that is soft-deleted now,
// a stack that a purge removed, and a stack that came after the time.
func seededHistory() *memHistory {
	return &memHistory{rows: []coreinventory.Change{
		row("pulumi", "stk-a", "web/prod", coreinventory.ChangeCreated, 1),
		row("opentofu", "ws-b", "network", coreinventory.ChangeCreated, 2),
		row("pulumi", "stk-c", "api/prod", coreinventory.ChangeCreated, 3),
		row("pulumi", "stk-a", "web/live", coreinventory.ChangeRenamed, 10),
		row("opentofu", "ws-b", "network", coreinventory.ChangeDeleted, 11),
		row("pulumi", "stk-c", "api/prod", coreinventory.ChangeDeleted, 12),
		row("pulumi", "stk-c", "api/prod", coreinventory.ChangePurged, 13),
		row("pulumi", "stk-d", "later", coreinventory.ChangeCreated, 20),
	}}
}

// unitAtBody is one decoded unit of the view at a time.
type unitAtBody struct {
	unitBody

	UnitID      string `json:"unitID"`
	CurrentName string `json:"currentName"`
	Deleted     bool   `json:"deleted"`
	DeletedAt   string `json:"deletedAt"`
}

// unitsAtBody is the decoded view at a time.
type unitsAtBody struct {
	Units   []unitAtBody `json:"units"`
	At      string       `json:"at"`
	Purged  int          `json:"purged"`
	Omitted int          `json:"omitted"`
}

func newHistoryHandler(
	t *testing.T, history coreinventory.HistoryReader, authz identity.Authorizer,
) testHandler {
	t.Helper()

	return newHistoryHandlerWithURL(t, history, authz, "")
}

// newHistoryHandlerWithURL is newHistoryHandler with the public URL of the
// server.
func newHistoryHandlerWithURL(
	t *testing.T, history coreinventory.HistoryReader, authz identity.Authorizer, publicURL string,
) testHandler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	namespaces, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, apiinventory.Config{
		Placements:    nil,
		OutputSharing: nil,
		Outputs:       nil,
		Views:         nil,
		Audit:         newTestAudit(), OnAuditFailure: nil, Locks: nil, Moves: nil,
		Collections:   testCollectionService(t, nsStore),
		Namespaces:    namespaces,
		Units:         &memReader{entries: nil, err: nil},
		Deleted:       seededDeleted(),
		History:       history,
		Lifecycles:    testLifecycles(),
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		PublicURL:     publicURL,
		DenialLimiter: nil,
		StateGuard:    stategate.Off("the test keeps no gates"),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

func getUnitsAt(t *testing.T, handler testHandler, at string) (*httptest.ResponseRecorder, unitsAtBody) {
	t.Helper()

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units?at="+at, "test-token")

	var got unitsAtBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode view: %v (%s)", err, rec.Body.String())
		}
	}

	return rec, got
}

func TestListUnitsAt(t *testing.T) {
	t.Parallel()

	handler := newHistoryHandler(t, seededHistory(), selectiveAuthorizer{deny: nil})
	at := atTime(5).Format(time.RFC3339Nano)

	rec, got := getUnitsAt(t, handler, at)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	if got.At != atTime(5).Format(time.RFC3339Nano) || got.Purged != 1 || got.Omitted != 0 || len(got.Units) != 2 {
		t.Fatalf("view = %+v, want 2 units, 1 purged, none omitted", got)
	}

	checkSoftDeletedUnit(t, got.Units[0])
	checkRenamedUnit(t, got.Units[1])

	if containsName(rec.Body.String(), "api/prod") {
		t.Fatalf("the view names a purged unit: %s", rec.Body.String())
	}
}

// checkSoftDeletedUnit checks the workspace that is soft-deleted now.
func checkSoftDeletedUnit(t *testing.T, deleted unitAtBody) {
	t.Helper()

	if deleted.Name != "network" || !deleted.Deleted || deleted.DeletedAt != atTime(11).Format(time.RFC3339Nano) ||
		deleted.Tool != "OpenTofu" || deleted.UnitID != "ws-b" {
		t.Fatalf("soft-deleted unit = %+v, want the workspace network with the mark deleted", deleted)
	}
}

// checkRenamedUnit checks the stack that a rename gave a new name.
func checkRenamedUnit(t *testing.T, renamed unitAtBody) {
	t.Helper()

	if renamed.Name != "web/prod" || renamed.CurrentName != "web/live" || renamed.Deleted ||
		renamed.Reference != "pulumi:acme/web/prod" || renamed.UnitID != "stk-a" || renamed.DeletedAt != "" {
		t.Fatalf("renamed unit = %+v, want web/prod with the current name web/live", renamed)
	}
}

func containsName(body, name string) bool {
	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return false
	}

	found := false

	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			found = found || x == name
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		default:
		}
	}
	walk(raw)

	return found
}

func TestListUnitsAtPoints(t *testing.T) {
	t.Parallel()

	handler := newHistoryHandler(t, seededHistory(), selectiveAuthorizer{deny: nil})

	cases := []struct {
		name       string
		at         time.Time
		wantNames  []string
		wantPurged int
	}{
		{name: "before the first change", at: atTime(0), wantNames: []string{}, wantPurged: 0},
		{name: "after the rename and the purge", at: atTime(14), wantNames: []string{"web/live"}, wantPurged: 0},
		{name: "after every change", at: atTime(30), wantNames: []string{"later", "web/live"}, wantPurged: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, got := getUnitsAt(t, handler, tc.at.Format(time.RFC3339Nano))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
			}

			names := make([]string, 0, len(got.Units))
			for _, u := range got.Units {
				names = append(names, u.Name)
			}

			if fmt.Sprint(names) != fmt.Sprint(tc.wantNames) || got.Purged != tc.wantPurged {
				t.Fatalf("names = %v purged %d, want %v purged %d", names, got.Purged, tc.wantNames, tc.wantPurged)
			}
		})
	}
}

func TestListUnitsAtOmitsUnitsTheCallerCannotRead(t *testing.T) {
	t.Parallel()

	authz := denyUnits{deny: map[string]bool{"opentofu:acme/network": true}}
	handler := newHistoryHandler(t, seededHistory(), authz)

	rec, got := getUnitsAt(t, handler, atTime(5).Format(time.RFC3339Nano))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	if len(got.Units) != 1 || got.Units[0].Name != "web/prod" || got.Omitted != 1 {
		t.Fatalf("view = %+v, want only web/prod and 1 omitted", got)
	}
}

func TestListUnitsAtErrors(t *testing.T) {
	t.Parallel()

	withHistory := newHistoryHandler(t, seededHistory(), selectiveAuthorizer{deny: nil})
	withoutHistory := newHistoryHandler(t, nil, selectiveAuthorizer{deny: nil})

	cases := []struct {
		handler testHandler
		name    string
		at      string
		want    int
	}{
		{
			name:    "a store with no change log",
			handler: withoutHistory,
			at:      "2026-10-10T09:00:00Z",
			want:    http.StatusNotImplemented,
		},
		{name: "a time that is not a time", handler: withHistory, at: "yesterday", want: http.StatusBadRequest},
		{name: "Unix milliseconds", handler: withHistory, at: "1700000000000", want: http.StatusBadRequest},
		{name: "a negative number", handler: withHistory, at: "-5", want: http.StatusBadRequest},
		{name: "a date with no time", handler: withHistory, at: "2026-10-10", want: http.StatusBadRequest},
		{name: "no zone", handler: withHistory, at: "2026-10-10T09:00:00", want: http.StatusBadRequest},
		{name: "a lower case t", handler: withHistory, at: "2026-10-10t09:00:00Z", want: http.StatusBadRequest},
		{name: "a lower case z", handler: withHistory, at: "2026-10-10T09:00:00z", want: http.StatusBadRequest},
		{name: "a space for the T", handler: withHistory, at: "2026-10-10%2009:00:00Z", want: http.StatusBadRequest},
		{name: "a comma fraction", handler: withHistory, at: "2026-10-10T09:00:00,5Z", want: http.StatusBadRequest},
		{
			name:    "a fraction of 10 digits",
			handler: withHistory,
			at:      "2026-10-10T09:00:00.1234567890Z",
			want:    http.StatusBadRequest,
		},
		{name: "an empty fraction", handler: withHistory, at: "2026-10-10T09:00:00.Z", want: http.StatusBadRequest},
		{
			name:    "an offset with no colon",
			handler: withHistory,
			at:      "2026-10-10T09:00:00%2B0200",
			want:    http.StatusBadRequest,
		},
		{
			name:    "an offset of 24 hours",
			handler: withHistory,
			at:      "2026-10-10T09:00:00%2B24:00",
			want:    http.StatusBadRequest,
		},
		{name: "a leap second", handler: withHistory, at: "2026-12-31T23:59:60Z", want: http.StatusBadRequest},
		{name: "a month 13", handler: withHistory, at: "2026-13-10T09:00:00Z", want: http.StatusBadRequest},
		{
			name:    "a day that does not exist",
			handler: withHistory,
			at:      "2026-02-30T09:00:00Z",
			want:    http.StatusBadRequest,
		},
		{name: "a year with 5 digits", handler: withHistory, at: "12026-10-10T09:00:00Z", want: http.StatusBadRequest},
		{name: "before 1970", handler: withHistory, at: "1969-12-31T23:59:59Z", want: http.StatusBadRequest},
		{
			name:    "before 1970 by an offset",
			handler: withHistory,
			at:      "1970-01-01T00:00:00%2B01:00",
			want:    http.StatusBadRequest,
		},
		{name: "year 0", handler: withHistory, at: "0000-01-01T00:00:00Z", want: http.StatusBadRequest},
		{name: "a leading space", handler: withHistory, at: "%202026-10-10T09:00:00Z", want: http.StatusBadRequest},
		{name: "a trailing new line", handler: withHistory, at: "2026-10-10T09:00:00Z%0A", want: http.StatusBadRequest},
		{
			name:    "a CR LF inside",
			handler: withHistory,
			at:      "2026-10-10T09:00:00Z%0D%0AX-Evil:1",
			want:    http.StatusBadRequest,
		},
		{name: "a NUL byte", handler: withHistory, at: "2026-10-10T09:00:00Z%00", want: http.StatusBadRequest},
		{name: "a long value", handler: withHistory, at: strings.Repeat("9", 4096), want: http.StatusBadRequest},
		{name: "a digit overflow", handler: withHistory, at: "99999999999999999999999999", want: http.StatusBadRequest},
		{name: "the far future", handler: withHistory, at: "9999-12-31T23:59:59Z", want: http.StatusOK},
		{name: "the epoch", handler: withHistory, at: "1970-01-01T00:00:00Z", want: http.StatusOK},
		{name: "a fraction", handler: withHistory, at: "2026-10-10T09:00:00.123456789Z", want: http.StatusOK},
		{name: "an offset", handler: withHistory, at: "2026-10-10T11:00:00%2B02:00", want: http.StatusOK},
		{name: "a negative offset", handler: withHistory, at: "2026-10-10T04:00:00-05:00", want: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := authorizedGet(t, tc.handler, "/api/dustpan/v1/namespaces/acme/units?at="+tc.at, "test-token")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}

			if tc.want == http.StatusBadRequest && !strings.Contains(rec.Body.String(), "RFC 3339") {
				t.Fatalf("error text = %s, want it to name RFC 3339", rec.Body.String())
			}
		})
	}
}

// TestListUnitsAtOffsetsAgree checks that the same instant, in UTC and with an
// offset, gives the same view, and that the answer at is a UTC RFC 3339 time
// that is cut to whole milliseconds.
func TestListUnitsAtOffsetsAgree(t *testing.T) {
	t.Parallel()

	handler := newHistoryHandler(t, seededHistory(), selectiveAuthorizer{deny: nil})

	_, utc := getUnitsAt(t, handler, "2026-10-10T09:05:00Z")
	_, offset := getUnitsAt(t, handler, "2026-10-10T11:05:00%2B02:00")
	_, frac := getUnitsAt(t, handler, "2026-10-10T09:05:00.0009Z")

	if len(utc.Units) == 0 || !reflect.DeepEqual(utc, offset) || !reflect.DeepEqual(utc, frac) {
		t.Fatalf("views differ: utc %+v, offset %+v, fraction %+v", utc, offset, frac)
	}

	got, err := time.Parse(time.RFC3339Nano, utc.At)
	if err != nil || !got.Equal(atTime(5)) || !strings.HasSuffix(utc.At, "Z") {
		t.Fatalf("at = %q (%v), want %s in UTC", utc.At, err, atTime(5).Format(time.RFC3339Nano))
	}
}

func TestListUnitsAtNeedsNamespaceRead(t *testing.T) {
	t.Parallel()

	handler := newHistoryHandler(t, seededHistory(), stubAuthorizer{})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units?at=2026-10-10T09:00:00Z", "test-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

// TestListUnitsAtConsoleURL checks that a unit of the view has a consoleUrl,
// and that a unit that is soft-deleted now has none: the console has no page
// for it.
func TestListUnitsAtConsoleURL(t *testing.T) {
	t.Parallel()

	handler := newHistoryHandlerWithURL(
		t,
		seededHistory(),
		selectiveAuthorizer{deny: nil},
		"https://dustpan.example.com",
	)
	at := atTime(5).Format(time.RFC3339Nano)

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units?at="+at, "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got consoleURLView

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode view: %v", err)
	}

	if len(got.Units) == 0 {
		t.Fatal("view holds no unit")
	}

	for _, u := range got.Units {
		want := "https://dustpan.example.com/units/" + u.UnitID
		if u.Deleted {
			want = ""
		}

		if u.ConsoleURL != want {
			t.Fatalf("unit %s (deleted %v) consoleUrl = %q, want %q", u.UnitID, u.Deleted, u.ConsoleURL, want)
		}
	}
}

// consoleURLView is the view at a time, with the console link of each unit.
type consoleURLView struct {
	Units []consoleURLUnit `json:"units"`
}

// consoleURLUnit is one unit of consoleURLView.
type consoleURLUnit struct {
	UnitID     string `json:"unitID"`
	Deleted    bool   `json:"deleted"`
	ConsoleURL string `json:"consoleUrl"` //nolint:tagliatelle // The tool-neutral API names this field consoleUrl.
}
