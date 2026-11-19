package forge_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
)

const (
	secretB = "SECONDEVENT-0123456789abcdef0123456789"
	secretC = "THIRDEVENT-0123456789abcdef0123456789"
	week    = 7 * 24 * time.Hour
)

// putSecret puts the registration with the event secret s.
func (r *rig) putSecret(t *testing.T, s string) (forge.RegistrationView, error) {
	t.Helper()

	in := registrationInput()
	in.EventSecret = []byte(s)

	return r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitHub, in, 0)
}

// openOld opens the old sealed event secret of reg as the service seals it.
func (r *rig) openOld(t *testing.T, reg *forge.Registration) string {
	t.Helper()

	plain, err := r.sealer.OpenBound(
		t.Context(), forge.EventSecretScope(forge.KindGitHub), []byte(reg.AddressDigest), reg.OldSealedEventSecret,
	)
	if err != nil {
		t.Fatalf("open the old secret: %v", err)
	}

	return string(plain)
}

func TestRegistrationValidateOldFieldsAllOrNone(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.register(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put: %v", err)
	}

	full := r.registration(t)

	if err := full.Validate(); err != nil {
		t.Fatalf("a full old set: %v", err)
	}

	cases := map[string]func(*forge.Registration){
		"only the end":         func(g *forge.Registration) { g.OldEventSecretSetID, g.OldSealedEventSecret = "", nil },
		"only the set ID":      func(g *forge.Registration) { g.OldSealedEventSecret, g.OldEventSecretEndsAt = nil, time.Time{} },
		"only the sealed":      func(g *forge.Registration) { g.OldEventSecretSetID, g.OldEventSecretEndsAt = "", time.Time{} },
		"no sealed":            func(g *forge.Registration) { g.OldSealedEventSecret = nil },
		"no end":               func(g *forge.Registration) { g.OldEventSecretEndsAt = time.Time{} },
		"no set ID":            func(g *forge.Registration) { g.OldEventSecretSetID = "" },
		"a set ID of bad form": func(g *forge.Registration) { g.OldEventSecretSetID = "x\r\ny" },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := full
			mutate(&reg)

			if err := reg.Validate(); !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}
		})
	}

	none := full
	none.OldEventSecretSetID, none.OldSealedEventSecret, none.OldEventSecretEndsAt = "", nil, time.Time{}

	if err := none.Validate(); err != nil {
		t.Fatalf("no old set: %v", err)
	}
}

func TestPutRegistrationRotatesTheEventSecretAFirstRegistrationHasNoO(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	view := r.register(t)
	reg := r.registration(t)

	if reg.OldEventSecretSetID != "" || reg.OldSealedEventSecret != nil || !reg.OldEventSecretEndsAt.IsZero() {
		t.Fatalf("old fields = %+v, want none", reg)
	}

	if !view.OldEventSecretEndsAt.IsZero() {
		t.Fatal("the view shows a window for a first registration")
	}
}

func TestPutRegistrationRotatesTheEventSecretADifferentSecretMovesThe(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)
	first := r.registration(t)
	now := time.UnixMilli(r.clock.Load())

	view, err := r.putSecret(t, secretB)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	reg := r.registration(t)

	if reg.OldEventSecretSetID != first.EventSecretSetID {
		t.Fatalf("old set ID = %q, want %q", reg.OldEventSecretSetID, first.EventSecretSetID)
	}

	if !reg.OldEventSecretEndsAt.Equal(now.Add(week)) {
		t.Fatalf("ends at %v, want %v", reg.OldEventSecretEndsAt, now.Add(week))
	}

	if got := r.openOld(t, &reg); got != secretCanary {
		t.Fatal("the old fields do not hold the former secret")
	}

	if !view.OldEventSecretEndsAt.Equal(now.Add(week)) {
		t.Fatalf("view end = %v, want %v", view.OldEventSecretEndsAt, now.Add(week))
	}

	if reg.EventSecretSetID == first.EventSecretSetID {
		t.Fatal("the current secret kept its set ID")
	}
}

func TestPutRegistrationRotatesTheEventSecretTheSameSecretKeepsTheOld(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put B: %v", err)
	}

	before := r.registration(t)

	r.advance(time.Hour)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put B again: %v", err)
	}

	after := r.registration(t)

	if after.OldEventSecretSetID != before.OldEventSecretSetID ||
		!after.OldEventSecretEndsAt.Equal(before.OldEventSecretEndsAt) ||
		!bytes.Equal(after.OldSealedEventSecret, before.OldSealedEventSecret) {
		t.Fatal("the same secret changed the old fields")
	}
}

func TestPutRegistrationRotatesTheEventSecretAnEndedWindowIsClearedBy(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put B: %v", err)
	}

	r.advance(week)

	view, err := r.putSecret(t, secretB)
	if err != nil {
		t.Fatalf("put B again: %v", err)
	}

	reg := r.registration(t)
	if reg.OldEventSecretSetID != "" || reg.OldSealedEventSecret != nil || !reg.OldEventSecretEndsAt.IsZero() {
		t.Fatalf("an ended window stayed: %+v", reg)
	}

	if !view.OldEventSecretEndsAt.IsZero() {
		t.Fatal("the view shows an ended window")
	}
}

func TestPutRegistrationRotatesTheEventSecretAThirdSecretReplacesTheO(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put B: %v", err)
	}

	r.advance(time.Hour)

	if _, err := r.putSecret(t, secretC); err != nil {
		t.Fatalf("put C: %v", err)
	}

	reg := r.registration(t)

	if got := r.openOld(t, &reg); got != secretB {
		t.Fatal("old secret is not the one that was current (got the first or the third)")
	}

	if !reg.OldEventSecretEndsAt.Equal(time.UnixMilli(r.clock.Load()).Add(week)) {
		t.Fatalf("the window did not restart: %v", reg.OldEventSecretEndsAt)
	}
}

func TestPutRegistrationRotatesTheEventSecretAnAddressChangeEndsTheWi(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put B: %v", err)
	}

	in := registrationInput()
	in.EventSecret = []byte(secretC)
	in.BaseURL = "https://ghe.example.com/api/v3"

	if _, err := r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitHub, in, 0); err != nil {
		t.Fatalf("put: %v", err)
	}

	reg := r.registration(t)
	if reg.OldEventSecretSetID != "" || reg.OldSealedEventSecret != nil || !reg.OldEventSecretEndsAt.IsZero() {
		t.Fatalf("the old secret survived an address change: %+v", reg)
	}
}

func TestPutRegistrationRotatesTheEventSecretASecretThatDoesNotOpenL(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	bad := r.registration(t)
	bad.SealedEventSecret = []byte("garbage")
	r.store.setRegistration(bad)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put: %v", err)
	}

	reg := r.registration(t)
	if reg.OldEventSecretSetID != "" || reg.OldSealedEventSecret != nil || !reg.OldEventSecretEndsAt.IsZero() {
		t.Fatalf("an unopened secret was moved: %+v", reg)
	}
}

func TestPutRegistrationRotatesTheEventSecretTheWriteCarriesTheVersion(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)
	read := r.registration(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put: %v", err)
	}

	got := r.store.putIfVersions
	if got[len(got)-1] != read.Version {
		t.Fatalf("ifVersion = %v, want the read version %d", got, read.Version)
	}
}

func TestPutRegistrationRotatesTheEventSecretAConcurrentPutReturnsErrV(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put B: %v", err)
	}

	before := r.registration(t)

	// Another put lands between the read and the write of this one.
	r.store.afterGetReg = func() {
		r.store.afterGetReg = nil

		other := before
		other.Version++
		r.store.setRegistration(other)
	}

	_, err := r.putSecret(t, secretC)
	if !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("put = %v, want ErrVersionConflict", err)
	}

	after := r.registration(t)
	if !bytes.Equal(after.SealedEventSecret, before.SealedEventSecret) ||
		!bytes.Equal(after.OldSealedEventSecret, before.OldSealedEventSecret) {
		t.Fatal("a failed put changed a secret")
	}
}

func TestPutRegistrationRotatesTheEventSecretACallerVersionOtherThanT(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	in := registrationInput()
	in.EventSecret = []byte(secretB)

	_, err := r.svc.PutRegistration(t.Context(), callerEntry(), forge.KindGitHub, in, 5)
	if !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("put = %v, want ErrVersionConflict", err)
	}
}

func TestPutRegistrationRotatesTheEventSecretAReadErrorOtherThanNoRe(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)
	r.store.getRegErr = errBoom

	if _, err := r.putSecret(t, secretB); !errors.Is(err, errBoom) {
		t.Fatalf("put = %v, want the store error", err)
	}
}

func TestPutRegistrationRotatesTheEventSecretTheAuditEntryAndTheView(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	view, err := r.putSecret(t, secretB)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	text := secretCanary + "|" + secretB
	for _, e := range r.store.audits {
		for _, c := range e.Changes {
			if v := c.New; len(v) > 8 && strings.Contains(text, v) {
				t.Fatalf("the audit entry holds a secret: %v", c)
			}
		}
	}

	if view.EventSecret.ID == "" {
		t.Fatal("the view lost the set ID")
	}
}

func TestConfirmRotationItClearsTheFieldsAndWritesAnAudit(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put: %v", err)
	}

	before := r.registration(t)
	audits := len(r.store.audits)

	view, err := r.svc.ConfirmRotation(t.Context(), callerEntry(), forge.KindGitHub, before.Version)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if !view.OldEventSecretEndsAt.IsZero() || view.Version != before.Version+1 {
		t.Fatalf("view = %+v", view)
	}

	if reg := r.registration(t); hasOldFields(&reg) {
		t.Fatalf("old fields stayed: %+v", reg)
	}

	if len(r.store.audits) != audits+1 {
		t.Fatalf("audit entries = %d, want %d", len(r.store.audits), audits+1)
	}

	e := r.store.audits[len(r.store.audits)-1]
	if e.Action != "git-host-app:confirm-rotation" || e.Subject != "alice" || e.ObjectID != "github" ||
		e.Namespace != audit.ServerScope || e.At.IsZero() {
		t.Fatalf("entry = %+v", e)
	}
}

func TestConfirmRotationNoWindowGivesErrNoRotation(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)
	audits := len(r.store.audits)

	_, err := r.svc.ConfirmRotation(t.Context(), callerEntry(), forge.KindGitHub, 0)
	if !errors.Is(err, forge.ErrNoRotation) {
		t.Fatalf("confirm = %v, want ErrNoRotation", err)
	}

	if len(r.store.audits) != audits {
		t.Fatal("a refused confirm wrote an entry")
	}
}

func TestConfirmRotationAStaleVersionGivesErrVersionConflict(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put: %v", err)
	}

	_, err := r.svc.ConfirmRotation(t.Context(), callerEntry(), forge.KindGitHub, 1)
	if !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("confirm = %v, want ErrVersionConflict", err)
	}

	if r.registration(t).OldEventSecretEndsAt.IsZero() {
		t.Fatal("a refused confirm cleared the window")
	}
}

func TestConfirmRotationNoRegistrationGivesErrNoRegistration(t *testing.T) {
	t.Parallel()
	r := newRig(t)

	_, err := r.svc.ConfirmRotation(t.Context(), callerEntry(), forge.KindGitHub, 0)
	if !errors.Is(err, forge.ErrNoRegistration) {
		t.Fatalf("confirm = %v, want ErrNoRegistration", err)
	}
}

func TestConfirmRotationAnUnsupportedKindIsRefusedBeforeTheStore(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.register(t)

	_, err := r.svc.ConfirmRotation(t.Context(), callerEntry(), forge.Kind("gitlab"), 0)
	if !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("confirm = %v, want ErrInvalid", err)
	}
}

// hasOldFields reports whether any old event secret field of reg is set.
func hasOldFields(reg *forge.Registration) bool {
	return reg.OldEventSecretSetID != "" || reg.OldSealedEventSecret != nil || !reg.OldEventSecretEndsAt.IsZero()
}

func TestRegistrationViewShowsNoWindowForAPartialOldSet(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.register(t)

	reg := r.registration(t)
	reg.OldEventSecretEndsAt = time.UnixMilli(r.clock.Load()).Add(time.Hour)
	r.store.setRegistration(reg)

	view, err := r.svc.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !view.OldEventSecretEndsAt.IsZero() {
		t.Fatal("the view shows a window for a partial old set")
	}
}
