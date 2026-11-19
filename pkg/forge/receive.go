package forge

import (
	"context"
	"errors"
	"opendustpan.org/opendustpan/pkg/audit"
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
type EventRefusal struct{ Reason string }

func (e *EventRefusal) Error() string { return "the server refused the event: " + e.Reason }

// ReceiveEvent checks and records one delivery of the hook hookID. It returns
// the receipt, or an *EventRefusal.
func (s *Service) ReceiveEvent(context.Context, string, EventRequest) (EventReceipt, error) {
	return EventReceipt{}, errors.New("not built yet")
}

// ConfirmRotation ends the old event secret at once. It returns
// ErrNoRegistration, ErrVersionConflict and ErrNoRotation.
func (s *Service) ConfirmRotation(context.Context, audit.Entry, Kind, int64) (RegistrationView, error) {
	return RegistrationView{}, errors.New("not built yet")
}
