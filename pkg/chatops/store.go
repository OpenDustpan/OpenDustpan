package chatops

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Links is the persistence port for chat links. Links are in the server
// scope (audit.ServerScope): each write takes the zero fence, returns
// ErrInvalid for any other fence, and appends the audit entry e in its
// transaction. It returns ErrInvalid for an entry e whose Namespace is not
// audit.ServerScope. A failed write appends nothing and changes nothing.
type Links interface {
	// PutLink stores l. previous is the principal of the link of the same
	// chat user that the person saw on the accept page, or empty when the
	// page showed no link. PutLink never replaces a link silently: it
	// returns ErrConflict when previous is empty and the chat user has a
	// link, and when previous is not empty and the stored link is missing
	// or names another principal. It returns ErrInvalid for a link that
	// fails Link.Validate.
	PutLink(ctx context.Context, fence namespace.Fence, l Link, previous string, e audit.Entry) error
	// DeleteLink deletes the link of the chat user u. It returns
	// ErrNotFound for a chat user with no link. The intents of u are rows
	// of the namespaces of their jobs, so DeleteLink does not delete them:
	// the server then deletes each intent of u with no use through
	// Intents.IntentsOf and Intents.DeleteIntent, with the fence of its
	// namespace. Until then CheckIntent refuses them, because the link is
	// gone.
	DeleteLink(ctx context.Context, fence namespace.Fence, u ChatUser, e audit.Entry) error
	// LinkOf returns the link of the chat user u, or ErrNotFound. The IDs
	// match exactly.
	LinkOf(ctx context.Context, u ChatUser) (Link, error)
	// LinksOf returns the links of the principal, so the person can see
	// and delete them.
	LinksOf(ctx context.Context, principal string) ([]Link, error)
}

// LinkRequests is the persistence port for the one-time link requests.
// Link requests are in the server scope: each write takes the zero fence
// and returns ErrInvalid for any other fence. A link request changes no
// access, so its writes append no audit entry. The link that it makes
// appends one, through Links.PutLink.
type LinkRequests interface {
	// CreateLinkRequest stores r. It returns ErrInvalid for a request that
	// fails LinkRequest.Validate or that has a use, and ErrConflict for a
	// request with the hash of a stored one.
	CreateLinkRequest(ctx context.Context, fence namespace.Fence, r LinkRequest) error
	// GetLinkRequest returns the link request with the token hash
	// tokenSHA256, or ErrNotFound.
	GetLinkRequest(ctx context.Context, tokenSHA256 string) (LinkRequest, error)
	// UseLinkRequest sets the use of the link request with the token hash
	// tokenSHA256 to now and principal, once, in one transaction with a
	// check that it has none, and returns the request with its use. It
	// returns ErrInvalid for an empty principal, ErrNotFound, ErrUsed, and
	// ErrExpired after its expiry. The use comes before the link, so one
	// token never makes two links.
	UseLinkRequest(ctx context.Context, fence namespace.Fence, tokenSHA256, principal string, now time.Time) (
		LinkRequest, error)
}

// Intents is the persistence port for pending approvals from chat. An
// intent is in the namespace of its job: each write takes the fence of
// that namespace and checks it in its transaction.
type Intents interface {
	// CreateIntent stores in and appends the audit entry e in its
	// transaction. It returns ErrInvalid for an intent that fails
	// Intent.Validate, for an intent with a use, for an entry e whose
	// Namespace is not the namespace of in, and for a fence of another
	// namespace or the zero fence, namespace.ErrFenced for a
	// stale fence, and ErrConflict for an ID that exists. It never
	// replaces an intent.
	CreateIntent(ctx context.Context, fence namespace.Fence, in Intent, e audit.Entry) error
	// GetIntent returns the intent id, or ErrNotFound.
	GetIntent(ctx context.Context, id string) (Intent, error)
	// UseIntent sets the use of the intent id to now, once, in one
	// transaction with a check that it has none. It returns ErrNotFound
	// for an unknown intent and for a fence of another namespace or the
	// zero fence, namespace.ErrFenced for a stale fence, ErrUsed, and
	// ErrExpired after its expiry. The approval of the job and the use are
	// not in one transaction: the use comes first, so a second request can
	// never approve twice.
	UseIntent(ctx context.Context, fence namespace.Fence, id string, now time.Time) error
	// IntentsOf returns the intents whose request names the chat user u,
	// in every namespace. The IDs match exactly.
	IntentsOf(ctx context.Context, u ChatUser) ([]Intent, error)
	// DeleteIntent deletes the intent id, which has no use. It returns
	// ErrNotFound for an unknown intent and for a fence of another
	// namespace or the zero fence, namespace.ErrFenced for a stale fence,
	// and ErrUsed for an intent with a use: the use stays as a record.
	// The delete of the link of the chat user appends the audit entry, so
	// DeleteIntent appends none.
	DeleteIntent(ctx context.Context, fence namespace.Fence, id string) error
}
