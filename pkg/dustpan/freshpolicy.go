package dustpan

import (
	"context"
	"fmt"
	"strconv"

	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/setting"
)

// freshPolicySetter is a store that reads the fresh authentication setting
// through a policy that the server sets after it builds the store.
type freshPolicySetter interface {
	SetFreshPolicy(p freshstep.Policy)
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
