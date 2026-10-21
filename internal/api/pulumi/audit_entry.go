package pulumi

import (
	"context"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
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

// auditDetail is what one audit entry of a request records beyond the
// request and the caller: the time, the action, the state version, the
// value path, the value count, the kind and the outcome.
type auditDetail struct {
	at         time.Time
	action     string
	version    string
	valuePath  string
	valueCount *int64
	kind       audit.Kind
	outcome    audit.Outcome
}

// requestEntry returns the audit entry of the request r by the principal p on
// obj, with detail. It is the one place that fills the facts of the request:
// the address, the user agent, the channel and the request ID of ctx. The
// store assigns the sequence, the previous hash and the hash on append.
func requestEntry(
	ctx context.Context, r *http.Request, p identity.Principal, obj identity.Object, detail auditDetail,
) audit.Entry {
	return audit.Entry{
		At:            detail.at,
		Namespace:     obj.Namespace(),
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        detail.action,
		Object:        obj.String(),
		Version:       detail.version,
		ValuePath:     detail.valuePath,
		ValueCount:    detail.valueCount,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     truncateUserAgent(r.UserAgent()),
		Channel:       sessionauth.AuditChannel(r, channelHeader),
		Kind:          detail.kind,
		Outcome:       detail.outcome,
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
}

// changeEntry returns the audit entry of the kind change for a (ADR 60,
// decision 4). The store appends it in the transaction of the change, so the
// change and its entry commit together or not at all (fail closed). The
// service stamps the time of the change on it.
func changeEntry(ctx context.Context, a changeAudit) audit.Entry {
	return requestEntry(ctx, a.request, a.principal, a.object, auditDetail{
		at:         time.Time{},
		action:     string(a.action),
		version:    "",
		valuePath:  "",
		valueCount: nil,
		kind:       audit.KindChange,
		outcome:    audit.OutcomeSuccess,
	})
}
