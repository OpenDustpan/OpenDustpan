package sqlite_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/jobproxy/jobproxytest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestProxyRuleStoreConformance runs the RuleStore suite on SQLite.
func TestProxyRuleStoreConformance(t *testing.T) {
	t.Parallel()

	jobproxytest.RunRuleStore(t, func(t *testing.T) jobproxytest.Harness {
		t.Helper()

		s := open(t)

		return jobproxytest.Harness{
			Store: s.ProxyRules(),
			Namespace: func(t *testing.T, name string) namespace.Fence {
				t.Helper()

				return ensureNamespace(t, s, namespace.Name(name))
			},
			Audit: func(t *testing.T, ns string) []audit.Entry {
				t.Helper()

				return allAuditEntries(t, s, namespace.Name(ns))
			},
		}
	})
}
