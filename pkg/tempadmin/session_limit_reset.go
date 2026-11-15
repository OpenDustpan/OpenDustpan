package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/setting"
)

// The recovery modes. The host reset runs in the first two only.
const (
	// RecoveryModeBootstrap is the recovery mode of a host that holds the
	// credential of the installation.
	RecoveryModeBootstrap = "bootstrap"
	// RecoveryModeSignedRequest is the recovery mode of a host that takes a
	// signed request.
	RecoveryModeSignedRequest = "signed-request"
	// RecoveryModeNone is the recovery mode of an installation with no
	// break-glass. In this mode the host reset refuses.
	RecoveryModeNone = "none"
)

// ErrResetRefused reports a reset of the stored session limit that the
// package refuses: no reason, or a recovery mode that is not bootstrap or
// signed-request.
var ErrResetRefused = errors.New("session limit reset refused")

// ResetInput names one reset of the stored session limit.
type ResetInput struct {
	// Mode is the recovery mode of the installation.
	Mode string
	// Reason is the reason that the host operator gave.
	Reason string
	// Audit takes the failure entry of a refusal. Required.
	Audit audit.ServerStore
	// Entry is the audit entry of the host. The settings service sets the
	// time, the scope, the action, the kind and the outcome.
	Entry audit.Entry
	// Now returns the time of the change. Nil means time.Now.
	Now func() time.Time
}

// ResetSessionLimit deletes the stored session limit, so the effective limit
// is DefaultSessionLimit again, and writes an audit entry with the reason. It
// returns ErrResetRefused, and changes nothing, for every recovery mode except
// bootstrap and signed-request, and for a reason that ValidateReason refuses.
// A refusal writes a failure entry. The host command checks the same rules and
// the off switch.
func ResetSessionLimit(ctx context.Context, store setting.Store, in ResetInput) error {
	if in.Audit == nil {
		return fmt.Errorf("%w: session limit reset audit", ErrMissingDependency)
	}

	// Defense in depth: the host command checks the mode and the reason too.
	// These checks stay so that a new caller cannot skip them. The mode check
	// is an allowlist, so an unknown or an empty mode is refused.
	if in.Mode != RecoveryModeBootstrap && in.Mode != RecoveryModeSignedRequest {
		return refuseReset(ctx, in, fmt.Errorf("%w: the recovery mode is %q: set DUSTPAN_RECOVERY_MODE=%s on the host, "+
			"then run the command again", ErrResetRefused, in.Mode, RecoveryModeBootstrap))
	}

	if err := ValidateReason(in.Reason); err != nil {
		return refuseReset(ctx, in, fmt.Errorf("%w: %w", ErrResetRefused, err))
	}

	registry, err := setting.NewRegistry(SessionLimitSettingDeclarations())
	if err != nil {
		return fmt.Errorf("session limit registry: %w", err)
	}

	svc := setting.NewService(store, registry, nil, nil, in.Now)

	res, err := svc.Get(ctx, setting.InstallationScope())
	if err != nil {
		return fmt.Errorf("read the setting %s: %w", SettingSessionLimit, err)
	}

	entry := in.Entry
	entry.Reason = in.Reason
	entry.Object = string(audit.ServerScope)

	patch := setting.Patch{{Key: SettingSessionLimit, Value: "", Delete: true}}

	// The host holds the credential of the installation, so the change counts
	// as fresh: a reset to the default never needs a sign-in.
	_, err = svc.Patch(ctx, setting.InstallationScope(), patch,
		setting.PatchRequest{Entry: entry, IfMatch: res.Revision, Fresh: true})
	if err != nil {
		return fmt.Errorf("reset the setting %s: %w", SettingSessionLimit, err)
	}

	return nil
}

// refuseReset writes the failure entry of a refused reset and returns refusal.
// The entry holds the reason only when ValidateReason accepts it, so a hostile
// text does not reach the log.
func refuseReset(ctx context.Context, in ResetInput, refusal error) error {
	e := in.Entry
	e.At = time.Now().UTC().Truncate(time.Millisecond)
	if in.Now != nil {
		e.At = in.Now().UTC().Truncate(time.Millisecond)
	}

	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeFailure
	e.Action = Action
	e.Object = "setting:" + SettingSessionLimit

	e.Reason = ""
	if ValidateReason(in.Reason) == nil {
		e.Reason = in.Reason
	}

	if _, err := in.Audit.AppendServer(ctx, e); err != nil {
		return fmt.Errorf("%w: write the refusal entry: %w", refusal, err)
	}

	return refusal
}
