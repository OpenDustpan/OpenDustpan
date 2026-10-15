package ipallow_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// benchGate64List parses 64 CIDRs (the maximum): 10.0.0.0/24 through
// 10.0.63.0/24.
func benchGate64List(b *testing.B) ipallow.List {
	b.Helper()

	items := make([]string, 0, ipallow.MaxEntries)
	for i := range ipallow.MaxEntries {
		items = append(items, fmt.Sprintf("10.0.%d.0/24", i))
	}

	list, err := ipallow.ParseList(items)
	if err != nil {
		b.Fatalf("parse 64 CIDRs: %v", err)
	}

	return list
}

// benchGate builds a Gate with the feature on over list, with Next allowing
// every request, and returns it with a context holding addr.
func benchGate(b *testing.B, list ipallow.List, addr netip.Addr) (ipallow.Gate, context.Context) {
	b.Helper()

	next := &fakeNext{calls: 0, err: nil}
	rules := &fakeRules{
		calls: 0,
		lists: map[namespace.Name]ipallow.List{"acme": list},
		errs:  map[namespace.Name]error{},
	}

	gate := ipallow.Gate{Next: next, Rules: rules, Enabled: true, IncludeBootstrap: false}

	return gate, clientip.WithAddr(context.Background(), addr)
}

// BenchmarkGateAuthorize measures one Authorize call with the feature on
// against a full list of 64 CIDRs: the address inside only the last entry,
// and a sub-benchmark with the address outside all of them.
func BenchmarkGateAuthorize(b *testing.B) {
	list := benchGate64List(b)

	b.Run("inside-last", func(b *testing.B) {
		b.ReportAllocs()

		gate, ctx := benchGate(b, list, netip.MustParseAddr("10.0.63.7"))
		principal := userPrincipal("alice")
		obj := identity.NamespaceObject(namespace.Name("acme"))

		b.ResetTimer()

		for b.Loop() {
			if err := gate.Authorize(ctx, principal, identity.ActionNamespaceRead, obj); err != nil {
				b.Fatalf("Authorize inside the list = %v, want nil", err)
			}
		}
	})

	b.Run("outside-all", func(b *testing.B) {
		b.ReportAllocs()

		gate, ctx := benchGate(b, list, netip.MustParseAddr("192.0.2.10"))
		principal := userPrincipal("alice")
		obj := identity.NamespaceObject(namespace.Name("acme"))

		b.ResetTimer()

		for b.Loop() {
			if err := gate.Authorize(ctx, principal, identity.ActionNamespaceRead, obj); !errors.Is(
				err,
				identity.ErrDenied,
			) {
				b.Fatalf("Authorize outside the list = %v, want ErrDenied", err)
			}
		}
	})
}
