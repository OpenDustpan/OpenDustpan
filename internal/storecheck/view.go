package storecheck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
)

// Dir is the top-level directory that every check writes under. A reaper of
// garbage must never delete under it: the prefix reaper of this package
// deletes it (see blob.StoreCheckKey).
const Dir = blob.StoreCheckDir

// Prefix returns the throwaway prefix of the check id:
// "store-checks/<check ID>/".
func (id ID) Prefix() string {
	return Dir + id.String() + "/"
}

// allow reports whether prefix is exactly the prefix of the check id. The
// list holds one value, so every other prefix fails closed: the empty prefix,
// a prefix of another area, a prefix with a dot segment, and the prefix of
// another check, also when that check is real.
func allow(id ID, prefix string) error {
	// Defense in depth: the start makes the ID and the input check parses it
	// again. This check stays so that a caller of the allowlist alone, for
	// example the prefix reaper, never trusts an ID that it did not parse. It
	// does not rely on the blob.Scoped view or on the reaper exemption.
	if _, err := ParseID(id.String()); err != nil {
		return ErrPrefixRefused
	}

	if prefix != id.Prefix() {
		return ErrPrefixRefused
	}

	return nil
}

// viewOf is the allowlist and the view of one prefix. It is the first and the
// second layer of the containment, in this order. The caller is the service
// or the prefix reaper.
//
// The first layer refuses each prefix that is not the prefix of check id. The
// refusal fails closed and writes a denial entry. The second layer is the
// view of the prefix: it refuses a key outside it with blob.ErrOutOfScope
// before any call to the store.
func (s *Service) viewOf(ctx context.Context, store blob.Store, id ID, prefix string) (*blob.Scoped, error) {
	if err := allow(id, prefix); err != nil {
		s.auditPrefixDenied(ctx, id)

		return nil, err
	}

	// Defense in depth: the allowlist above holds one exact prefix. This view
	// stays so that a key that a case builds from the prefix, with a dot
	// segment or a slash in a name, cannot leave the prefix. It does not rely
	// on the allowlist.
	view, err := blob.NewScopedPrefix(store, prefix)
	if err != nil {
		s.auditPrefixDenied(ctx, id)

		return nil, fmt.Errorf("%w: %w", ErrPrefixRefused, err)
	}

	return view, nil
}

// auditPrefixDenied writes the denial entry of a refused prefix. The entry
// names the check that asked, never the prefix that it asked for: the prefix
// is not trusted and can hold any bytes. A lost entry is logged.
func (s *Service) auditPrefixDenied(ctx context.Context, id ID) {
	object := auditObject
	if _, err := ParseID(id.String()); err == nil {
		object += " " + id.String()
	}

	entry := s.entry(Actor{Subject: systemSubject, TokenID: "", PrincipalKind: ""}, object)
	entry.Kind = audit.KindSecurity
	entry.Outcome = audit.OutcomeDenied
	entry.Reason = "the prefix is not the prefix of the check"

	s.appendAudit(ctx, entry)
}

// systemSubject names the server in the entries that no principal caused.
const systemSubject = "system:store-check"

// appendAudit appends entry to the audit log of the installation. A lost
// entry does not change the result of the check: the server logs the loss.
func (s *Service) appendAudit(ctx context.Context, entry audit.Entry) {
	if _, err := s.audit.AppendServer(ctx, entry); err != nil {
		s.logger.ErrorContext(ctx, "store check audit entry was not written",
			slog.String("error", errorClass(err)))
	}
}

// errorClass names the class of err for a log line. A log line can go to a
// remote sink, and an adapter error can hold an endpoint, so the line never
// holds the text of err.
func errorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, blob.ErrThrottled):
		return "throttled"
	case errors.Is(err, blob.ErrNotFound):
		return "not_found"
	case errors.Is(err, blob.ErrOutOfScope):
		return "out_of_scope"
	default:
		return "error"
	}
}

// Actor is the principal that asked for a check. It goes to the input of the
// operation, so the worker can write the end entry in the name of the
// principal.
type Actor struct {
	Subject       string `json:"subject"`
	TokenID       string `json:"tokenID,omitempty"`
	PrincipalKind string `json:"principalKind,omitempty"`
}
