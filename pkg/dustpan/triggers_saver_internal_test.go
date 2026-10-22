package dustpan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// liveUnits answers every unit as live.
type liveUnits struct{}

func (liveUnits) Object(_ context.Context, u graph.Unit) (identity.Object, error) {
	obj, err := identity.UnitObject(u.Tool, u.Namespace, u.ID)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit object: %w", err)
	}

	return obj.WithUnitID(u.ID), nil
}

func (liveUnits) Find(_ context.Context, ns namespace.Name, unitID string) (graph.Unit, error) {
	return graph.Unit{Namespace: ns, Tool: "opentofu", ID: unitID}, nil
}

// savePrincipal stores a person with the ID id and the subject subject.
func savePrincipal(t *testing.T, st *sqlite.Store, id, subject string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), ID: id, Subject: subject, DisplayName: subject,
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
		DisabledAt: time.Time{},
	}

	if err := st.CreatePrincipal(t.Context(), p, storetest.ServerChange(time.Unix(0, 0), "principal:"+id)); err != nil {
		t.Fatalf("create principal %q: %v", id, err)
	}
}

// TestSaverRecordsNameThePrincipalRecord proves: the saver ID that the server
// resolver answers is the ID of the principal record, not the subject. A
// principal that is deleted, and a new principal with the same subject that
// holds grants, do not inherit a trigger that the first principal saved.
func TestSaverRecordsNameThePrincipalRecord(t *testing.T) {
	t.Parallel()

	const subject = "user:ann"

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	check, err := trigger.NewSaverCheck(trigger.SaverCheckConfig{
		Resolver: saverRecords{records: st}, Authorizer: &recordAuthorizer{calls: nil, err: nil}, Units: liveUnits{},
	})
	if err != nil {
		t.Fatalf("NewSaverCheck: %v", err)
	}

	savePrincipal(t, st, "prn-first", subject)

	firstID, err := check.SaverID(t.Context(), subject)
	if err != nil || firstID != "prn-first" {
		t.Fatalf("SaverID = %q, %v; want the record ID prn-first", firstID, err)
	}

	saved := trigger.Trigger{
		ID: "trg-1", Unit: graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "unit-1"},
		Kind: trigger.KindReviewRequestPush, Frequency: trigger.Frequency{Preset: "", Cron: ""},
		Source: graph.Unit{Namespace: "", Tool: "", ID: ""}, Enabled: true, LastSaver: subject, LastSaverID: firstID,
	}

	if err := check.CheckStart(t.Context(), saved); err != nil {
		t.Fatalf("CheckStart of the saver = %v, want nil", err)
	}

	if err := st.DeletePrincipal(
		t.Context(), "prn-first", storetest.ServerChange(time.Unix(1, 0), "principal:prn-first"),
	); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	savePrincipal(t, st, "prn-second", subject)

	secondID, err := check.SaverID(t.Context(), subject)
	if err != nil || secondID != "prn-second" {
		t.Fatalf("SaverID after the new principal = %q, %v; want prn-second", secondID, err)
	}

	err = check.CheckStart(t.Context(), saved)
	if !errors.Is(err, trigger.ErrSaverDenied) {
		t.Errorf("CheckStart with a new principal of the subject = %v, want ErrSaverDenied", err)
	}
}
