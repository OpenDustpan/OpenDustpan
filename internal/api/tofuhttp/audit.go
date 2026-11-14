package tofuhttp

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// NamespaceService is the namespace use case that the http backend routes
// need. namespace.Service implements it.
type NamespaceService interface {
	// Get returns the namespace called name, or namespace.ErrNotFound.
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
	// WritableHere returns the namespace if this installation may write to it,
	// or namespace.ErrNotHome when another installation is its primary.
	WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// rules returns the override and audit rules of OpenTofu units, which this
// adapter shares with the TFE-compatible API.
func (h *Handler) rules() tofuunit.Rules {
	return tofuunit.Rules{Authz: h.authz, Namespaces: h.namespaces, Audit: h.audit, Guard: h.guard}
}

// appendDownloadAudit writes the audit entry of a state read: the action
// unit:read-secrets, with the kind reveal for a person and tool-operation for
// any other caller. It returns the append error, so the read answers no
// content when the entry is lost.
func (h *Handler) appendDownloadAudit(
	r *http.Request, p identity.Principal, obj identity.Object, version string,
) error {
	return h.rules().Append(r.Context(), tofuunit.Entry(r, p, obj, tofuunit.Detail{
		At: time.Now().UTC(), Action: identity.ActionUnitReadSecrets, Version: version, ValuePath: "",
		Kind: tofuunit.ReadKind(p), Outcome: audit.OutcomeSuccess, Reason: "",
	}))
}

// recordOverride returns the audit callback of one streamed write. The write
// calls it after the body streams and before it finalizes, with the serial of
// the body (ADR 41, decision 5).
func (h *Handler) recordOverride(
	r *http.Request, p identity.Principal, obj identity.Object,
) func(workspace.Override, int64) error {
	return func(override workspace.Override, serial int64) error {
		return h.appendOverrideAudit(r, p, obj, tofuunit.OverrideAction(override), strconv.FormatInt(serial, 10))
	}
}

// appendOverrideAudit writes the audit entry of one allowed override: the
// action and the unit. It returns the append error, so the write is refused
// when the entry is lost.
func (h *Handler) appendOverrideAudit(
	r *http.Request, p identity.Principal, obj identity.Object, action identity.Action, version string,
) error {
	return h.rules().Append(r.Context(), tofuunit.Entry(r, p, obj, tofuunit.Detail{
		At: time.Now().UTC(), Action: action, Version: version, ValuePath: "",
		Kind: audit.KindOverride, Outcome: audit.OutcomeSuccess, Reason: "",
	}))
}

// forceUnlockEntry returns the audit entry of a force-unlock from the request
// fields. The workspace service sets the rest: the time, the namespace, the
// subject, the unit ID, the changes and the new state version. The UNLOCK
// body is the lock info of the OpenTofu http backend, which has no field for
// a reason, so the entry holds none.
func (h *Handler) forceUnlockEntry(r *http.Request, c call, obj identity.Object) audit.Entry {
	return tofuunit.Entry(r, c.principal, obj, tofuunit.Detail{
		At: time.Time{}, Action: identity.ActionUnitWriteForced, Version: "", ValuePath: "",
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, Reason: "",
	})
}
