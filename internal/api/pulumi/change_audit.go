package pulumi

import (
	"context"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// changeAudit holds the facts of one audit entry for a change of a unit: the
// request, the caller, the unit and the action that the route declares.
type changeAudit struct {
	request   *http.Request
	principal identity.Principal
	object    identity.Object
	action    identity.Action
}

// appendChangeEntry writes one audit entry of the kind change after a change
// committed (ADR 60, decision 4). The change already happened, so a failed
// append does not change the answer: it writes a log line and counts the
// failure, as a failed reveal entry does.
func (h *Handler) appendChangeEntry(ctx context.Context, a changeAudit) {
	if h.audit == nil {
		return
	}

	ns, err := h.namespaces.Get(ctx, a.object.Namespace())
	if err != nil {
		h.failAudit(ctx)

		return
	}

	entry := audit.Entry{
		At:         time.Now().UTC(),
		Namespace:  a.object.Namespace(),
		Subject:    a.principal.Subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     string(a.action),
		Object:     a.object.String(),
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		SourceIP:   sourceIP(a.request),
		UserAgent:  truncateUserAgent(a.request.UserAgent()),
		Channel:    audit.NormalizeChannel(a.request.Header.Get(channelHeader)),
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  requestid.FromContext(ctx),
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}

	if _, err := h.audit.Append(ctx, ns.Fence(), entry); err != nil {
		h.failAudit(ctx)
	}
}
