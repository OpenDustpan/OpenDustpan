// Package tofuunit holds what the two OpenTofu adapters share about a
// workspace as a unit: its authorization object, the overrides that a caller
// may use on it, and the audit entries of a request on it. The TFE-compatible
// API and the OpenTofu http backend never call each other; both call this
// package, so an override and its audit entry follow one rule whichever
// protocol the CLI speaks.
package tofuunit

import (
	"context"
	"errors"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Tool is the tool name of a workspace in a unit reference. Both OpenTofu
// adapters use it, so the console shows one tool for both.
const Tool = "opentofu"

// ChannelHeader is the request header that declares where a request comes
// from: "api" or "console". The audit entry records it as declared; unknown
// values are "api".
const ChannelHeader = "Dustpan-Channel"

// Object returns the authorization object of the workspace ref.
func Object(ref workspace.Ref) (identity.Object, error) {
	return identity.UnitObject(Tool, ref.Namespace, ref.Name.String())
}

// Namespaces is the namespace use case that the rules need.
// namespace.Service implements it.
type Namespaces interface {
	// Get returns the namespace called name, or namespace.ErrNotFound.
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// ErrNoAuditStore is the refusal of Append when Rules has no audit store.
var ErrNoAuditStore = errors.New("tofuunit: no audit store")

// Rules checks the overrides of a caller on a workspace and writes the audit
// entries of OpenTofu requests. Authz, Namespaces, Audit and Guard must be
// set: Append refuses to run without an audit store.
type Rules struct {
	Authz      identity.Authorizer
	Namespaces Namespaces
	Audit      audit.Store
	Guard      *refusal.Recorder
}

// Allowance returns the overrides that p may use on the workspace w, whose
// object is obj: the switch of the namespace narrowed by the switch of the
// unit, and the action of each override. It refuses nothing: the domain
// refuses an override that the result does not allow, and the store checks
// the switches again in the transaction of the write.
func (u Rules) Allowance(
	ctx context.Context, p identity.Principal, w workspace.Workspace, obj identity.Object,
) (namespace.Overrides, error) {
	ns, err := u.Namespaces.Get(ctx, w.Ref.Namespace)
	if err != nil {
		return namespace.Overrides{}, err
	}

	switches := ns.Overrides.And(w.Overrides)

	return namespace.Overrides{
		Forced:   switches.Forced && u.Permits(ctx, p, identity.ActionUnitWriteForced, obj),
		Unlocked: switches.Unlocked && u.Permits(ctx, p, identity.ActionUnitWriteUnlocked, obj),
	}, nil
}

// Permits reports whether p may do action on obj. An Authorizer error of any
// kind denies.
func (u Rules) Permits(ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object) bool {
	return u.Authz.Authorize(ctx, p, action, obj) == nil
}

// OverrideAction returns the action of override. Any value but
// workspace.OverrideUnlocked is a forced write, the stronger of the two, so
// an unknown value never reads as the weaker action.
func OverrideAction(override workspace.Override) identity.Action {
	if override == workspace.OverrideUnlocked {
		return identity.ActionUnitWriteUnlocked
	}

	return identity.ActionUnitWriteForced
}

// RecordOverrideRefusal writes the refusal record of a write that the
// workspace refused because an override is off or not granted. The switch
// check in the domain and the permits check of Allowance write none, and the
// 403 alone leaves no trace. It does nothing for any other error.
func (u Rules) RecordOverrideRefusal(
	ctx context.Context, r *http.Request, p identity.Principal, obj identity.Object, err error,
) {
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		return
	}

	// The action comes from the typed refusal, never from its text. A refusal
	// without a type is a forced write.
	action := identity.ActionUnitWriteForced
	if override, ok := workspace.RefusedOverride(err); ok {
		action = OverrideAction(override)
	}

	u.Guard.Record(ctx, r, p, refusal.Denial{
		Action: action, Object: obj, Reason: "override refused", ReasonWithin: nil, Window: 0,
	})
}

// Detail is what one audit entry records beyond the request and the caller.
type Detail struct {
	// At is the time of the entry. The zero time leaves it to the workspace
	// service, which stamps the time of the change.
	At        time.Time
	Action    identity.Action
	Version   string
	ValuePath string
	Kind      audit.Kind
	Outcome   audit.Outcome
	Reason    string
}

// Entry returns the audit entry of the request r by p on obj, with detail. It
// is the one place that fills the facts of an OpenTofu request: the address,
// the user agent cut on a character boundary, the declared channel and the
// request ID. The store assigns the sequence, the previous hash and the hash
// on append.
func Entry(r *http.Request, p identity.Principal, obj identity.Object, detail Detail) audit.Entry {
	return audit.Entry{
		At:            detail.At,
		Namespace:     obj.Namespace(),
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(detail.Action),
		Object:        obj.String(),
		Version:       detail.Version,
		ValuePath:     detail.ValuePath,
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      clientip.String(r),
		UserAgent:     audit.TruncateUserAgent(r.UserAgent()),
		Channel:       audit.NormalizeChannel(r.Header.Get(ChannelHeader)),
		Kind:          detail.Kind,
		Outcome:       detail.Outcome,
		RequestID:     requestid.FromContext(r.Context()),
		ObjectID:      obj.UnitID(),
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        detail.Reason,
		PrincipalKind: string(p.Kind),
		JobID:         p.JobID,
	}
}

// ReadKind returns the kind of the audit entry of a read of secret values by
// p: reveal for a person, who asked to see every secret value, and
// tool-operation for any other caller. The principal decides, and the
// channel header never does.
func ReadKind(p identity.Principal) audit.Kind {
	if p.ActsAsPerson() {
		return audit.KindReveal
	}

	return audit.KindToolOperation
}

// Append writes entry in its namespace with the fence of the namespace, so
// the appends of one namespace form one sequence. It returns ErrNoAuditStore
// without an audit store, and the error of the namespace read or of the
// append; the caller refuses its answer when the entry is lost.
func (u Rules) Append(ctx context.Context, entry audit.Entry) error {
	// Defense in depth: each adapter refuses a nil audit store when it is
	// built; this check stays so that a Rules value built another way fails
	// closed and never answers as if the entry were written.
	if u.Audit == nil {
		return ErrNoAuditStore
	}

	ns, err := u.Namespaces.Get(ctx, entry.Namespace)
	if err != nil {
		return err
	}

	_, err = u.Audit.Append(ctx, ns.Fence(), entry)

	return err
}
