package gate_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func groupLimit(scope gate.Scope, group string) gate.AccessLimit {
	return gate.AccessLimit{
		Scope: scope, ID: "freeze", Version: 0,
		Policy: `forbid (principal, action, resource) when { principal in OpenDustpan::Group::"` + group + `" };`,
	}
}

// readLimitStore returns one stored access limit from Get and List.
type readLimitStore struct {
	gate.Store

	limit gate.AccessLimit
}

type readLimits struct {
	gate.Objects[gate.AccessLimit]

	limit gate.AccessLimit
}

func (s readLimitStore) AccessLimits() gate.Objects[gate.AccessLimit] {
	return readLimits{Objects: nil, limit: s.limit}
}

func (r readLimits) Get(context.Context, gate.Scope, gate.ID) (gate.AccessLimit, error) {
	return r.limit, nil
}

func (r readLimits) List(context.Context, gate.Scope) ([]gate.AccessLimit, error) {
	return []gate.AccessLimit{r.limit}, nil
}

func (readLimits) Put(context.Context, gate.AccessLimit, int64, namespace.Fence, audit.Entry) (int64, error) {
	return 1, nil
}

// TestPutAccessLimitRefusesAGroupThatDoesNotMatch is the table of hostile
// group names at the service layer. fakeGroups holds the group "platform".
func TestPutAccessLimitRefusesAGroupThatDoesNotMatch(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")

	tests := []struct {
		name  string
		group string
		want  error
	}{
		{"stored name", "platform", nil},
		{"group that does not exist", "ghost", gate.ErrLimitGroupName},
		{"stored name in another case", "Platform", gate.ErrLimitGroupName},
		{"upper case name", "PLATFORM", gate.ErrLimitGroupName},
		{"name that is not valid", "a b", gate.ErrLimitGroupName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			engine := &fakeEngine{Engine: nil, diags: nil, err: nil, membershipErr: nil, checks: nil, schema: nil}
			svc, st := newObjectService(t, engine)

			_, err := svc.PutAccessLimit(t.Context(), groupLimit(scope, tt.group), 0, callerEntry())
			if !errors.Is(err, tt.want) {
				t.Fatalf("PutAccessLimit() = %v, want %v", err, tt.want)
			}

			if code, _ := gate.Code(err); tt.want != nil && code != "gate.limit-group-name" {
				t.Errorf("Code() = %q, want gate.limit-group-name", code)
			}

			if tt.want != nil && len(st.writes) != 0 {
				t.Errorf("a refused limit reached the store %d times, want 0", len(st.writes))
			}
		})
	}
}

// TestAccessLimitReadsShowStaleGroups checks that a read names the group of a
// stored limit that no group has now.
func TestAccessLimitReadsShowStaleGroups(t *testing.T) {
	t.Parallel()

	scope := gate.NamespaceScope("acme")
	engine := &fakeEngine{Engine: nil, diags: nil, err: nil, membershipErr: nil, checks: nil, schema: nil}

	tests := []struct {
		name  string
		group string
		want  []string
	}{
		{"group exists", "platform", nil},
		{"group deleted", "ghost", []string{"ghost"}},
		{"group in another case", "Platform", []string{"Platform"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, err := gate.NewConfigService(gate.ConfigServiceConfig{
				Store: readLimitStore{Store: nil, limit: groupLimit(scope, tt.group)}, Namespaces: fakeNamespaces{},
				Approvers: fakeGroups{}, Engine: engine, Now: configNow,
			})
			if err != nil {
				t.Fatalf("NewConfigService() = %v", err)
			}

			got := svc.StaleGroups(t.Context(), groupLimit(scope, tt.group).Policy)
			if !slices.Equal(got, tt.want) {
				t.Errorf("StaleGroups() = %v, want %v", got, tt.want)
			}
		})
	}
}
