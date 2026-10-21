package inventory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
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
	At      int64        `json:"at"`
	Purged  int          `json:"purged"`
	Omitted int          `json:"omitted"`
}

func newHistoryHandler(
	t *testing.T, history coreinventory.HistoryReader, authz identity.Authorizer,
) *apiinventory.Handler {
	t.Helper()

	return newHistoryHandlerWithURL(t, history, authz, "")
}

// newHistoryHandlerWithURL is newHistoryHandler with the public URL of the
// server.
func newHistoryHandlerWithURL(
	t *testing.T, history coreinventory.HistoryReader, authz identity.Authorizer, publicURL string,
) *apiinventory.Handler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	namespaces, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		Views: nil,
		Audit: nil, OnAuditFailure: nil, Locks: nil, Moves: nil,
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
		StateGuard:    nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

func getUnitsAt(t *testing.T, handler *apiinventory.Handler, at string) (*httptest.ResponseRecorder, unitsAtBody) {
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
	at := strconv.FormatInt(atTime(5).UnixMilli(), 10)

	rec, got := getUnitsAt(t, handler, at)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	if got.At != atTime(5).UnixMilli() || got.Purged != 1 || got.Omitted != 0 || len(got.Units) != 2 {
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

			rec, got := getUnitsAt(t, handler, strconv.FormatInt(tc.at.UnixMilli(), 10))
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

	rec, got := getUnitsAt(t, handler, strconv.FormatInt(atTime(5).UnixMilli(), 10))
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
		handler *apiinventory.Handler
		name    string
		at      string
		want    int
	}{
		{name: "a time that is not a number", handler: withHistory, at: "yesterday", want: http.StatusBadRequest},
		{name: "a negative time", handler: withHistory, at: "-5", want: http.StatusBadRequest},
		{name: "a store with no change log", handler: withoutHistory, at: "100", want: http.StatusNotImplemented},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := authorizedGet(t, tc.handler, "/api/dustpan/v1/namespaces/acme/units?at="+tc.at, "test-token")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestListUnitsAtNeedsNamespaceRead(t *testing.T) {
	t.Parallel()

	handler := newHistoryHandler(t, seededHistory(), stubAuthorizer{})

	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units?at=100", "test-token")
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
	at := strconv.FormatInt(atTime(5).UnixMilli(), 10)

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
