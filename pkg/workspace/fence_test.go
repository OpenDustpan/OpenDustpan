package workspace_test

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// stateDoc is a state document with one resource. The fence keeps the
// resources and changes only the lineage and the serial.
const stateDoc = `{"version":4,"lineage":"lin-old","serial":1,"resources":[{"type":"null_resource","name":"a"}]}`

// smallDoc is a state document that fits the plain fixture, which caps state
// at 64 bytes.
const smallDoc = `{"lineage":"lin-old","serial":1,"resources":[]}`

// readFenced reads the plain content of sv through the service that owns it.
func readFenced(t *testing.T, svc *workspace.Service, sv workspace.StateVersion) map[string]json.RawMessage {
	t.Helper()

	r, err := svc.Open(t.Context(), sv)
	if err != nil {
		t.Fatalf("open fence %s: %v", sv.ID, err)
	}

	raw, readErr := io.ReadAll(r)
	closeErr := r.Close()

	if readErr != nil || closeErr != nil {
		t.Fatalf("read fence %s: %v, close %v", sv.ID, readErr, closeErr)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fence %s is not a JSON object: %v", sv.ID, err)
	}

	return doc
}

// A force-unlock of a sealed state writes a sealed fence: the blob holds no
// plain state, and the plain content reads back with the new lineage and the
// next serial, and the resources of the state before it.
func TestForceUnlockFencesSealedState(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	f.pushPlain(t, stateDoc, nil, 1)

	if _, err := f.sealed.SealUnsealedStates(ctx); err != nil {
		t.Fatalf("seal v1: %v", err)
	}

	if _, err := f.sealed.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if _, err := f.sealed.ForceUnlock(ctx, f.ws.ID, "admin"); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	cur, err := f.sealed.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if !cur.Sealed || cur.FenceBy != "admin" || cur.Serial != 2 || cur.Lineage == "lin-old" {
		t.Fatalf("fence sealed=%t by %q serial %d lineage %q, want a sealed admin fence at serial 2",
			cur.Sealed, cur.FenceBy, cur.Serial, cur.Lineage)
	}

	assertBlobHidesState(t, f, cur)

	doc := readFenced(t, f.sealed, cur)

	if string(doc["lineage"]) != `"`+cur.Lineage+`"` || string(doc["serial"]) != "2" {
		t.Fatalf(
			"plain fence lineage %s serial %s, want the stored lineage and serial 2",
			doc["lineage"],
			doc["serial"],
		)
	}

	if !strings.Contains(string(doc["resources"]), "null_resource") {
		t.Fatalf("plain fence lost the resources: %s", doc["resources"])
	}
}

// assertBlobHidesState checks that the blob of a sealed fence holds neither the
// resources nor the lineage of the state in plain text.
func assertBlobHidesState(t *testing.T, f sealFixture, cur workspace.StateVersion) {
	t.Helper()

	raw, err := f.blobs.Get(t.Context(), cur.Content)
	if err != nil {
		t.Fatalf("blob of the fence: %v", err)
	}

	stored, readErr := io.ReadAll(raw)
	closeErr := raw.Close()

	if readErr != nil || closeErr != nil {
		t.Fatalf("read the sealed blob: %v, close %v", readErr, closeErr)
	}

	if strings.Contains(string(stored), "null_resource") || strings.Contains(string(stored), cur.Lineage) {
		t.Fatal("the fence blob holds plain state")
	}
}

// A force-unlock of a state that is not sealed writes a plain fence, with
// the same posture as the state before it.
func TestForceUnlockFencesPlainState(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
		t.Fatalf("push v1: %v", err)
	}

	if _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin"); err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	cur, err := f.svc.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}

	if cur.Sealed || cur.FenceBy != "admin" || cur.Serial != 2 || cur.Lineage == "lin-old" {
		t.Fatalf("fence sealed=%t by %q serial %d lineage %q, want a plain admin fence at serial 2",
			cur.Sealed, cur.FenceBy, cur.Serial, cur.Lineage)
	}

	doc := readFenced(t, f.svc, cur)

	if string(doc["lineage"]) != `"`+cur.Lineage+`"` || string(doc["serial"]) != "2" {
		t.Fatalf("fence lineage %s serial %s, want the stored lineage and serial 2", doc["lineage"], doc["serial"])
	}
}

// A force-unlock of a workspace with no state only releases its lock: no
// version is written.
func TestForceUnlockWithoutStateOnlyReleases(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	released, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin")
	if err != nil {
		t.Fatalf("force unlock: %v", err)
	}

	if released.Lock.Locked() {
		t.Fatalf("workspace still locked by %q", released.Lock.Holder)
	}

	if _, curErr := f.svc.Current(ctx, f.ws.ID); !errors.Is(curErr, workspace.ErrNoState) {
		t.Fatalf("current after a force-unlock with no state: err = %v, want ErrNoState", curErr)
	}
}

// A sealed state needs the sealer to write its fence. Without it the
// force-unlock is refused and the lock stays.
func TestForceUnlockSealedNeedsSealer(t *testing.T) {
	t.Parallel()

	f := newSealFixture(t)
	ctx := t.Context()

	f.pushPlain(t, stateDoc, nil, 1)

	if _, err := f.sealed.SealUnsealedStates(ctx); err != nil {
		t.Fatalf("seal v1: %v", err)
	}

	if _, err := f.sealed.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if _, err := f.plain.ForceUnlock(ctx, f.ws.ID, "admin"); !errors.Is(err, workspace.ErrSealerNotConfigured) {
		t.Fatalf("force unlock without a sealer: err = %v, want ErrSealerNotConfigured", err)
	}

	held, err := f.plain.GetByID(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	if !held.Lock.Locked() {
		t.Fatal("the lock was released without a fence")
	}
}
