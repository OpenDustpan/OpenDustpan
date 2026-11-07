package forge

import (
	"testing"
	"time"
)

// TestTheLimiterSharesTheAppBudget sends Checks of two installations of one
// app straight to the limiter, as if no other limit were there. The Check over
// the app limit is refused and not recorded, and an app of another kind keeps
// its own budget.
func TestTheLimiterSharesTheAppBudget(t *testing.T) {
	t.Parallel()

	limits := CheckLimits{PerConnection: time.Nanosecond, PerInstallation: 100, PerApp: 2, Window: time.Minute}
	l := newCheckLimiter(limits)
	now := time.UnixMilli(1_700_000_000_000)

	tests := []struct {
		connection, installation, app string
		want                          bool
	}{
		{"acme/c1", "github/101", "github", true},
		{"beta/c2", "github/202", "github", true},
		{"gamma/c3", "github/303", "github", false},
		{"gamma/c3", "github/303", "github", false},
		{"delta/c4", "gitlab/404", "gitlab", true},
	}

	for i, tt := range tests {
		now = now.Add(time.Millisecond)

		if _, ok := l.admit(tt.connection, tt.installation, tt.app, now); ok != tt.want {
			t.Fatalf("check %d of %s = %v, want %v", i, tt.installation, ok, tt.want)
		}
	}

	if _, ok := l.admit("gamma/c3", "github/303", "github", now.Add(time.Minute)); !ok {
		t.Fatal("a check after the window was refused")
	}
}

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
			if _, ok := l.admit("acme/conn", "github/101", "github", time.UnixMilli(1_700_000_000_000)); ok {
				t.Fatalf("the limiter admitted a check with %+v", limits)
			}
		})
	}
}
