package forge

import (
	"testing"
	"time"
)

// TestTheLimiterRefusesLimitsThatAreNotPositive sends limits that NewService
// refuses straight to the limiter, as if NewService had not checked them. The
// limiter refuses the check and does not panic.
func TestTheLimiterRefusesLimitsThatAreNotPositive(t *testing.T) {
	t.Parallel()

	good := DefaultCheckLimits()

	for name, limits := range map[string]CheckLimits{
		"zero":                      {PerConnection: 0, PerInstallation: 0, PerApp: good.PerApp, Window: 0},
		"zero per installation":     {PerConnection: good.PerConnection, PerInstallation: 0, PerApp: good.PerApp, Window: good.Window},
		"negative per installation": {PerConnection: good.PerConnection, PerInstallation: -1, PerApp: good.PerApp, Window: good.Window},
		"zero per connection":       {PerConnection: 0, PerInstallation: good.PerInstallation, PerApp: good.PerApp, Window: good.Window},
		"zero per app": {
			PerConnection: good.PerConnection, PerInstallation: good.PerInstallation, PerApp: 0, Window: good.Window,
		},
		"negative per app": {
			PerConnection: good.PerConnection, PerInstallation: good.PerInstallation, PerApp: -1, Window: good.Window,
		},
		"zero window": {PerConnection: good.PerConnection, PerInstallation: good.PerInstallation, PerApp: good.PerApp, Window: 0},
		"negative window": {
			PerConnection: good.PerConnection, PerInstallation: good.PerInstallation, PerApp: good.PerApp, Window: -time.Minute,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l := newCheckLimiter(limits)
			if _, ok := l.admit("acme/conn", "github/101", time.UnixMilli(1_700_000_000_000)); ok {
				t.Fatalf("the limiter admitted a check with %+v", limits)
			}
		})
	}
}
