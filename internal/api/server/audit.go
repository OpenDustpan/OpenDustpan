package server

import (
	"context"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
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

// callerEntry returns the audit entry of the caller p for action: who acted,
// from where and through which channel. The Service of a change sets the
// time, the scope, the kind, the outcome and the record.
func callerEntry(r *http.Request, p identity.Principal, action identity.Action) audit.Entry {
	return audit.Entry{
		At:           time.Time{},
		Namespace:    "",
		Subject:      p.Subject,
		IDPSubject:   "",
		TokenID:      p.TokenID,
		Action:       string(action),
		Object:       "",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     clientip.String(r),
		UserAgent:    truncateUserAgent(r.UserAgent()),
		Channel:      audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:         "",
		Outcome:      "",
		RequestID:    requestid.FromContext(r.Context()),
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}
}

// appendServerChange writes one entry of the kind change in the scope
// @server for a change that did not happen (ADR 60, decisions 4 and 7),
// outside any transaction. A nil audit store writes no entry. A lost entry
// does not change the answer: the server logs it and counts it on the
// failure metric instead.
func (h *Handler) appendServerChange(r *http.Request, p identity.Principal, c serverChange) {
	if h.audit == nil {
		return
	}

	entry := callerEntry(r, p, c.action)
	entry.At = c.at
	entry.Namespace = audit.ServerScope
	entry.Kind = audit.KindChange
	entry.Outcome = c.outcome
	entry.Object = c.object

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
	entries, ok := h.audit.(namespaceAppender)
	if !ok || obj.Namespace() == "" {
		h.appendServerChange(r, p, c)

		return
	}

	ns, err := h.namespaces.Get(r.Context(), obj.Namespace())
	if err != nil {
		h.appendServerChange(r, p, c)

		return
	}

	entry := callerEntry(r, p, c.action)
	entry.At = c.at
	entry.Namespace = ns.Name
	entry.Kind = audit.KindChange
	entry.Outcome = c.outcome
	entry.Object = c.object

	if _, err := entries.Append(r.Context(), ns.Fence(), entry); err != nil {
		h.failAudit(r.Context())
	}
}

// failAudit records a lost audit entry: one log line and one count on the
// failure metric through the wired callback. It names no error text: the
// text may hold driver detail (ADR 32 decision 7).
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
