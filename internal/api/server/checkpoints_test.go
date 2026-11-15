package server_test

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// countingEpochs is a CheckpointEpochs that counts up from 1.
type countingEpochs struct {
	next int64
}

func (c *countingEpochs) NewEpoch(context.Context, time.Time) (int64, error) {
	c.next++

	return c.next, nil
}

func (c *countingEpochs) Now() time.Time {
	return time.Now()
}

// clockEpochs records the time that NewEpoch receives.
type clockEpochs struct {
	now time.Time
	got time.Time
}

func (c *clockEpochs) NewEpoch(_ context.Context, at time.Time) (int64, error) {
	c.got = at

	return 7, nil
}

func (c *clockEpochs) Now() time.Time {
	return c.now
}

// newEpochHandler builds a Handler with epochs and the audit store st.
func newEpochHandler(t *testing.T, epochs apiserver.CheckpointEpochs, st *sqlite.Store) testHandler {
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
		TempAdmins:       nil,
		Now:              nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
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
		CheckpointEpochs: epochs,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     nil,
		AuditSinks:       nil,
		SignIn:           nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// openEpochStore opens a SQLite store that the test closes.
func openEpochStore(t *testing.T) *sqlite.Store {
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

func TestNewCheckpointEpochIsAChange(t *testing.T) {
	t.Parallel()

	st := openEpochStore(t)
	handler := newEpochHandler(t, &countingEpochs{next: 0}, st)

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/audit/checkpoint-epochs", "")
	if status != http.StatusCreated || !strings.Contains(body, `"epoch":1`) {
		t.Fatalf("new epoch: status %d, body %s, want 201 and epoch 1", status, body)
	}

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list server scope: %v", err)
	}

	if len(listing.Entries) != 1 {
		t.Fatalf("server entries: got %d, want 1", len(listing.Entries))
	}

	e := listing.Entries[0]
	if e.Kind != audit.KindChange || e.Action != string(identity.ActionServerManageAudit) ||
		e.ValuePath != "/audit/checkpoints/epoch/1" || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("epoch entry: got %+v", e)
	}
}

func TestNewCheckpointEpochWhileCheckpointsAreOff(t *testing.T) {
	t.Parallel()

	handler := newEpochHandler(t, nil, openEpochStore(t))

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/audit/checkpoint-epochs", "")
	if status != http.StatusConflict || !strings.Contains(body, "audit checkpoints are off") {
		t.Fatalf("new epoch with checkpoints off: status %d, body %s, want 409", status, body)
	}
}

func TestNewCheckpointEpochUsesTheTimeOfThePort(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
	epochs := &clockEpochs{now: at, got: time.Time{}}
	handler := newEpochHandler(t, epochs, openEpochStore(t))

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/audit/checkpoint-epochs", "")
	if status != http.StatusCreated || !strings.Contains(body, `"epoch":7`) {
		t.Fatalf("new epoch: status %d, body %s, want 201 and epoch 7", status, body)
	}

	if !epochs.got.Equal(at) {
		t.Fatalf("new epoch time: got %s, want %s", epochs.got.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano))
	}
}
