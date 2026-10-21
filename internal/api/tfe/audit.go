package tfe

import (
	"context"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// rules returns the override and audit rules of OpenTofu units, which this
// API shares with the OpenTofu http backend.
func (h *Handler) rules() tofuunit.Rules {
	return tofuunit.Rules{Authz: h.authz, Namespaces: h.namespaces, Audit: h.audit, Guard: h.guard}
}

// appendToolOperationAudit writes one audit entry for a route that answers a
// plain secret value to the CLI: the action unit:read-secrets with the kind
// tool-operation, so a reader never confuses it with a person who asked to
// see a secret value. It returns the append error, so the route answers 500
// with no secret value when it cannot record the entry (ADR 41 decision 5).
// A nil audit store writes no entry.
func (h *Handler) appendToolOperationAudit(
	r *http.Request, p identity.Principal, obj identity.Object, version, valuePath string,
) error {
	return h.appendReadAudit(
		r,
		p,
		obj,
		readEntry{version: version, valuePath: valuePath, kind: audit.KindToolOperation},
	)
}

// appendDownloadAudit writes the audit entry of a state download. A download
// by a person writes the kind reveal, because a person asked to see every
// secret value of the state. Any other download writes the kind
// tool-operation. The principal decides, and the channel header never does.
func (h *Handler) appendDownloadAudit(
	r *http.Request, p identity.Principal, obj identity.Object, version string,
) error {
	return h.appendReadAudit(r, p, obj, readEntry{version: version, valuePath: "", kind: tofuunit.ReadKind(p)})
}

// readEntry holds the fields of the audit entry of a read of secret values
// that differ between routes.
type readEntry struct {
	version   string
	valuePath string
	kind      audit.Kind
}

// appendReadAudit writes one audit entry of the action unit:read-secrets.
func (h *Handler) appendReadAudit(
	r *http.Request, p identity.Principal, obj identity.Object, re readEntry,
) error {
	return h.rules().Append(r.Context(), tofuunit.Entry(r, p, obj, tofuunit.Detail{
		At: time.Now().UTC(), Action: identity.ActionUnitReadSecrets, Version: re.version, ValuePath: re.valuePath,
		Kind: re.kind, Outcome: audit.OutcomeSuccess, Reason: "",
	}))
}

// changeEntry returns the audit entry of the kind change for a change of a
// unit (ADR 60, decision 4). The store appends it in the transaction of the
// change, so the change and its entry commit together or not at all (fail
// closed). The service stamps the time of the change on it.
func changeEntry(r *http.Request, p identity.Principal, obj identity.Object, action identity.Action) audit.Entry {
	entry := tofuunit.Entry(r, p, obj, tofuunit.Detail{
		At: time.Time{}, Action: action, Version: "", ValuePath: "",
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, Reason: "",
	})

	// The change entry of this adapter has always named no access token,
	// unlike its other entries. It stays so until a decision changes it.
	entry.TokenID = ""

	return entry
}

// failAudit records a lost audit entry: one log line with no secret and no
// value, and one count on the failure metric through the wired callback. It
// names no error text: the text may hold driver detail (ADR 32 decision 7).
func (h *Handler) failAudit(ctx context.Context) {
	h.logger.ErrorContext(ctx, "audit append failed")

	if h.onAuditFailure != nil {
		h.onAuditFailure()
	}
}
