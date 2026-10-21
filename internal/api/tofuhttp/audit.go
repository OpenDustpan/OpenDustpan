package tofuhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// channelHeader is the request header that declares where a request comes
// from. The audit entry records it as declared; unknown values are "api".
const channelHeader = "Dustpan-Channel"

// NamespaceService is the namespace use case that the http backend routes
// need. namespace.Service implements it.
type NamespaceService interface {
	// Get returns the namespace called name, or namespace.ErrNotFound.
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// allowance returns the overrides that the caller may use on the unit of one
// request: the switch of the namespace narrowed by the switch of the unit, and
// the action of each override. It refuses nothing: the domain refuses an
// override that the result does not allow (ADR 44, decisions 4, 5 and 8).
func (h *Handler) allowance(
	ctx context.Context, p identity.Principal, w workspace.Workspace, obj identity.Object,
) (namespace.Overrides, error) {
	ns, err := h.namespaces.Get(ctx, w.Ref.Namespace)
	if err != nil {
		return namespace.Overrides{}, err
	}

	switches := ns.Overrides.And(w.Overrides)

	return namespace.Overrides{
		Forced:   switches.Forced && h.permits(ctx, p, identity.ActionUnitWriteForced, obj),
		Unlocked: switches.Unlocked && h.permits(ctx, p, identity.ActionUnitWriteUnlocked, obj),
	}, nil
}

// permits reports whether the principal may do action on obj.
func (h *Handler) permits(ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object) bool {
	return h.authz.Authorize(ctx, p, action, obj) == nil
}

// recordOverrideRefusal writes the audit entry of a write that the workspace
// refused because an override is off or not granted. The other layers (the
// switch check in the domain, the permits check above) do not write one, and
// the 403 alone leaves no trace. It does nothing for any other error.
func (h *Handler) recordOverrideRefusal(
	ctx context.Context, r *http.Request, p identity.Principal, obj identity.Object, err error,
) {
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		return
	}

	// The action comes from the typed refusal, never from its text. A refusal
	// without a type is a forced write.
	action := identity.ActionUnitWriteForced
	if override, ok := workspace.RefusedOverride(err); ok && override == workspace.OverrideUnlocked {
		action = identity.ActionUnitWriteUnlocked
	}

	h.guard.Record(ctx, r, p, refusal.Denial{Action: action, Object: obj, Reason: "override refused", Window: 0})
}

// appendDownloadAudit writes the audit entry of a state read: the action
// unit:read-secrets, with the kind reveal for a person and tool-operation for
// any other caller. The principal decides, and the channel header never does.
// It returns the append error, so the read answers no content when the entry
// is lost.
func (h *Handler) appendDownloadAudit(
	r *http.Request, p identity.Principal, obj identity.Object, version string,
) error {
	ctx := r.Context()

	kind := audit.KindToolOperation
	if p.ActsAsPerson() {
		kind = audit.KindReveal
	}

	entry := audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     obj.Namespace(),
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(identity.ActionUnitReadSecrets),
		Object:        obj.String(),
		Version:       version,
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     truncateUserAgent(r.UserAgent()),
		Channel:       audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:          kind,
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

// recordOverride returns the audit callback of one streamed write. The write
// calls it after the body streams and before it finalizes, with the serial of
// the body (ADR 41, decision 5).
func (h *Handler) recordOverride(
	r *http.Request, p identity.Principal, obj identity.Object,
) func(workspace.Override, int64) error {
	return func(override workspace.Override, serial int64) error {
		action := identity.ActionUnitWriteUnlocked
		if override == workspace.OverrideForced {
			action = identity.ActionUnitWriteForced
		}

		return h.appendOverrideAudit(r, p, obj, action, strconv.FormatInt(serial, 10))
	}
}

// appendOverrideAudit writes the audit entry of one allowed override: the
// action and the unit (ADR 44, decision 7).
// It returns the append error, so the write is refused when the entry is lost.
func (h *Handler) appendOverrideAudit(
	r *http.Request, p identity.Principal, obj identity.Object, action identity.Action, version string,
) error {
	ctx := r.Context()

	entry := audit.Entry{
		At:            time.Now().UTC(),
		Namespace:     obj.Namespace(),
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(action),
		Object:        obj.String(),
		Version:       version,
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     truncateUserAgent(r.UserAgent()),
		Channel:       audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:          audit.KindOverride,
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

// forceUnlockEntry returns the audit entry of a force-unlock from the request
// fields. The workspace service sets the rest: the time, the namespace, the
// subject, the unit ID, the changes and the new state version. The UNLOCK
// body is the lock info of the OpenTofu http backend, which has no field for
// a reason, so the entry holds none.
func (h *Handler) forceUnlockEntry(r *http.Request, c call, obj identity.Object) audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     obj.Namespace(),
		Subject:       c.principal.Subject,
		IDPSubject:    "",
		TokenID:       c.principal.TokenID,
		Action:        string(identity.ActionUnitWriteForced),
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
		PrincipalKind: string(c.principal.Kind),
		JobID:         c.principal.JobID,
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
