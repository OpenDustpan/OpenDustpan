package tfe

import (
	"context"
	"net"
	"net/http"
	"time"

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
	if h.audit == nil {
		return nil
	}

	ctx := r.Context()

	entry := audit.Entry{
		At:         time.Now().UTC(),
		Namespace:  obj.Namespace(),
		Subject:    p.Subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     string(identity.ActionUnitReadSecrets),
		Object:     obj.String(),
		Version:    version,
		ValuePath:  valuePath,
		SourceIP:   sourceIP(r),
		UserAgent:  truncateUserAgent(r.UserAgent()),
		Channel:    audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:       audit.KindToolOperation,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  requestid.FromContext(ctx),
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}

	ns, err := h.namespaces.Get(ctx, entry.Namespace)
	if err != nil {
		return err
	}

	_, err = h.audit.Append(ctx, ns.Fence(), entry)

	return err
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

// sourceIP returns the remote address that the server sees, without the
// port. A value without a port reads back unchanged.
func sourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
