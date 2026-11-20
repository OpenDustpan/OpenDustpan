package forge

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// EventReceipt is a verified delivery of a forge event: the hint. A later
// consumer reads it, and reads the object through the API before it changes a
// record.
type EventReceipt struct {
	ReceivedAt time.Time
	// Audit is written in the same transaction as the receipt when it is not
	// nil.
	Audit          *audit.Entry
	Kind           Kind
	DeliveryID     string
	BodyDigest     string // hex SHA-256 of the body
	InstallationID string // empty for a ping
	EventKind      EventKind
	Repo           string
	ReviewID       string
}

// ReceiptStore keeps the event receipts of the last 24 hours.
type ReceiptStore interface {
	// RecordEventReceipt deletes the receipts older than 24 hours, then
	// stores r, in one transaction. It returns false, with no write, when
	// r.DeliveryID or r.BodyDigest is already stored for r.Kind. It returns
	// ErrInvalid for a receipt that fails its checks and ErrReceiptsFull at
	// the bound.
	RecordEventReceipt(ctx context.Context, r EventReceipt) (bool, error)
}
