package tfe

import (
	"context"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// channelHeader is the request header that declares where a request comes
// from: "api" or "console". The audit entry records it as declared; unknown
// values are "api".
const channelHeader = "Dustpan-Channel"

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
// tool-operation. The principal decides, and the channel header never does
// (ADR 240 decision 1).
func (h *Handler) appendDownloadAudit(
	r *http.Request, p identity.Principal, obj identity.Object, version string,
) error {
	kind := audit.KindToolOperation
	if p.ActsAsPerson() {
		kind = audit.KindReveal
	}

	return h.appendReadAudit(r, p, obj, readEntry{version: version, valuePath: "", kind: kind})
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
	if h.audit == nil {
		return nil
	}

	ctx := r.Context()

	entry := audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     obj.Namespace(),
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(identity.ActionUnitReadSecrets),
		Object:        obj.String(),
		Version:       re.version,
		ValuePath:     re.valuePath,
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     truncateUserAgent(r.UserAgent()),
		Channel:       audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:          re.kind,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     requestid.FromContext(ctx),
		ObjectID:      obj.UnitID(),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: string(p.Kind),
		JobID:         p.JobID,
	}

	ns, err := h.namespaces.Get(ctx, entry.Namespace)
	if err != nil {
		return err
	}

	_, err = h.audit.Append(ctx, ns.Fence(), entry)

	return err
}

// changeEntry returns the audit entry of the kind change for a change of a
// unit (ADR 60, decision 4). The store appends it in the transaction of the
// change, so the change and its entry commit together or not at all (fail
// closed). The service stamps the time of the change on it.
func changeEntry(r *http.Request, p identity.Principal, obj identity.Object, action identity.Action) audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     obj.Namespace(),
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(action),
		Object:        obj.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     truncateUserAgent(r.UserAgent()),
		Channel:       audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     requestid.FromContext(r.Context()),
		ObjectID:      obj.UnitID(),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
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

// truncateUserAgent caps s at audit.MaxUserAgentLen bytes on a UTF-8 rune
// boundary, so a multi-byte rune is never split.
func truncateUserAgent(s string) string {
	if len(s) <= audit.MaxUserAgentLen {
		return s
	}

	cut := audit.MaxUserAgentLen
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}

	return s[:cut]
}
