package chatops

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// Links is the persistence port for chat links. Links are in the server
// scope: each write appends the audit entry e in its transaction.
type Links interface {
	// PutLink stores l, and replaces the link of the same chat user. It
	// returns ErrInvalid for a link with an empty field.
	PutLink(ctx context.Context, l Link, e audit.Entry) error
	// DeleteLink deletes the link of the chat user. It returns ErrNotFound.
	DeleteLink(ctx context.Context, platform, workspace, user string, e audit.Entry) error
	// LinkOf returns the link of the chat user, or ErrNotFound.
	LinkOf(ctx context.Context, platform, workspace, user string) (Link, error)
	// LinksOf returns the links of the principal, so the person can see
	// and delete them.
	LinksOf(ctx context.Context, principal string) ([]Link, error)
}

// Intents is the persistence port for pending approvals from chat.
type Intents interface {
	// CreateIntent stores in. It returns ErrInvalid for an intent with no
	// ID or no principal.
	CreateIntent(ctx context.Context, in Intent) error
	// GetIntent returns the intent id, or ErrNotFound.
	GetIntent(ctx context.Context, id string) (Intent, error)
	// UseIntent sets the use of the intent id to now, once, in one
	// transaction with a check that it has none. It returns ErrNotFound,
	// ErrIntentUsed, and ErrIntentExpired after its expiry. The approval
	// of the job and the use are not in one transaction: the use comes
	// first, so a second request can never approve twice.
	UseIntent(ctx context.Context, id string, now time.Time) error
}
