package trigger_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/cron"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/trigger"
)

func TestManagerCreateStoresTheSaverID(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	got, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	if got.LastSaverID != saverID(ada) || r.store.triggers[got.ID].LastSaverID != saverID(ada) {
		t.Errorf(
			"saver ID = %q, stored %q; want %q",
			got.LastSaverID,
			r.store.triggers[got.ID].LastSaverID,
			saverID(ada),
		)
	}

	// A replace by another person takes over the trigger, with the ID of that
	// person.
	r.authz.grant(grace, "b", trigger.SettingsAction, trigger.StartAction, identity.ActionUnitRead)

	again, err := r.m.Replace(t.Context(), person(grace), reqInfo(), got.ID, scheduleInput("b"))
	if err != nil {
		t.Fatalf("Replace = %v", err)
	}

	if again.LastSaver != grace || again.LastSaverID != saverID(grace) {
		t.Errorf("replace saver = %q (%q), want %q (%q)", again.LastSaver, again.LastSaverID, grace, saverID(grace))
	}
}

func TestManagerCreateRefusesACallerWithNoRecord(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)
	r.savers.idErr = trigger.ErrSaverDenied

	_, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	wantErr(t, "Create without a record", err, trigger.ErrSaverDenied)

	if r.store.callCount() != 0 {
		t.Errorf("store writes = %d, want none", r.store.callCount())
	}
}

func TestManagerDeleteARowThatFailsItsChecks(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	created, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	// A preset and a cron expression: a row of another version can hold it.
	bad := r.store.triggers[created.ID]
	bad.Frequency = trigger.Frequency{Preset: cron.PresetDaily, Cron: "0 6 * * *"}
	r.store.triggers[created.ID] = bad

	// Every route that needs a valid row refuses it.
	_, _, err = r.m.Get(t.Context(), person(ada), acme, created.ID)
	wantErr(t, "Get", err, trigger.ErrInvalid)

	_, err = r.m.Fire(t.Context(), person(ada), acme, created.ID, trigger.FireInput{
		Reason: "", Location: noLocation(), IdempotencyKey: "", Request: reqInfo(),
	})
	wantErr(t, "Fire", err, trigger.ErrInvalid)

	// The settings action on the unit still decides who may delete it.
	r.authz.grant(grace, "b", identity.ActionUnitRead)

	wantErr(t, "Delete without the settings action",
		r.m.Delete(t.Context(), person(grace), reqInfo(), acme, created.ID), identity.ErrDenied)

	if err := r.m.Delete(t.Context(), person(ada), reqInfo(), acme, created.ID); err != nil {
		t.Fatalf("Delete = %v", err)
	}

	if _, ok := r.store.triggers[created.ID]; ok {
		t.Error("the row is still stored after the delete")
	}
}

func TestManagerFireKeyAndRequest(t *testing.T) {
	t.Parallel()

	r := newManagerRig(t)

	created, err := r.m.Create(t.Context(), person(ada), reqInfo(), scheduleInput("b"))
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	_, err = r.m.Fire(t.Context(), person(ada), acme, created.ID, trigger.FireInput{
		Reason: "", Location: noLocation(), IdempotencyKey: "click-1", Request: reqInfo(),
	})
	if err != nil {
		t.Fatalf("Fire = %v", err)
	}

	if len(r.disp.reqs) != 1 || r.disp.reqs[0].IdempotencyKey != "click-1" || r.disp.reqs[0].Request != reqInfo() {
		t.Fatalf("dispatch = %+v, want the key click-1 and the request", r.disp.reqs)
	}

	for name, key := range map[string]string{
		"too long":      strings.Repeat("k", idempotency.MaxKeyLength+1),
		"not printable": "k\nk",
	} {
		_, err = r.m.Fire(t.Context(), person(ada), acme, created.ID, trigger.FireInput{
			Reason: "", Location: noLocation(), IdempotencyKey: key, Request: reqInfo(),
		})
		wantErr(t, "Fire with a key that is "+name, err, trigger.ErrInvalid)
	}

	if len(r.disp.reqs) != 1 {
		t.Errorf("dispatcher calls = %d, want 1: a key that is not valid never reaches it", len(r.disp.reqs))
	}
}
