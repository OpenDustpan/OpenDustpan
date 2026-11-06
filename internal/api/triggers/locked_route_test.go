package triggers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/cron"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/graph"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// longUnitRig returns a rig whose caller holds every action on the unit with the
// given namespace, tool and name, and that has the unit and a schedule trigger on
// it. The grants name the unit exactly as the 301-byte case does, so that only
// the unit name decides the answer.
func longUnitRig(t *testing.T, ns, tool, name string) (*rig, string) {
	t.Helper()

	grants := append(holdsAll(), "namespace:read "+ns)
	for _, a := range []string{"unit:read", "unit:manage", "unit:run-plan"} {
		grants = append(grants, a+" "+tool+":"+ns+"/"+name)
	}

	r := newRig(t, newAuthorizer(grants...))

	return r, plantUnit(r, ns, tool, name)
}

// plantUnit adds a unit with the given namespace, tool and name to the rig, and
// a schedule trigger on it. It returns the path of the fire route of the
// trigger.
func plantUnit(r *rig, ns, tool, name string) string {
	const unitID = "ws-long"

	r.inv.extra[unitID] = coreinventory.Entry{
		LastRelease: nil, ManualLock: nil, LastChanged: time.Unix(0, 0), Namespace: namespace.Name(ns),
		Tool: tool, Name: name, UnitID: unitID, DisplayName: "", Status: "", ResourceCount: nil, Lock: nil,
		Collection: "", Labels: nil,
	}

	r.store.plant(trigger.Trigger{
		ID: "trg-long", Unit: graph.Unit{Namespace: namespace.Name(ns), Tool: tool, ID: unitID},
		Kind: trigger.KindSchedule, Frequency: trigger.Frequency{Preset: cron.PresetDaily, Cron: ""},
		Source: noUnit(), Enabled: true, LastSaver: ada, LastSaverID: "prn-" + ada,
	})

	return fmt.Sprintf("/api/dustpan/v1/namespaces/%s/units/%s/triggers/trg-long/fire", ns, unitID)
}

// TestFireAnswerKeepsTheRemovalRouteOfTheLongestUnit proves that the answer of
// a fire that a manual lock refuses holds the whole removal route for the
// longest unit reference that the answer takes: a namespace of 100 bytes, the
// tool code opentofu and a unit name in which each byte needs escaping in the
// query. The route sentence of this unit is longer than the old limit of 300
// bytes.
func TestFireAnswerKeepsTheRemovalRouteOfTheLongestUnit(t *testing.T) {
	t.Parallel()

	ns := strings.Repeat("n", 100)
	tool := "opentofu"
	// The answer names the unit as namespace/name in an argument of at most
	// errcode.MaxIdentifier characters, so this is the longest name that the
	// answer takes with a namespace of 100 bytes.
	name := strings.Repeat("&", errcode.MaxIdentifier-len(ns)-1)
	unit := collection.UnitRef{Namespace: namespace.Name(ns), Tool: tool, Name: name}

	r, path := longUnitRig(t, ns, tool, name)
	r.disp.err = fmt.Errorf("%w: %w", job.ErrLocked, &manuallock.LockedError{
		Lock: manuallock.Lock{
			Since: time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC), Target: manuallock.UnitTarget(unit),
			Holder: "user-1", Reason: strings.Repeat("r", 2000),
		},
		Unit: unit, Noun: "workspace", Name: ns + "/" + name,
	})

	code, body, _ := do(t, r, http.MethodPost, path, "")
	if code != http.StatusConflict {
		t.Fatalf("status = %d %s, want 409", code, body)
	}

	got := decode[struct {
		Message string `json:"message"`
	}](t, body).Message

	route := refusal.RemovalRoutes(manuallock.UnitTarget(unit)).Remove
	want := ". To remove the lock, the holder sends DELETE " + route + "."
	if !strings.HasSuffix(got, want) {
		t.Fatalf("message has %d bytes and does not end with the whole route sentence of %d bytes", len(got), len(want))
	}

	if len(want) <= 300 {
		t.Fatalf("the route sentence has %d bytes, want a unit that needs more than 300", len(want))
	}

	if len(got) > refusal.MaxLockedTextBytes {
		t.Fatalf("message has %d bytes, want at most %d", len(got), refusal.MaxLockedTextBytes)
	}
}

// TestFireRefusesAUnitNameOfTooManyBytes proves that a unit with a name of 301
// bytes is refused at the boundary with 404, and that the fire never reaches
// the dispatcher with it.
func TestFireRefusesAUnitNameOfTooManyBytes(t *testing.T) {
	t.Parallel()

	r, path := longUnitRig(t, "acme", "pulumi", strings.Repeat("&", 301))

	code, body, _ := do(t, r, http.MethodPost, path, "")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d %s, want 404", code, body)
	}

	if n := len(r.disp.reqs); n != 0 {
		t.Fatalf("dispatcher got %d requests, want 0", n)
	}
}
