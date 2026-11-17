package dustpan

import (
	"fmt"

	"opendustpan.org/opendustpan/internal/jobproxysvc"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// proxyRuleStore is what a SQL store gives for the proxy rule sets.
type proxyRuleStore interface {
	ProxyRules() rulestore.Handle
}

// newProxyRuleService builds the service of the proxy rule sets from the
// namespace store. The second result is false when the store keeps none or the
// server has no inventory reader: both SQL adapters keep them. Without the
// service, the proxy rule routes stay unmounted.
func newProxyRuleService(cfg *settings, namespaces *namespace.Service) (*jobproxysvc.Service, bool, error) {
	store, ok := cfg.namespaceStore.(proxyRuleStore)
	if !ok || cfg.inventoryReader == nil || namespaces == nil {
		return nil, false, nil
	}

	svc, err := jobproxysvc.New(jobproxysvc.Config{
		Store: store.ProxyRules(), Namespaces: namespaces, Units: cfg.inventoryReader, Now: cfg.now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("proxy rule service: %w", err)
	}

	return svc, true, nil
}
