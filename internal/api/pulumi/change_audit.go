package pulumi

import (
	"context"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
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

// changeEntry returns the audit entry of the kind change for a (ADR 60,
// decision 4). The store appends it in the transaction of the change, so the
// change and its entry commit together or not at all (fail closed). The
// service stamps the time of the change on it.
func changeEntry(ctx context.Context, a changeAudit) audit.Entry {
	return audit.Entry{
		At:         time.Time{},
		Namespace:  a.object.Namespace(),
		Subject:    a.principal.Subject,
		IDPSubject: "",
		TokenID:    a.principal.TokenID,
		Action:     string(a.action),
		Object:     a.object.String(),
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		SourceIP:   clientip.String(a.request),
		UserAgent:  truncateUserAgent(a.request.UserAgent()),
		Channel:    audit.NormalizeChannel(a.request.Header.Get(channelHeader)),
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  requestid.FromContext(ctx),
		ObjectID:   a.object.UnitID(),
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}
}
