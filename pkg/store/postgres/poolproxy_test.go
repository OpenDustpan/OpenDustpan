package postgres_test

import (
	"os"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner/runnertest"
)

func TestPoolProxyConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	runnertest.RunPoolProxyStore(t, func(t *testing.T) runnertest.ProxyHarness {
		t.Helper()

		s := open(t, base)
		r := s.Runners()

		domain := &runnerDomain{t: t, s: s, counter: atomic.Int64{}}

		return runnertest.ProxyHarness{Pools: r, Proxy: r, Domain: domain}
	})
}
