package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/faultlog"
	denials "opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// channelHeader is the request header that declares where a request comes
// from: "api" or "console". The audit entry records it as declared; unknown
// values are "api".
const channelHeader = "Dustpan-Channel"

// serverChange is one refused change of the installation that a failure
// entry records: the time on the Service clock, the action, the record and
// how it ended.
type serverChange struct {
	at      time.Time
	action  identity.Action
	object  string
	outcome audit.Outcome
}

// reachReason returns the audit reason of a reach refusal: the missing
// actions with their objects. It answers the empty string for any other
// error, and for a reach check that failed before it compared the reaches.
func reachReason(err error) string {
	refusal, ok := errors.AsType[access.ReachRefusal](err)
	if !ok {
		return ""
	}

	missing := refusal.MissingActions()
	if len(missing) == 0 {
		return ""
	}

	return access.MissingActionsReason(missing)
}

// callerEntry returns the audit entry of the caller p for action: who acted,
// from where and through which channel. For a console session, the entry
// also records the session ID, so a token that a session creates leads
// back to its session. The Service of a change sets the time, the scope,
// the kind, the outcome and the record.
func callerEntry(r *http.Request, p identity.Principal, action identity.Action) audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     "",
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(action),
		Object:        "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       sessionChanges(r),
		SourceIP:      clientip.String(r),
		UserAgent:     audit.TruncateUserAgent(userAgent(r)),
		Channel:       callerChannel(r),
		Kind:          "",
		Outcome:       "",
		RequestID:     requestid.FromContext(r.Context()),
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: string(p.Kind),
		JobID:         p.JobID,
	}
}

// sessionKeyID is the key of the change that records the console session
// of an entry.
const sessionKeyID = "sessionId"

// sessionChanges returns the session ID of the console session of r as a
// recorded value with no old value, or nil for a request with an access
// token.
func sessionChanges(r *http.Request) []audit.Change {
	caller, ok := sessionFromRequest(r)
	if !ok {
		return nil
	}

	return []audit.Change{{
		Key: sessionKeyID, Old: nil, New: caller.Match.Session.ID, Layer: "", TakesEffect: "",
	}}
}

// callerChannel returns the audit channel of r: the console for a request
// that the session cookie signed in, else the channel that the client
// declares.
func callerChannel(r *http.Request) audit.Channel {
	if _, ok := sessionFromRequest(r); ok {
		return audit.ChannelConsole
	}

	return audit.NormalizeChannel(apidesc.Header(r, channelHeader))
}

// appendServerChange writes one entry of the kind change in the scope
// @server for a change that did not happen (ADR 60, decisions 4 and 7),
// outside any transaction. A lost entry
// does not change the answer: the server logs it and counts it on the
// failure metric instead.
func (h *Handler) appendServerChange(r *http.Request, p identity.Principal, c serverChange) {
	h.appendServerChangeReason(r, p, c, "")
}

// appendServerChangeReason is appendServerChange with the reason of the
// refusal. A reason is safe text that names why the change did not happen.
func (h *Handler) appendServerChangeReason(
	r *http.Request, p identity.Principal, c serverChange, reason string,
) {
	entry := callerEntry(r, p, c.action)
	entry.At = c.at
	entry.Namespace = audit.ServerScope
	entry.Kind = audit.KindChange
	entry.Outcome = c.outcome
	entry.Object = c.object
	entry.Reason = denials.Clean(reason, audit.MaxReasonLen)

	if _, err := h.audit.AppendServer(r.Context(), entry); err != nil {
		h.failAudit(r.Context())
	}
}

// namespaceAppender appends the entries of a namespace. Both SQL adapters
// meet it with audit.Store.
type namespaceAppender interface {
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// appendObjectChange writes the failure entry of a change on obj: in the
// namespace of obj, so that the auditor of the namespace sees it, or in
// @server for an object of the installation. When the namespace does not
// exist, or the audit store keeps only @server, the entry goes to @server.
func (h *Handler) appendObjectChange(r *http.Request, p identity.Principal, obj identity.Object, c serverChange) {
	h.appendObjectChangeReason(r, p, obj, c, "")
}

// appendObjectChangeReason is appendObjectChange with the reason of the
// refusal.
func (h *Handler) appendObjectChangeReason(
	r *http.Request, p identity.Principal, obj identity.Object, c serverChange, reason string,
) {
	entries, ok := h.audit.(namespaceAppender)
	if !ok || obj.Namespace() == "" {
		h.appendServerChangeReason(r, p, c, reason)

		return
	}

	ns, err := h.namespaces.Get(r.Context(), obj.Namespace())
	if err != nil {
		h.appendServerChangeReason(r, p, c, reason)

		return
	}

	entry := callerEntry(r, p, c.action)
	entry.At = c.at
	entry.Namespace = ns.Name
	entry.Kind = audit.KindChange
	entry.Outcome = c.outcome
	entry.Object = c.object
	entry.Reason = denials.Clean(reason, audit.MaxReasonLen)

	if _, err := entries.Append(r.Context(), ns.Fence(), entry); err != nil {
		h.failAudit(r.Context())
	}
}

// appendBreakGlassRefusal writes the denied entry of a change that leaves
// too few working break-glass accounts, before the answer. A lost entry does
// not change the refusal: the server logs it and counts it on the failure
// metric instead.
func (h *Handler) appendBreakGlassRefusal(
	r *http.Request, p identity.Principal, action identity.Action, object string,
) {
	entry := callerEntry(r, p, action)
	entry.At = h.principals.Now()
	entry.Namespace = audit.ServerScope
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeDenied
	entry.Object = object
	entry.Reason = errcode.PrincipalNoWorkingBreakGlass

	if _, err := h.audit.AppendServer(r.Context(), entry); err != nil {
		h.failBreakGlassAudit(r.Context())
	}
}

// failBreakGlassAudit records a lost denial entry of a break-glass refusal:
// one log line and one count on the failure metric. It names no error text,
// no principal and no request text.
func (h *Handler) failBreakGlassAudit(ctx context.Context) {
	faultlog.LogFault(ctx, h.logger, "audit write of a break-glass refusal failed",
		faultlog.CauseUnclassified, "audit.append")

	h.countAuditFailure()
}

// failAudit records a lost audit entry: one log line and one count on the
// failure metric through the wired callback. It names no error text: the
// text may hold driver detail (ADR 32 decision 7).
func (h *Handler) failAudit(ctx context.Context) {
	faultlog.LogFault(ctx, h.logger, "audit append failed", faultlog.CauseUnclassified, "audit.append")

	h.countAuditFailure()
}

// countAuditFailure runs the wired audit-failure hook.
func (h *Handler) countAuditFailure() {
	if h.onAuditFailure != nil {
		h.onAuditFailure()
	}
}
