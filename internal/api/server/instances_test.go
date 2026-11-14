package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// newInstanceHandler builds a Handler with the instance routes on st, and an
// authorizer that denies the actions of deny.
func newInstanceHandler(
	t *testing.T, st *sqlite.Store, instances installation.InstanceStore, deny ...identity.Action,
) testHandler {
	t.Helper()

	denied := map[identity.Action]bool{}
	for _, action := range deny {
		denied[action] = true
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		TempAdmins:       nil,
		Now:              nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: denied},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       newTestNamespaces(t),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Tokens:           nil,
		Access:           nil,
		Audit:            st,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		CheckpointEpochs: nil,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     nil,
		AuditSinks:       nil,
		SignIn:           nil,
		DenialLimiter:    nil,
		Instances:        instances,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// seedInstances registers the rows old (v0.4.0) and new (v0.5.0).
func seedInstances(t *testing.T, st *sqlite.Store) {
	t.Helper()

	start := time.Date(2026, time.October, 20, 8, 0, 0, 0, time.UTC)

	for _, row := range []installation.Instance{
		{StartedAt: start.Add(time.Minute), ID: "ins-new", BinaryVersion: "v0.5.0"},
		{StartedAt: start, ID: "ins-old", BinaryVersion: "v0.4.0"},
	} {
		if err := st.RegisterInstance(t.Context(), row); err != nil {
			t.Fatalf("register %s: %v", row.ID, err)
		}
	}
}

// serverEntries returns the entries of the scope @server.
func serverEntries(t *testing.T, st *sqlite.Store) []audit.Entry {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list server scope: %v", err)
	}

	return listing.Entries
}

func TestListInstances(t *testing.T) {
	t.Parallel()

	st := openEpochStore(t)
	seedInstances(t, st)

	handler := newInstanceHandler(t, st, st)

	status, body := serveMaintenance(t, handler, http.MethodGet, "/api/server/instances", "")
	if status != http.StatusOK {
		t.Fatalf("list: status %d, body %s, want 200", status, body)
	}

	var got listInstancesResponse

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	if len(got.Instances) != 2 || got.Instances[0].ID != "ins-old" || got.Instances[1].ID != "ins-new" {
		t.Fatalf("instances = %+v, want ins-old then ins-new", got.Instances)
	}

	if first := got.Instances[0]; first.BinaryVersion != "v0.4.0" || first.StartedAt != "2026-10-20T08:00:00Z" {
		t.Fatalf("first row = %+v, want v0.4.0 started at 2026-10-20T08:00:00Z", first)
	}
}

func TestListInstancesWhenEmpty(t *testing.T) {
	t.Parallel()

	st := openEpochStore(t)
	handler := newInstanceHandler(t, st, st)

	status, body := serveMaintenance(t, handler, http.MethodGet, "/api/server/instances", "")
	if status != http.StatusOK || strings.TrimSpace(body) != `{"instances":[]}` {
		t.Fatalf("list: status %d, body %s, want 200 and an empty list", status, body)
	}
}

func TestDeleteInstanceWritesAnEntry(t *testing.T) {
	t.Parallel()

	st := openEpochStore(t)
	seedInstances(t, st)

	handler := newInstanceHandler(t, st, st)

	status, body := serveMaintenance(t, handler, http.MethodDelete, "/api/server/instances/ins-old", "")
	if status != http.StatusNoContent {
		t.Fatalf("delete: status %d, body %s, want 204", status, body)
	}

	rows, err := st.ListInstances(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != "ins-new" {
		t.Fatalf("rows after the delete = %+v, %v, want only ins-new", rows, err)
	}

	entries := serverEntries(t, st)
	if len(entries) != 1 {
		t.Fatalf("server entries = %d, want 1", len(entries))
	}

	e := entries[0]
	if e.Kind != audit.KindChange || e.Action != string(identity.ActionServerDeleteInstance) ||
		e.ValuePath != "/instances/ins-old" || e.Outcome != audit.OutcomeSuccess || e.Subject == "" {
		t.Fatalf("delete entry = %+v, want a successful change by a caller on /instances/ins-old", e)
	}
}

func TestDeleteMissingInstance(t *testing.T) {
	t.Parallel()

	st := openEpochStore(t)
	seedInstances(t, st)

	handler := newInstanceHandler(t, st, st)

	status, body := serveMaintenance(t, handler, http.MethodDelete, "/api/server/instances/ins-none", "")
	if status != http.StatusNotFound || !strings.Contains(body, "instance.not-found") {
		t.Fatalf("delete: status %d, body %s, want 404 with instance.not-found", status, body)
	}

	if strings.Contains(body, "ins-none") {
		t.Fatalf("body %s holds the ID of the request", body)
	}

	entries := serverEntries(t, st)
	if len(entries) != 1 || entries[0].Outcome != audit.OutcomeFailure {
		t.Fatalf("entries = %+v, want one failure entry", entries)
	}
}

// TestDeleteInstanceRefusesHostileIDs sends IDs that no instance has to the
// route. The handler refuses them before the store, and no row or entry
// changes.
func TestDeleteInstanceRefusesHostileIDs(t *testing.T) {
	t.Parallel()

	st := openEpochStore(t)
	seedInstances(t, st)

	handler := newInstanceHandler(t, st, st)

	for _, id := range []string{
		"ins%20old", "ins%0Aold", "ins%27%3B%20DROP%20TABLE%20instance_versions", "%25", "ins%2Fold",
		strings.Repeat("a", installation.MaxInstanceFieldLength+1),
	} {
		status, body := serveMaintenance(t, handler, http.MethodDelete, "/api/server/instances/"+id, "")
		if status != http.StatusBadRequest || !strings.Contains(body, "instance.invalid-id") {
			t.Errorf("delete %q: status %d, body %s, want 400 with instance.invalid-id", id, status, body)
		}
	}

	rows, err := st.ListInstances(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v, want both rows", rows, err)
	}

	if entries := serverEntries(t, st); len(entries) != 0 {
		t.Fatalf("entries = %+v, want none for a refused ID", entries)
	}
}

func TestInstanceRoutesNeedTheirActions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		deny   identity.Action
	}{
		{name: "list", method: http.MethodGet, path: "/api/server/instances", deny: identity.ActionServerReadInstances},
		{
			name: "delete", method: http.MethodDelete, path: "/api/server/instances/ins-old",
			deny: identity.ActionServerDeleteInstance,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := openEpochStore(t)
			seedInstances(t, st)

			handler := newInstanceHandler(t, st, st, tt.deny)

			status, body := serveMaintenance(t, handler, tt.method, tt.path, "")
			if status != http.StatusForbidden {
				t.Fatalf("%s: status %d, body %s, want 403", tt.name, status, body)
			}

			rows, err := st.ListInstances(t.Context())
			if err != nil || len(rows) != 2 {
				t.Fatalf("rows = %+v, %v, want both rows after a refused %s", rows, err, tt.name)
			}
		})
	}
}

// TestInstanceRoutesAreOffWithoutAStore proves that a handler with no store
// mounts no instance route.
func TestInstanceRoutesAreOffWithoutAStore(t *testing.T) {
	t.Parallel()

	handler := newInstanceHandler(t, openEpochStore(t), nil)

	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/api/server/instances"},
		{http.MethodDelete, "/api/server/instances/ins-old"},
	} {
		if status, body := serveMaintenance(t, handler, tt.method, tt.path, ""); status != http.StatusNotFound {
			t.Errorf("%s %s: status %d, body %s, want 404", tt.method, tt.path, status, body)
		}
	}
}

// listInstancesResponse decodes the list of instance rows in tests.
type listInstancesResponse struct {
	Instances []instanceRow `json:"instances"`
}

// instanceRow decodes one instance row in tests.
type instanceRow struct {
	ID            string `json:"id"`
	BinaryVersion string `json:"binaryVersion"`
	StartedAt     string `json:"startedAt"`
}

// failingInstances is an InstanceStore whose delete fails with err.
type failingInstances struct {
	installation.InstanceStore

	err error
}

func (f failingInstances) DeleteInstance(context.Context, string) error { return f.err }

// TestBelowMinimumAnswersWithBothVersions proves the answer to a write that a
// binary below the minimum made: 503, the code, both versions in the text.
func TestBelowMinimumAnswersWithBothVersions(t *testing.T) {
	t.Parallel()

	st := openEpochStore(t)
	below := &schema.MinimumError{SchemaMinimum: "v0.5.0", BinaryVersion: "v0.4.0"}
	handler := newInstanceHandler(t, st, failingInstances{InstanceStore: st, err: below})

	status, body := serveMaintenance(t, handler, http.MethodDelete, "/api/server/instances/ins-old", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status %d, body %s, want 503", status, body)
	}

	for _, want := range []string{"schema.binary-below-minimum", "v0.4.0", "v0.5.0", "binaryVersion", "minimumVersion"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s does not hold %q", body, want)
		}
	}
}
