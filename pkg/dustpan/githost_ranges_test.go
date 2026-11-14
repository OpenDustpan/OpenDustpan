package dustpan_test

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
)

// newGitHostServer builds a server with the Git host store and the options in
// extra.
func newGitHostServer(t *testing.T, extra ...dustpan.Option) error {
	t.Helper()

	p := newPorts(t)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn), dustpan.WithGitHostStore(p.st.GitHost()))
	opts = append(opts, extra...)

	_, err := dustpan.NewServer(opts...)

	return err
}

// TestNewServerRefusesHostileGitHostRanges sends a wide or unsafe range
// straight to NewServer, as an embedder does, with no flag parser before it.
// The server must refuse to start.
func TestNewServerRefusesHostileGitHostRanges(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"0.0.0.0/0", "::/0", "::ffff:0:0/96", "127.0.0.0/8", "127.0.0.1/32", "169.254.0.0/16", "169.254.169.254/32",
		"fe80::/32", "fd00:ec2::254/128", "10.0.0.0/7",
	} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()

			err := newGitHostServer(t, dustpan.WithGitHostAllowedRanges(netip.MustParsePrefix(text)))
			if !errors.Is(err, egress.ErrRangeTooWide) {
				t.Fatalf("NewServer with the range %s: got %v, want ErrRangeTooWide", text, err)
			}
		})
	}
}

// TestNewServerRefusesCheckLimitsThatAreNotPositive sends limits that turn the
// limiter off, or make the first check panic, straight to NewServer.
func TestNewServerRefusesCheckLimitsThatAreNotPositive(t *testing.T) {
	t.Parallel()

	good := forge.DefaultCheckLimits()

	for name, limits := range map[string]forge.CheckLimits{
		"zero per installation": {PerConnection: good.PerConnection, PerInstallation: 0, PerApp: good.PerApp, Window: good.Window},
		"zero per connection":   {PerConnection: 0, PerInstallation: good.PerInstallation, PerApp: good.PerApp, Window: good.Window},
		"zero per app": {
			PerConnection: good.PerConnection, PerInstallation: good.PerInstallation, PerApp: 0, Window: good.Window,
		},
		"zero window":     {PerConnection: good.PerConnection, PerInstallation: good.PerInstallation, PerApp: good.PerApp, Window: 0},
		"negative window": {PerConnection: good.PerConnection, PerInstallation: 1, PerApp: good.PerApp, Window: -time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := newGitHostServer(t, dustpan.WithGitHostCheckLimits(limits))
			if !errors.Is(err, forge.ErrInvalid) {
				t.Fatalf("NewServer with %+v: got %v, want ErrInvalid", limits, err)
			}
		})
	}
}
