package sqlite_test

import (
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner/runnertest"
)

func TestPoolProxyConformance(t *testing.T) {
	t.Parallel()

	runnertest.RunPoolProxyStore(t, func(t *testing.T) runnertest.ProxyHarness {
		t.Helper()

		s := open(t)
		r := s.Runners()

		domain := &runnerDomain{t: t, s: s, counter: atomic.Int64{}}

		return runnertest.ProxyHarness{Pools: r, Proxy: r, Domain: domain}
	})
}
