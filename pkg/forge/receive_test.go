package forge_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
)

// events returns the fake verifier of the rig.
func (r *rig) events() *fakeEvents {
	fe, ok := r.opener.events.(*fakeEvents)
	if !ok {
		panic("the verifier of the rig is not a fakeEvents")
	}

	return fe
}

// request returns a delivery that the fake verifier accepts for secret.
func request(secret string, extra ...string) forge.EventRequest {
	h := map[string][]string{"Sig": {secret}}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = []string{extra[i+1]}
	}

	return forge.EventRequest{Header: h, Body: []byte(`{"body":"` + secret + strings.Join(extra, ",") + `"}`)}
}

// intake registers the app with the canary secret, assigns installation 101 to
// acme and returns the hook ID.
func intake(t *testing.T) (*rig, string) {
	t.Helper()

	r := newRig(t)
	view := r.register(t)
	r.assign(t, "101", "acme")

	return r, view.HookID
}

// wantRefusal checks that err is an EventRefusal with the reason.
func wantRefusal(t *testing.T, err error, reason string) {
	t.Helper()

	var ref *forge.EventRefusal
	if !errors.As(err, &ref) || ref.Reason != reason {
		t.Fatalf("error = %v, want the refusal %q", err, reason)
	}
}

// rotated registers with the canary secret, assigns installation 101, and puts
// secretB. The canary is the old secret.
func rotated(t *testing.T) (*rig, string, forge.Registration) {
	t.Helper()

	r, hook := intake(t)

	if _, err := r.putSecret(t, secretB); err != nil {
		t.Fatalf("put B: %v", err)
	}

	reg := r.registration(t)

	return r, hook, reg
}

func TestReceiveEventAReviewEventIsRecordedAsAHint(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)
	req := request(secretCanary)

	got, err := r.svc.ReceiveEvent(t.Context(), hook, req)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	sum := sha256.Sum256(req.Body)

	want := forge.EventReceipt{
		ReceivedAt: time.UnixMilli(r.clock.Load()).UTC(), Audit: nil, Kind: forge.KindGitHub, DeliveryID: "delivery-1",
		BodyDigest: hex.EncodeToString(sum[:]), InstallationID: "101", EventKind: forge.EventReview,
		Repo: "acme/app", ReviewID: "7",
	}
	if got != want {
		t.Fatalf("receipt = %+v, want %+v", got, want)
	}

	if len(r.store.receipts) != 1 {
		t.Fatalf("stored receipts = %d, want 1", len(r.store.receipts))
	}

	if calls := r.events().secretsOfCalls(); len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != secretCanary {
		t.Fatalf("verifier calls = %v, want one call with the current secret alone", calls)
	}
}

func TestReceiveEventAPingNeedsNoInstallation(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	view := r.register(t)

	got, err := r.svc.ReceiveEvent(t.Context(), view.HookID, request(secretCanary, "Kind", "ping"))
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if got.EventKind != forge.EventPing || got.InstallationID != "" || got.Repo != "" || got.ReviewID != "" {
		t.Fatalf("receipt = %+v", got)
	}
}

func TestReceiveEventAHookIDOfABadFormIs(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)
	reads := r.store.getRegs

	for _, id := range []string{
		"", hook[:31], hook + "a", strings.ToUpper(hook), "../x", strings.Repeat("a", 4096), hook[:31] + "\x00",
	} {
		_, err := r.svc.ReceiveEvent(t.Context(), id, request(secretCanary))
		wantRefusal(t, err, forge.EventReasonUnknownHook)
	}

	if r.store.getRegs != reads || len(r.events().secretsOfCalls()) != 0 || r.store.receiptCalls != 0 {
		t.Fatal("a bad hook ID reached the store or the verifier")
	}
}

func TestReceiveEventAnotherValidHookIDIsRefused(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	other := []byte(hook)
	if other[0] == 'a' {
		other[0] = 'b'
	} else {
		other[0] = 'a'
	}

	_, err := r.svc.ReceiveEvent(t.Context(), string(other), request(secretCanary))
	wantRefusal(t, err, forge.EventReasonUnknownHook)

	if len(r.events().secretsOfCalls()) != 0 {
		t.Fatal("the verifier ran for a wrong hook ID")
	}
}

func TestReceiveEventNoRegistration(t *testing.T) {
	t.Parallel()
	r := newRig(t)

	_, err := r.svc.ReceiveEvent(t.Context(), forge.NewHookID(), request(secretCanary))
	wantRefusal(t, err, forge.EventReasonNoRegistration)
}

func TestReceiveEventARegistrationThatFailsItsChecksIsRefused(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	// The SQL store refuses such a row with ErrInvalid. The service checks
	// the row again.
	bad := r.registration(t)
	bad.OldEventSecretEndsAt = time.UnixMilli(r.clock.Load()).Add(time.Hour)
	r.store.setRegistration(bad)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonOther)

	r.store.setRegistration(r.registration(t))
	r.store.getRegErr = forge.ErrInvalid

	_, err = r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonOther)
}

func TestReceiveEventAChangedAddressDigestIsRefused(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	bad := r.registration(t)
	bad.AddressDigest = strings.Repeat("0", 64)
	r.store.setRegistration(bad)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonSecretNotOpened)

	if len(r.events().secretsOfCalls()) != 0 {
		t.Fatal("the verifier ran with a secret of a changed address")
	}
}

func TestReceiveEventASealedSecretThatDoesNotOpenIs(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	bad := r.registration(t)
	bad.SealedEventSecret = []byte("garbage")
	r.store.setRegistration(bad)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonSecretNotOpened)
}

func TestReceiveEventAWrongSignature(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretB))
	wantRefusal(t, err, forge.EventReasonSignature)

	if r.store.receiptCalls != 0 {
		t.Fatal("a refused event reached the receipt store")
	}
}

func TestReceiveEventTheVerifierErrorsMapToTheirReason(t *testing.T) {
	t.Parallel()
	rows := []struct {
		err    error
		reason string
	}{
		{forge.ErrInvalid, forge.EventReasonMalformed},
		{forge.ErrUnsupported, forge.EventReasonUnsupported},
		{forge.ErrSignature, forge.EventReasonSignature},
		{errBoom, forge.EventReasonOther},
	}

	for _, row := range rows {
		r, hook := intake(t)
		r.events().resultErr = row.err

		_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
		wantRefusal(t, err, row.reason)
	}
}

func TestReceiveEventAnEventKindThatThePortDoesNot(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"", "push", "REVIEW"} {
		r, hook := intake(t)
		r.events().result = &forge.Event{
			DeliveryID: "d", Kind: forge.EventKind(kind), Repo: "acme/app", ReviewID: "7", InstallationID: "101",
			Sender: noAccount(),
		}

		_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
		wantRefusal(t, err, forge.EventReasonUnsupported)

		if r.store.receiptCalls != 0 {
			t.Fatalf("kind %q reached the receipt store", kind)
		}
	}
}

func TestReceiveEventAnEventWithNoDeliveryIDIsMalformed(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)
	r.events().result = &forge.Event{
		DeliveryID: "", Kind: forge.EventReview, Repo: "acme/app", ReviewID: "7", InstallationID: "101",
		Sender: noAccount(),
	}

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonMalformed)
}

func TestReceiveEventNoVerifierFailsClosed(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)
	r.opener.events = nil

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonOther)
}

func TestReceiveEventAReviewEventOfAnInstallationThatIs(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary, "Install", "999"))
	wantRefusal(t, err, forge.EventReasonNotAssigned)

	if r.store.receiptCalls != 0 {
		t.Fatal("an unassigned installation reached the receipt store")
	}
}

func TestReceiveEventAnInstallationIDOfABadFormIs(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary, "Install", "../1"))
	wantRefusal(t, err, forge.EventReasonMalformed)
}

func TestReceiveEventAnAssignmentToANamespaceThatDoesNot(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)
	delete(r.ns, "acme")

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonNotAssigned)
}

func TestReceiveEventANamespaceThatAnotherInstallationOwns(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	n := r.ns["acme"]
	n.PrimaryInstallation = "us-east"
	r.ns["acme"] = n

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonNotPrimary)

	if r.store.receiptCalls != 0 || len(r.store.receipts) != 0 {
		t.Fatal("an event of a foreign namespace left a receipt")
	}
}

func TestReceiveEventTheSameDeliveryTwiceAndTheSameBody(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	if _, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary)); err != nil {
		t.Fatalf("first: %v", err)
	}

	// The second request has another body, the same delivery ID.
	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary, "X", "y"))
	wantRefusal(t, err, forge.EventReasonDuplicate)

	// The third has the first body and a new delivery ID.
	replay := request(secretCanary)
	replay.Header["Delivery"] = []string{"delivery-2"}

	_, err = r.svc.ReceiveEvent(t.Context(), hook, replay)
	wantRefusal(t, err, forge.EventReasonDuplicate)

	if len(r.store.receipts) != 1 {
		t.Fatalf("receipts = %d, want 1", len(r.store.receipts))
	}
}

func TestReceiveEventATableAtItsBound(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)
	r.store.receiptsFull = true

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonReceiptsFull)
}

func TestReceiveEventAnyStoreErrorIsOther(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)
	r.store.receiptErr = errBoom

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonOther)

	r, hook = intake(t)
	r.store.failAssignment = true

	_, err = r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonOther)

	r, hook = intake(t)
	r.store.getRegErr = errBoom

	_, err = r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonOther)
}

func TestReceiveEventARefusalAndTheErrorTextHoldNo(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretB))
	if err == nil || strings.Contains(err.Error(), secretB) || strings.Contains(err.Error(), hook) ||
		strings.Contains(err.Error(), secretCanary) {
		t.Fatalf("error = %v", err)
	}
}

func TestReceiveEventOldSecretWindowTheCurrentSecretIsCheckedAloneAn(t *testing.T) {
	t.Parallel()
	r, hook, _ := rotated(t)

	got, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretB))
	if err != nil || got.Audit != nil {
		t.Fatalf("receive = %+v, %v", got, err)
	}

	if calls := r.events().secretsOfCalls(); len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != secretB {
		t.Fatalf("verifier calls = %v", calls)
	}
}

func TestReceiveEventOldSecretWindow1MsBeforeTheEndTheOldSecret(t *testing.T) {
	t.Parallel()
	r, hook, reg := rotated(t)
	r.clock.Store(reg.OldEventSecretEndsAt.UnixMilli() - 1)

	got, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if got.Audit == nil {
		t.Fatal("the receipt has no audit entry for the old secret")
	}

	e := got.Audit
	if e.Action != "git-host-app:event-accepted-old-secret" || e.Namespace != audit.ServerScope ||
		e.ObjectID != string(forge.KindGitHub) || e.Kind != audit.KindSecurity || e.At.IsZero() {
		t.Fatalf("entry = %+v", e)
	}

	text := entryText(e)

	if strings.Contains(text, secretCanary) || strings.Contains(text, hook) {
		t.Fatal("the entry holds a secret or the hook ID")
	}

	if calls := r.events().secretsOfCalls(); fmt.Sprint(calls) != fmt.Sprint([][]string{{secretB}, {secretCanary}}) {
		t.Fatalf("verifier calls = %v, want the current secret, then the old one, each alone", calls)
	}

	if n := len(r.store.audits); r.store.audits[n-1].Action != "git-host-app:event-accepted-old-secret" {
		t.Fatal("the store did not write the entry with the receipt")
	}
}

func TestReceiveEventOldSecretWindowAtTheEndAndAfterTheEndThe(t *testing.T) {
	t.Parallel()
	for _, delta := range []int64{0, 1, 3600_000} {
		r, hook, reg := rotated(t)
		r.clock.Store(reg.OldEventSecretEndsAt.UnixMilli() + delta)

		_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
		wantRefusal(t, err, forge.EventReasonSignature)

		// The service does not open an expired secret.
		if calls := r.events().secretsOfCalls(); len(calls) != 1 {
			t.Fatalf("delta %d: verifier calls = %v, want only the current secret", delta, calls)
		}

		if r.store.receiptCalls != 0 {
			t.Fatalf("delta %d: a refused event reached the receipt store", delta)
		}
	}
}

func TestReceiveEventOldSecretWindowAfterTheConfirmationTheOldSecret(t *testing.T) {
	t.Parallel()
	r, hook, reg := rotated(t)

	if _, err := r.svc.ConfirmRotation(t.Context(), callerEntry(), forge.KindGitHub, reg.Version); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonSignature)
}

func TestReceiveEventOldSecretWindowAThirdSecretIsRefused(t *testing.T) {
	t.Parallel()
	r, hook, _ := rotated(t)

	if _, err := r.putSecret(t, secretC); err != nil {
		t.Fatalf("put C: %v", err)
	}

	// The canary was current before B. Now B is old and the canary is gone.
	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonSignature)

	if _, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretB, "Delivery", "d-2")); err != nil {
		t.Fatalf("the old secret B: %v", err)
	}
}

func TestReceiveEventOldSecretWindowNULLOldFieldsRefuseTheOldSecret(t *testing.T) {
	t.Parallel()
	r, hook := intake(t)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretB))
	wantRefusal(t, err, forge.EventReasonSignature)

	if calls := r.events().secretsOfCalls(); len(calls) != 1 {
		t.Fatalf("verifier calls = %v, want one", calls)
	}
}

func TestReceiveEventOldSecretWindowAPartialOldSetIsRefusedAsOther(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*forge.Registration){
		"no sealed": func(g *forge.Registration) { g.OldSealedEventSecret = nil },
		"no end":    func(g *forge.Registration) { g.OldEventSecretEndsAt = time.Time{} },
		"no set":    func(g *forge.Registration) { g.OldEventSecretSetID = "" },
	} {
		r, hook, reg := rotated(t)
		mutate(&reg)
		r.store.setRegistration(reg)

		_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
		wantRefusal(t, err, forge.EventReasonOther)

		if r.store.receiptCalls != 0 {
			t.Fatalf("%s: a refused event reached the receipt store", name)
		}
	}
}

func TestReceiveEventOldSecretWindowAnOldSecretThatDoesNotOpenIs(t *testing.T) {
	t.Parallel()
	r, hook, reg := rotated(t)
	reg.OldSealedEventSecret = []byte("garbage")
	r.store.setRegistration(reg)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary))
	wantRefusal(t, err, forge.EventReasonSecretNotOpened)
}

func TestReceiveEventOldSecretWindowTheOldSecretInAWindowStillObeys(t *testing.T) {
	t.Parallel()
	r, hook, _ := rotated(t)

	_, err := r.svc.ReceiveEvent(t.Context(), hook, request(secretCanary, "Install", "999"))
	wantRefusal(t, err, forge.EventReasonNotAssigned)

	if len(r.store.audits) != 0 &&
		r.store.audits[len(r.store.audits)-1].Action == "git-host-app:event-accepted-old-secret" {
		t.Fatal("an event that was refused later left an old-secret entry")
	}
}

// entryText joins the text fields of e that a secret could reach.
func entryText(e *audit.Entry) string {
	parts := make([]string, 0, 3+2*len(e.Changes))
	parts = append(parts, e.Reason, e.Action, e.ObjectID)

	for _, c := range e.Changes {
		parts = append(parts, c.Key, c.New)
	}

	return strings.Join(parts, " ")
}
