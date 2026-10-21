package dustpan_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// noEntries is a namespace store that appends the entries of @server and no
// entry of a namespace. The embedded interfaces hide every other method of the
// store, so the type does not implement audit.Store.
type noEntries struct {
	namespace.Store
	audit.ServerStore
}

// noServerEntries is a namespace store that appends the entries of the
// namespaces and none of @server.
type noServerEntries struct {
	namespace.Store
	auditEntries
}

// auditEntries names audit.Store, so that noServerEntries can embed it next to
// namespace.Store.
type auditEntries = audit.Store

// TestNewServerNeedsAuditStores proves that NewServer fails closed. Every
// refusal and every override writes an audit entry, so a namespace store that
// keeps no entries cannot serve any adapter: the check names the missing
// interface, and no adapter or recorder is built with a nil store.
func TestNewServerNeedsAuditStores(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		wrap  func(st *sqlite.Store) namespace.Store
		wants string
	}{
		{
			name:  "no audit.Store, for the refusals of the OpenTofu, Pulumi, inventory, jobs, runner and context adapters",
			wrap:  func(st *sqlite.Store) namespace.Store { return noEntries{Store: st, ServerStore: st} },
			wants: "audit.Store",
		},
		{
			name:  "no audit.ServerStore, for the installation API and the temporary administrators",
			wrap:  func(st *sqlite.Store) namespace.Store { return noServerEntries{Store: st, auditEntries: st} },
			wants: "audit.ServerStore",
		},
		{
			name:  "neither",
			wrap:  func(st *sqlite.Store) namespace.Store { return bareNamespaceStore{st} },
			wants: "audit.Store",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithNamespaceStore(tt.wrap(st)))

			_, err := dustpan.NewServer(opts...)
			if !errors.Is(err, dustpan.ErrMissingOption) {
				t.Fatalf("NewServer with %s: err = %v, want ErrMissingOption", tt.name, err)
			}

			if !strings.Contains(err.Error(), tt.wants) {
				t.Fatalf("err = %q, want it to name %s", err, tt.wants)
			}
		})
	}
}
