package forge

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The reasons of a refused event. Each one is a class: it names no hook ID,
// installation, repository or signature.
const (
	EventReasonUnknownHook     = "unknown-hook"
	EventReasonNoRegistration  = "no-registration"
	EventReasonSignature       = "signature"
	EventReasonTooLarge        = "too-large"
	EventReasonMalformed       = "malformed"
	EventReasonUnsupported     = "unsupported"
	EventReasonDuplicate       = "duplicate"
	EventReasonNotAssigned     = "not-assigned"
	EventReasonNotPrimary      = "not-primary"
	EventReasonSecretNotOpened = "secret-not-opened"
	EventReasonReceiptsFull    = "receipts-full"
	EventReasonOther           = "other"
)

// EventRefusal is the error of Service.ReceiveEvent for a delivery that the
// server refused. Reason is one of the EventReason constants.
//
//nolint:errname // EventRefusal is the name that the port contract gives this error.
type EventRefusal struct{ Reason string }

func (e *EventRefusal) Error() string { return "the server refused the event: " + e.Reason }

// refuse returns the refusal with the reason.
func refuse(reason string) error {
	return &EventRefusal{Reason: reason}
}

// verifyReason maps an error of the verifier to a reason. An error that the
// port does not name is the reason other: the service denies what it does not
// know.
func verifyReason(err error) string {
	switch {
	case errors.Is(err, ErrSignature):
		return EventReasonSignature
	case errors.Is(err, ErrInvalid):
		return EventReasonMalformed
	case errors.Is(err, ErrUnsupported):
		return EventReasonUnsupported
	default:
		return EventReasonOther
	}
}

// ReceiveEvent checks and records one delivery of the hook hookID. It returns
// the receipt, or an *EventRefusal whose Reason is one of the EventReason
// constants. It runs these steps and stops at the first refusal: the form and
// the value of the hook ID, the registration, the address digest, the
// signature (the current secret, then the old secret in its window), the kind
// of the event, the installation map and the primary installation of its
// namespace, and the receipt with the rule of duplicates. The signature is the
// credential: the service writes nothing for a delivery that it did not verify.
func (s *Service) ReceiveEvent(ctx context.Context, hookID string, r EventRequest) (EventReceipt, error) {
	// One clock reading decides the window and stamps the receipt.
	now := s.now().UTC()

	// Defense in depth: the handler and the store bound the hook ID too. This
	// check stays so that a caller of the service cannot send a hostile value
	// to the store.
	if !ValidHookID(hookID) {
		return EventReceipt{}, refuse(EventReasonUnknownHook)
	}

	reg, err := s.loadRegistration(ctx, hookID)
	if err != nil {
		return EventReceipt{}, err
	}

	ev, usedOld, err := s.verifyEvent(ctx, &reg, r, now)
	if err != nil {
		return EventReceipt{}, err
	}

	receipt, err := s.mapEvent(ctx, ev)
	if err != nil {
		return EventReceipt{}, err
	}

	sum := sha256.Sum256(r.Body)
	receipt.Kind, receipt.BodyDigest, receipt.ReceivedAt = reg.Kind, hex.EncodeToString(sum[:]), now

	if usedOld {
		receipt.Audit = oldSecretEntry(&reg, now)
	}

	stored, err := s.store.RecordEventReceipt(ctx, receipt)

	switch {
	case errors.Is(err, ErrReceiptsFull):
		return EventReceipt{}, refuse(EventReasonReceiptsFull)
	case err != nil:
		return EventReceipt{}, refuse(EventReasonOther)
	case !stored:
		return EventReceipt{}, refuse(EventReasonDuplicate)
	}

	return receipt, nil
}

// loadRegistration reads the registration and checks that it is the one that
// the hook ID names, that its address digest matches its values and that it
// passes its own checks.
func (s *Service) loadRegistration(ctx context.Context, hookID string) (Registration, error) {
	reg, err := s.store.GetRegistration(ctx, KindGitHub)

	switch {
	case errors.Is(err, ErrNoRegistration):
		return Registration{}, refuse(EventReasonNoRegistration)
	case err != nil:
		return Registration{}, refuse(EventReasonOther)
	}

	// The compare takes the same time for any hook ID of the same length.
	if subtle.ConstantTimeCompare([]byte(hookID), []byte(reg.HookID)) != 1 {
		return Registration{}, refuse(EventReasonUnknownHook)
	}

	// Defense in depth: the store checks the digest when it reads the
	// registration, and OpenBound binds the secret to the digest. This check
	// stays so that a row whose digest field changed alone never gets a
	// secret opened for it. The binding of the open is the digest of the
	// values, not the stored field.
	if reg.AddressDigest != AddressDigest(reg.Kind, reg.BaseURL, reg.AppID) {
		return Registration{}, refuse(EventReasonSecretNotOpened)
	}

	// Defense in depth: the store checks a registration when it reads one. A
	// row that has a partial set of old fields, or another fault, stays out
	// here too, so a store that skips its check cannot open a window.
	if reg.Validate() != nil {
		return Registration{}, refuse(EventReasonOther)
	}

	return reg, nil
}

// verifyEvent checks the signature of r. It opens the current secret and gives
// the verifier that secret alone. Only when that returns ErrSignature, and the
// registration holds a window that is open at now, it opens the old secret and
// gives the verifier that secret alone. It never opens an expired secret. usedOld is true when
// the old secret signed the event.
func (s *Service) verifyEvent(
	ctx context.Context,
	reg *Registration,
	r EventRequest,
	now time.Time,
) (Event, bool, error) {
	opener, ok := s.openers[reg.Kind]
	if !ok {
		return Event{}, false, refuse(EventReasonOther)
	}

	verifier := opener.Events()
	if verifier == nil {
		return Event{}, false, refuse(EventReasonOther)
	}

	digest := AddressDigest(reg.Kind, reg.BaseURL, reg.AppID)

	current, err := s.secrets.OpenBound(ctx, EventSecretScope(reg.Kind), []byte(digest), reg.SealedEventSecret)
	if err != nil {
		return Event{}, false, refuse(EventReasonSecretNotOpened)
	}

	ev, err := verifier.ParseEvent(ctx, r, [][]byte{current})
	if err == nil {
		return ev, false, nil
	}

	if !errors.Is(err, ErrSignature) {
		return Event{}, false, refuse(verifyReason(err))
	}

	// The window check: the end is exclusive, so an event at the end is
	// refused. The reason stays signature for every case that follows.
	if !reg.oldSecretOpen(now) {
		return Event{}, false, refuse(EventReasonSignature)
	}

	old, err := s.secrets.OpenBound(ctx, EventSecretScope(reg.Kind), []byte(digest), reg.OldSealedEventSecret)
	if err != nil {
		return Event{}, false, refuse(EventReasonSecretNotOpened)
	}

	ev, err = verifier.ParseEvent(ctx, r, [][]byte{old})
	if err != nil {
		return Event{}, false, refuse(verifyReason(err))
	}

	return ev, true, nil
}

// mapEvent checks the verified event and returns its receipt. A review event
// needs an installation that the map gives to a namespace of which this
// installation is the primary installation. Any other kind than the two that
// the port names is refused.
func (s *Service) mapEvent(ctx context.Context, ev Event) (EventReceipt, error) {
	receipt := EventReceipt{
		ReceivedAt: time.Time{}, Audit: nil, Kind: "", DeliveryID: ev.DeliveryID, BodyDigest: "",
		InstallationID: "", EventKind: ev.Kind, Repo: "", ReviewID: "",
	}

	if ev.DeliveryID == "" {
		return EventReceipt{}, refuse(EventReasonMalformed)
	}

	switch ev.Kind {
	case EventPing:
		// A ping names no installation, repository or review.
		return receipt, nil
	case EventReview:
		// Handled below.
	default:
		return EventReceipt{}, refuse(EventReasonUnsupported)
	}

	if !ValidNumericID(ev.InstallationID) {
		return EventReceipt{}, refuse(EventReasonMalformed)
	}

	a, err := s.store.GetAssignment(ctx, ev.InstallationID)

	switch {
	case errors.Is(err, ErrAssignmentNotFound):
		return EventReceipt{}, refuse(EventReasonNotAssigned)
	case err != nil:
		return EventReceipt{}, refuse(EventReasonOther)
	}

	_, err = s.namespaces.WritableHere(ctx, a.Namespace)

	switch {
	case errors.Is(err, namespace.ErrNotHome):
		return EventReceipt{}, refuse(EventReasonNotPrimary)
	case errors.Is(err, namespace.ErrNotFound):
		return EventReceipt{}, refuse(EventReasonNotAssigned)
	case err != nil:
		return EventReceipt{}, refuse(EventReasonOther)
	}

	receipt.InstallationID, receipt.Repo, receipt.ReviewID = ev.InstallationID, ev.Repo, ev.ReviewID

	return receipt, nil
}

// oldSecretEntry returns the audit entry of an event that the old secret
// signed. The store writes it in the transaction of the receipt. It holds the
// set ID of the old secret and no secret, hook ID or delivery data.
func oldSecretEntry(reg *Registration, at time.Time) *audit.Entry {
	var caller audit.Entry

	caller.Subject, caller.Channel, caller.PrincipalKind = "webhook", audit.ChannelAPI, audit.PrincipalKindService
	e := serverEntry(caller, at, "git-host-app:event-accepted-old-secret", string(reg.Kind))
	e.Kind = audit.KindSecurity
	e.Reason = "the server accepted an event that the old event secret signed"
	e.Changes = append(e.Changes, change("oldEventSecretSetID", reg.OldEventSecretSetID))

	return &e
}
