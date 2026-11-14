package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// recordingSweeper is an apiserver.DeletedUnitSweeper that records the entry
// of each sweep and answers res or err.
type recordingSweeper struct {
	mu      sync.Mutex
	entries []audit.Entry
	res     inventory.SweepResult
	err     error
}

func (s *recordingSweeper) Sweep(_ context.Context, e audit.Entry) (inventory.SweepResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, e)

	return s.res, s.err
}

// errSweepRead stands for a store error that the sweep cannot recover from.
var errSweepRead = errors.New("pq: read failed on 10.0.0.7")

// newSweepHandler builds a Handler whose retention sweep is sweeper, which
// may be nil, and which denies the actions in deny.
func newSweepHandler(
	t *testing.T, sweeper apiserver.DeletedUnitSweeper, deny map[identity.Action]bool,
) testHandler {
	t.Helper()

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
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: deny},
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
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		DeletedUnits:     sweeper,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// sweptUnit decodes one purged unit of the answer of the sweep route.
type sweptUnit struct {
	Tool      string `json:"tool"`
	Namespace string `json:"namespace"`
	UnitID    string `json:"unitID"`
	Reference string `json:"reference"`
}

// sweepAnswer decodes the answer of the sweep route.
type sweepAnswer struct {
	Purged             []sweptUnit `json:"purged"`
	Failed             int         `json:"failed"`
	Skipped            int         `json:"skipped"`
	FinishedBlobPurges int         `json:"finishedBlobPurges"`
	PendingBlobPurges  int         `json:"pendingBlobPurges"`
}

// checkSweepAnswer checks that body names the unit stk-1 as
// pulumi:acme/web/prod and holds the counts 1, 2, 3 and 4.
func checkSweepAnswer(t *testing.T, body string) {
	t.Helper()

	var got sweepAnswer
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	want := sweepAnswer{
		Purged: []sweptUnit{
			{Tool: "pulumi", Namespace: "acme", UnitID: "stk-1", Reference: "pulumi:acme/web/prod"},
		},
		Failed:             1,
		Skipped:            2,
		FinishedBlobPurges: 3,
		PendingBlobPurges:  4,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sweep answer = %+v, want %+v", got, want)
	}
}

// TestSweepDeletedUnitsAnswersTheOutcome proves that the sweep route runs
// one sweep with the caller as the subject and the action unit:purge, and
// answers each purged unit and the counts.
func TestSweepDeletedUnitsAnswersTheOutcome(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	sweeper := &recordingSweeper{mu: sync.Mutex{}, entries: nil, err: nil, res: inventory.SweepResult{
		Purged: []inventory.DeletedEntry{{
			DeletedAt: at, PurgeAfter: at, Namespace: "acme", Tool: "pulumi", UnitID: "stk-1", Name: "web/prod",
		}},
		Failed: 1, Skipped: 2, FinishedBlobPurges: 3, PendingBlobPurges: 4,
		WaitingBlobPurges: 0, WaitingFor: "",
	}}
	handler := newSweepHandler(t, sweeper, map[identity.Action]bool{})

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/deleted-units/sweep", "")
	if status != http.StatusOK {
		t.Fatalf("sweep: status %d, want 200 (%s)", status, body)
	}

	checkSweepAnswer(t, body)

	if len(sweeper.entries) != 1 {
		t.Fatalf("sweeps = %d, want 1", len(sweeper.entries))
	}

	e := sweeper.entries[0]
	if e.Subject != "tester" || e.Action != "unit:purge" || e.Kind != audit.KindChange ||
		e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("sweep entry = %+v, want the caller, unit:purge, change and success", e)
	}
}

// TestSweepDeletedUnitsRefusals proves the answers of the sweep route
// without server:maintenance (403), without a sweeper (501), when the sweep
// cannot read the store (500, without the error text), and while another
// sweep runs (409, at once).
func TestSweepDeletedUnitsRefusals(t *testing.T) {
	t.Parallel()

	running := &recordingSweeper{mu: sync.Mutex{}, entries: nil, res: inventory.SweepResult{
		Purged: nil, Failed: 0, Skipped: 0, FinishedBlobPurges: 0, PendingBlobPurges: 0,
		WaitingBlobPurges: 0, WaitingFor: "",
	}, err: inventory.ErrSweepRunning}
	failing := &recordingSweeper{mu: sync.Mutex{}, entries: nil, res: inventory.SweepResult{
		Purged: nil, Failed: 0, Skipped: 0, FinishedBlobPurges: 0, PendingBlobPurges: 0,
		WaitingBlobPurges: 0, WaitingFor: "",
	}, err: errSweepRead}

	cases := []struct {
		name    string
		sweeper apiserver.DeletedUnitSweeper
		deny    map[identity.Action]bool
		want    int
	}{
		{
			name:    "no maintenance action",
			sweeper: failing,
			deny:    map[identity.Action]bool{identity.ActionServerMaintenance: true},
			want:    http.StatusForbidden,
		},
		{name: "no sweeper", sweeper: nil, deny: map[identity.Action]bool{}, want: http.StatusNotImplemented},
		{
			name:    "store read fails",
			sweeper: failing,
			deny:    map[identity.Action]bool{},
			want:    http.StatusInternalServerError,
		},
		{name: "a sweep runs", sweeper: running, deny: map[identity.Action]bool{}, want: http.StatusConflict},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newSweepHandler(t, tc.sweeper, tc.deny)

			status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/deleted-units/sweep", "")
			if status != tc.want {
				t.Fatalf("sweep: status %d, want %d (%s)", status, tc.want, body)
			}

			if strings.Contains(body, "10.0.0.7") {
				t.Fatalf("sweep answer holds the store error text: %s", body)
			}
		})
	}
}
