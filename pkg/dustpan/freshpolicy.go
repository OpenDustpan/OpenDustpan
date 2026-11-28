package dustpan

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/setting"
)

// FreshGuard returns the fresh step guard of the access token service that
// the host command makes over the built-in store b. The policy is the policy
// of the server, so both read the installation setting freshAuthEnabled the
// same way. The host command has no console session, so the check refuses a
// request whenever the policy requires the step.
func FreshGuard(b *BuiltinStore) (freshstep.Guard, error) {
	cfg := defaultSettings()
	cfg.namespaceStore = b
	cfg.settingsConfig.store = b.SettingStore()
	cfg.logger = slog.New(slog.DiscardHandler)

	return tokenFreshGuardOf(cfg)
}

// tokenFreshGuardOf returns the fresh step guard of the access token service
// of cfg: the policy of freshPolicyOf and the check of the console session.
func tokenFreshGuardOf(cfg *settings) (freshstep.Guard, error) {
	policies, err := newSessionPolicyService(cfg)
	if err != nil {
		return freshstep.Guard{}, err
	}

	return freshstep.NewGuard(freshPolicyOf(cfg), tokenFreshCheck(policies, cfg.now, cfg.logger))
}

// freshPolicyOf returns the policy that each layer asks before it needs a
// fresh authentication. The policy reads the effective value of the
// installation setting freshAuthEnabled at each call, so a change of the
// setting applies at once. A server with no settings store gives a policy that
// always requires the step. A value that is not a bool gives an error, and an
// error requires the step too.
func freshPolicyOf(cfg *settings) freshstep.SettingPolicy {
	svc := newSettingService(cfg, cfg.namespaceStore)
	if svc == nil {
		return freshstep.NewPolicy(nil)
	}

	return freshstep.NewPolicy(func(ctx context.Context) (bool, error) {
		item, err := svc.Effective(ctx, setting.InstallationScope(), setting.SettingFreshAuthEnabled)
		if err != nil {
			return false, fmt.Errorf("read the fresh authentication setting: %w", err)
		}

		on, err := strconv.ParseBool(item.Value)
		if err != nil {
			return false, fmt.Errorf("read the fresh authentication setting: %w", err)
		}

		return on, nil
	})
}
