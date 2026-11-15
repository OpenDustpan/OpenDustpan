package tempadmin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/setting"
)

// RecoveryModeNone is the recovery mode of an installation with no
// break-glass. In this mode the host reset refuses.
const RecoveryModeNone = "none"

// ErrResetRefused reports a reset of the stored session limit that the
// package refuses: no reason, or the recovery mode none.
var ErrResetRefused = errors.New("session limit reset refused")

// ResetInput names one reset of the stored session limit.
type ResetInput struct {
	// Mode is the recovery mode of the installation.
	Mode string
	// Reason is the reason that the host operator gave.
	Reason string
	// Entry is the audit entry of the host. The settings service sets the
	// time, the scope, the action, the kind and the outcome.
	Entry audit.Entry
	// Now returns the time of the change. Nil means time.Now.
	Now func() time.Time
}

// ResetSessionLimit deletes the stored session limit, so the effective limit
// is DefaultSessionLimit again, and writes an audit entry with the reason. It
// returns ErrResetRefused, and changes nothing, for the recovery mode none and
// for a reason that ValidateReason refuses. The host command checks the same
// rules and the off switch.
func ResetSessionLimit(ctx context.Context, store setting.Store, in ResetInput) error {
	// Defense in depth: the host command checks the mode and the reason too.
	// These checks stay so that a new caller cannot skip them.
	if in.Mode == RecoveryModeNone {
		return fmt.Errorf("%w: the recovery mode is %s: set DUSTPAN_RECOVERY_MODE=bootstrap on the host, "+
			"then run the command again", ErrResetRefused, RecoveryModeNone)
	}

	if err := ValidateReason(in.Reason); err != nil {
		return fmt.Errorf("%w: %w", ErrResetRefused, err)
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
