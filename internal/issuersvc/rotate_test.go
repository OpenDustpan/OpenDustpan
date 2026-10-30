package issuersvc_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/profile"
)

const day = 24 * time.Hour

func (h *harness) createKeys() []profile.KeyInfo {
	h.t.Helper()

	got, err := h.svc.CreateKeys(h.t.Context(), h.admin, h.ns.Name)
	if err != nil {
		h.t.Fatalf("CreateKeys: %v", err)
	}

	return got
}

func (h *harness) rotateAll() {
	h.t.Helper()

	if err := h.svc.RotateAll(h.t.Context()); err != nil {
		h.t.Fatalf("RotateAll: %v", err)
	}
}

func (h *harness) states() map[profile.KeyState]int {
	h.t.Helper()

	out := map[profile.KeyState]int{}
	keys := h.keysOf(h.ns)
	for i := range keys {
		out[keys[i].State]++
	}

	return out
}

func (h *harness) published() int {
	h.t.Helper()

	var doc profile.JWKS
	if err := json.Unmarshal(h.jwks(h.ns), &doc); err != nil {
		h.t.Fatal(err)
	}

	return len(doc.Keys)
}

func TestScheduleRotatesAtEachBoundary(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	first := h.mustLogin()
	start := h.clock.Now()

	type want struct {
		active, next, retired, published int
	}

	steps := []struct {
		name string
		at   time.Duration
		want want
	}{
		{"day 1: nothing is due", day, want{1, 1, 0, 2}},
		{"day 89: nothing is due", 89 * day, want{1, 1, 0, 2}},
		{"day 90: the next key is active and the old key is retired", 90 * day, want{1, 0, 1, 2}},
		{"day 96: the retired key stays in the JWKS", 96 * day, want{1, 0, 1, 2}},
		{"day 97: the retired key is deleted after 7 days", 97 * day, want{1, 0, 0, 1}},
		{"day 172: seven days before the end the next key does not exist yet", 172 * day, want{1, 0, 0, 1}},
		{"day 173: the next key is made 7 days before the end", 173 * day, want{1, 1, 0, 2}},
		{"day 179: still one active key", 179 * day, want{1, 1, 0, 2}},
		{"day 180: the second promotion", 180 * day, want{1, 0, 1, 2}},
	}

	for _, st := range steps {
		h.clock.Advance(start.Add(st.at).Sub(h.clock.Now()))
		h.rotateAll()

		got := h.states()
		if got[profile.KeyActive] != st.want.active || got[profile.KeyNext] != st.want.next ||
			got[profile.KeyRetired] != st.want.retired || h.published() != st.want.published {
			t.Errorf("%s: states %v, published %d, want %+v", st.name, got, h.published(), st.want)
		}

		if h.published() > profile.MaxPublishedKeys {
			t.Errorf("%s: the JWKS holds %d keys", st.name, h.published())
		}

		tok := h.mustLogin()
		if _, err := h.verify(tok.Value, "sts.amazonaws.com"); err != nil {
			t.Errorf("%s: a new token does not verify: %v", st.name, err)
		}
	}

	if _, err := h.verify(first.Value, "sts.amazonaws.com"); err == nil {
		t.Error("the token of the first key passes after its key was deleted and its life ended")
	}
}

func TestOldKeyStaysPublishedForTheOverlap(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	// The next key waits in the JWKS first. The old key signs the token right
	// before the promotion.
	h.clock.Advance(profile.MinNextKeyAge)

	before := h.mustLogin()

	h.clock.Advance(time.Minute)

	res, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false})
	if err != nil || !res.Promoted {
		t.Fatalf("Rotate = %+v, %v", res, err)
	}

	if after := h.mustLogin(); after.KeyID == before.KeyID {
		t.Fatal("the key did not change")
	}

	// A token that the old key signed a minute before the promotion still
	// verifies when a relying party reads the JWKS after it.
	if _, err = h.verify(before.Value, "sts.amazonaws.com"); err != nil {
		t.Errorf("the token of the old key does not verify in the overlap: %v", err)
	}
}

func TestManualRotationPromotesAndCapsTheRetiredKeys(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	signed := make([]profile.Token, 0, 5)
	signed = append(signed, h.mustLogin())

	for i := range 4 {
		h.clock.Advance(profile.MinNextKeyAge)

		res, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false})
		if err != nil {
			t.Fatalf("rotation %d: %v", i+1, err)
		}

		if !res.Promoted {
			t.Fatalf("rotation %d promoted nothing", i+1)
		}

		got := h.states()
		if got[profile.KeyActive] != 1 || got[profile.KeyNext] != 1 ||
			got[profile.KeyRetired] > profile.MaxRetiredKeys ||
			h.published() > profile.MaxPublishedKeys {
			t.Errorf("rotation %d: states %v, published %d", i+1, got, h.published())
		}

		if i >= 2 && len(res.Deleted) == 0 {
			t.Errorf("rotation %d deleted no retired key, and the cap is %d", i+1, profile.MaxRetiredKeys)
		}

		signed = append(signed, h.mustLogin())
	}

	// The tokens of the last three keys (5 minutes old) still verify; the
	// oldest key was deleted early, as the cap says.
	if _, err := h.verify(signed[len(signed)-1].Value, "sts.amazonaws.com"); err != nil {
		t.Errorf("the newest token: %v", err)
	}

	if _, err := h.verify(signed[0].Value, "sts.amazonaws.com"); err == nil {
		t.Error("the token of a key that the cap deleted still verifies")
	}
}

func TestNormalRotationWithoutANextKeyMakesOne(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()
	h.clock.Advance(90 * day)
	h.rotateAll()

	if got := h.states(); got[profile.KeyNext] != 0 {
		t.Fatalf("states = %v", got)
	}

	active := h.mustLogin().KeyID

	res, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false})
	if err != nil || res.Promoted {
		t.Fatalf("first manual rotation = %+v, %v", res, err)
	}

	if got := h.mustLogin().KeyID; got != active {
		t.Error("a key that no relying party has seen signed")
	}

	h.clock.Advance(profile.MinNextKeyAge)

	res, err = h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false})
	if err != nil || !res.Promoted {
		t.Fatalf("second manual rotation = %+v, %v", res, err)
	}

	if got := h.mustLogin().KeyID; got == active {
		t.Error("the key did not change at the second rotation")
	}
}

func TestEmergencyRotationReplacesEveryKey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	old := h.mustLogin()
	oldKeys := h.keysOf(h.ns)

	h.refuseBadReasons(len(oldKeys))

	res, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name,
		issuersvc.RotateRequest{Reason: "the store was copied", Emergency: true})
	if err != nil {
		t.Fatalf("emergency rotation: %v", err)
	}

	if len(res.Deleted) != 2 || res.Promoted {
		t.Errorf("result = %+v, want both earlier keys deleted and nothing promoted", res)
	}

	for _, k := range h.keysOf(h.ns) {
		for _, o := range oldKeys {
			if k.ID == o.ID {
				t.Errorf("the earlier key %s (%s) is still live: the next key must not be promoted", o.ID, o.State)
			}
		}
	}

	if _, err = h.verify(old.Value, "sts.amazonaws.com"); err == nil {
		t.Error("a token of an earlier key verifies after an emergency rotation")
	}

	fresh := h.mustLogin()
	if _, err = h.verify(fresh.Value, "sts.amazonaws.com"); err != nil {
		t.Errorf("a new token: %v", err)
	}

	got := h.outbox.types()
	if !slices.Contains(got, notify.IssuerKeyRevoked) || !slices.Contains(got, notify.IssuerKeyNext) {
		t.Errorf("notices = %v, want issuer.key-revoked and issuer.key-next", got)
	}

	if deleted := h.deletedCount(); deleted != 2 {
		t.Errorf("history holds %d deleted keys, want 2", deleted)
	}
}

// refuseBadReasons proves that an emergency rotation with a bad reason is
// refused and changes nothing.
func (h *harness) refuseBadReasons(keyCount int) {
	h.t.Helper()

	for name, reason := range map[string]string{
		"empty": "", "blanks": "   ", "control": "a\x00b", "newline": "a\nb", "501 characters": strings.Repeat("a", 501),
	} {
		_, err := h.svc.Rotate(
			h.t.Context(),
			h.admin,
			h.ns.Name,
			issuersvc.RotateRequest{Reason: reason, Emergency: true},
		)
		wantErr(h.t, "reason "+name, err, profile.ErrInvalid)
	}

	if got := h.keysOf(h.ns); len(got) != keyCount {
		h.t.Fatal("a refused emergency rotation changed the keys")
	}
}

// deletedCount counts the deleted keys in the history of the namespace.
func (h *harness) deletedCount() int {
	h.t.Helper()

	history, err := h.store.Issuer().History(h.t.Context(), h.ns.Name)
	if err != nil {
		h.t.Fatal(err)
	}

	deleted := 0

	for i := range history {
		if history[i].State == profile.KeyDeleted {
			deleted++
		}
	}

	return deleted
}

func TestNoticesNameNoKeyMaterial(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()
	h.clock.Advance(90 * day)
	h.rotateAll()
	h.clock.Advance(83 * day)
	h.rotateAll()

	h.outbox.mu.Lock()
	defer h.outbox.mu.Unlock()

	if len(h.outbox.notes) == 0 {
		t.Fatal("no notice for a new next key")
	}

	text := fmt.Sprintf("%+v", h.outbox.notes)

	for _, k := range h.keysOf(h.ns) {
		if strings.Contains(text, string(k.Sealed)) || strings.Contains(text, string(k.Public)) {
			t.Error("a notice holds a key")
		}
	}

	n := h.outbox.notes[0]
	if n.Type != notify.IssuerKeyNext || n.Attributes["kid"] == "" || n.Attributes["active-at"] == "" ||
		n.Attributes["namespace"] != "acme" {
		t.Errorf("notice = %+v", n)
	}
}

func TestCreateKeys(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	got := h.createKeys()
	if len(got) != 2 || got[0].State == got[1].State {
		t.Fatalf("keys = %+v", got)
	}

	_, err := h.svc.CreateKeys(t.Context(), h.admin, h.ns.Name)
	wantErr(t, "second CreateKeys", err, profile.ErrConflict)

	if n := len(h.keysOf(h.ns)); n != 2 {
		t.Errorf("keys = %d after a refused make", n)
	}
}

func TestKeyMaterialStaysInTheStoreAndOutOfEveryAnswer(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()
	h.clock.Advance(90 * day)
	h.rotateAll()

	h.checkSealedByState()

	st, err := h.svc.Get(t.Context(), h.reader, h.ns.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	raw := mustJSON(t, st)
	jwks := h.jwks(h.ns)

	for _, k := range h.keysOf(h.ns) {
		if bytes.Contains(raw, k.Public) {
			t.Error("the state holds a public key blob")
		}
	}

	for _, banned := range []string{"Sealed", "sealed", "PRIVATE", `"d"`, `"p"`, `"q"`, `"dp"`, `"dq"`, `"qi"`} {
		if bytes.Contains(raw, []byte(banned)) || bytes.Contains(jwks, []byte(banned)) {
			t.Errorf("an answer holds %q", banned)
		}
	}
}

// checkSealedByState checks that only a next and an active key keep a sealed
// private key.
func (h *harness) checkSealedByState() {
	h.t.Helper()

	keys := h.keysOf(h.ns)
	for i := range keys {
		k := &keys[i]

		switch k.State {
		case profile.KeyNext, profile.KeyActive:
			if len(k.Sealed) == 0 || strings.Contains(string(k.Sealed), "BEGIN") {
				h.t.Errorf("%s key %s: sealed = %d bytes", k.State, k.ID, len(k.Sealed))
			}
		case profile.KeyRetired, profile.KeyDeleted:
			if len(k.Sealed) != 0 {
				h.t.Errorf("%s key %s keeps a private key", k.State, k.ID)
			}
		default:
			h.t.Errorf("key %s has the unknown state %q", k.ID, k.State)
		}
	}
}

func TestEveryManagementCallChecksItsAction(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	ctx := t.Context()
	before := h.keysOf(h.ns)

	calls := map[string]func(a *profile.Actor) error{
		"Patch": func(a *profile.Actor) error {
			_, err := h.patch(a, patchAllow(false), 0, nil)

			return err
		},
		"CreateKeys": func(a *profile.Actor) error { _, err := h.svc.CreateKeys(ctx, a, h.ns.Name); return err },
		"Rotate": func(a *profile.Actor) error {
			_, err := h.svc.Rotate(ctx, a, h.ns.Name, issuersvc.RotateRequest{Reason: "x", Emergency: true})

			return err
		},
		"CheckCopy": func(a *profile.Actor) error { _, err := h.svc.CheckCopy(ctx, a, h.ns.Name); return err },
		"Export":    func(a *profile.Actor) error { _, err := h.svc.Export(ctx, a, h.ns.Name); return err },
	}

	for name, call := range calls {
		for who, a := range map[string]*profile.Actor{"a reader": h.reader, "nobody": h.nobody} {
			wantErr(t, name+" by "+who, call(a), identity.ErrDenied)
		}
	}

	_, err := h.svc.Get(ctx, h.nobody, h.ns.Name)
	wantErr(t, "Get by nobody", err, identity.ErrDenied)

	if _, err = h.svc.Get(ctx, h.reader, h.ns.Name); err != nil {
		t.Errorf("a reader cannot read the issuer: %v", err)
	}

	after := h.keysOf(h.ns)
	if !slices.EqualFunc(before, after, func(a, b profile.Key) bool { return a.ID == b.ID && a.State == b.State }) {
		t.Error("a refused call changed the keys")
	}

	set, err := h.store.Issuer().GetIssuerSettings(ctx, h.ns.Name)
	if err != nil {
		t.Fatalf("GetIssuerSettings: %v", err)
	}

	if set.Version != 0 {
		t.Error("a refused call changed the settings")
	}
}

// TestANormalRotationWaitsForTheNextKeyToAge checks the wait after an
// emergency rotation: the new next key is in the JWKS for seconds, and a
// verifier that cached the JWKS has not read it yet. A manual rotation is
// refused with the time, and the schedule promotes nothing, until the key was
// in the JWKS for twice the cache time of the JWKS.
func TestANormalRotationWaitsForTheNextKeyToAge(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	_, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name,
		issuersvc.RotateRequest{Reason: "the store was copied", Emergency: true})
	if err != nil {
		t.Fatalf("emergency rotation: %v", err)
	}

	active := h.mustLogin().KeyID

	for _, wait := range []time.Duration{time.Second, profile.MinNextKeyAge - 2*time.Second} {
		h.clock.Advance(wait)

		_, err = h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false})
		wantErr(t, "rotation before the next key is old enough", err, profile.ErrConflict)

		if !strings.Contains(err.Error(), "can become active after 2026-11-01T12:10:00Z") {
			t.Errorf("the refusal does not name the time: %v", err)
		}

		if got := h.mustLogin().KeyID; got != active {
			t.Error("a key that is not old enough signed")
		}
	}

	h.clock.Advance(2 * time.Second)

	res, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false})
	if err != nil || !res.Promoted {
		t.Fatalf("rotation when the next key is old enough = %+v, %v", res, err)
	}
}

// TestAnEmergencyRotationSaysThatItSkippedTheWait checks that the audit entry
// of an emergency rotation names the skipped wait and the reason.
func TestAnEmergencyRotationSaysThatItSkippedTheWait(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	_, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name,
		issuersvc.RotateRequest{Reason: "the store was copied", Emergency: true})
	if err != nil {
		t.Fatalf("emergency rotation: %v", err)
	}

	list, err := h.store.List(t.Context(), h.ns.Name, audit.Page{Number: 1, Size: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	found := false

	for i := range list.Entries {
		e := &list.Entries[i]
		if e.Reason != "the store was copied" {
			continue
		}

		for _, c := range e.Changes {
			if c.Key == "minNextKeyAge" && strings.HasPrefix(c.New, "skipped:") &&
				strings.Contains(c.New, "emergency") {
				found = true
			}
		}
	}

	if !found {
		t.Errorf("no entry of the emergency rotation names the skipped wait: %+v", list.Entries)
	}
}
