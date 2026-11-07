package runnerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/faultlog"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

const (
	// unverifiedSubject is the subject of the entry of a refused handoff token
	// when no runner proved its key: a launcher has no identity before its
	// session opens.
	unverifiedSubject = "unverified-launcher"
	// handoffAuditTimeout bounds the append of the entry of one refusal. The
	// append runs after the request context is done too, so a client that
	// closes the connection cannot make an entry get lost.
	handoffAuditTimeout = 5 * time.Second
)

// recordUnverifiedHandoffRefusal writes the entry of a refused exchange of a
// handoff token, when err is a refusal of a token or a claim. The request names
// no runner before the session opens, so the entry holds no claimed runner ID
// and lands in the installation scope.
func (h *Handler) recordUnverifiedHandoffRefusal(ctx context.Context, r *http.Request, err error) {
	ref, ok := runner.NewHandoffRefusal(err, "")
	if !ok {
		return
	}

	h.recordHandoffRefusal(ctx, r, &ref, "")
}

// recordHandoffRefusal writes the audit entry of ref. It runs after the refused
// store call, outside its transaction, which rolled back. It returns nothing:
// the refusal stays a refusal when the append fails (fail closed), and the
// failure is logged and counted. The entry never holds the token, its hash or
// the text of an error.
//
// ns is the namespace of a verified runner. It is empty for an unverified
// request, whose entry lands in the installation scope.
func (h *Handler) recordHandoffRefusal(
	ctx context.Context, r *http.Request, ref *runner.HandoffRefusal, ns namespace.Name,
) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handoffAuditTimeout)
	defer cancel()

	if appendErr := h.appendHandoffRefusal(ctx, r, ref, ns); appendErr != nil {
		faultlog.LogFault(ctx, h.logger, "audit append failed for a refused handoff token", faultlog.Cause(appendErr),
			"runnerapi.handoff-refused")

		if h.onAuditErr != nil {
			h.onAuditErr()
		}
	}
}

// recordHandoffClaimRefusal writes the entry of a refused request for the
// handoff token of a claim, by a runner that the proof of its key
// authenticated. Only a lost claim is a refusal: another error (an unknown
// operation, an unavailable store) writes nothing.
func (h *Handler) recordHandoffClaimRefusal(ctx context.Context, r *http.Request, err error, rn *runner.Runner) {
	if !errors.Is(err, runner.ErrClaimLost) {
		return
	}

	if ref, ok := runner.NewVerifiedHandoffRefusal(err, rn.ID); ok {
		h.recordHandoffRefusal(ctx, r, &ref, rn.Namespace)
	}
}

// appendHandoffRefusal appends the entry of ref in the namespace ns, or in the
// installation scope when ns is empty or does not exist.
func (h *Handler) appendHandoffRefusal(
	ctx context.Context, r *http.Request, ref *runner.HandoffRefusal, ns namespace.Name,
) error {
	// Fail closed to the installation scope: with no namespace reader, the
	// entry still lands somewhere an administrator reads.
	if ns != "" && h.namespaces != nil {
		n, err := h.namespaces.Get(ctx, ns)

		switch {
		case err == nil:
			if _, err = h.audit.Append(ctx, n.Fence(), h.handoffRefusalEntry(r, ref, ns)); err != nil {
				return fmt.Errorf("append entry: %w", err)
			}

			return nil
		case !errors.Is(err, namespace.ErrNotFound):
			return fmt.Errorf("read namespace: %w", err)
		}
	}

	ss, ok := h.audit.(audit.ServerStore)
	if !ok {
		return errNoServerAudit
	}

	if _, err := ss.AppendServer(ctx, h.handoffRefusalEntry(r, ref, audit.ServerScope)); err != nil {
		return fmt.Errorf("append installation entry: %w", err)
	}

	return nil
}

// errNoServerAudit reports an audit store that cannot write the installation
// scope.
var errNoServerAudit = errors.New("audit store has no installation scope")

// handoffRefusalEntry returns the entry of ref in the scope. Its outcome is
// denied and its reason is the reason code. The runner ID is cleaned again here,
// so this layer does not rely on the allowlist of runner.NewHandoffRefusal.
func (h *Handler) handoffRefusalEntry(r *http.Request, ref *runner.HandoffRefusal, scope namespace.Name) audit.Entry {
	rule := runner.AuditRules()[ref.Event()]

	subject := unverifiedSubject
	if ref.Verified && ref.Runner != "" {
		subject = refusal.Clean(ref.Runner, maxSessionID)
	}

	e := h.entry(r, scope, subject, rule, h.now())
	e.Outcome = audit.OutcomeDenied
	e.Reason = string(ref.Reason)

	if scope == audit.ServerScope {
		e.Object = audit.ServerScope.String()
	} else {
		e.Object = identity.NamespaceObject(scope).String()
	}

	e.Changes = []audit.Change{
		{Key: changeEvent, Old: nil, New: string(ref.Event()), Layer: "", TakesEffect: ""},
		{Key: "reason", Old: nil, New: string(ref.Reason), Layer: "", TakesEffect: ""},
		{Key: "runner", Old: nil, New: refusal.Clean(ref.Runner, maxSessionID), Layer: "", TakesEffect: ""},
		{Key: "runnerVerified", Old: nil, New: strconv.FormatBool(ref.Verified), Layer: "", TakesEffect: ""},
	}

	return e
}
