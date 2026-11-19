package pulumi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
)

var errCollectionStoreDown = errors.New("collection store down")

// memberCollections is the collections port of a fakePlacer that answers
// CollectionOf with a fixed result: a stack in no collection, or a failing
// store.
type memberCollections struct {
	rules *fakePlacer
	found bool
	err   error
}

func (m memberCollections) CollectionID(ctx context.Context, obj identity.Object) (string, bool, error) {
	return m.rules.CollectionID(ctx, obj)
}

func (m memberCollections) CollectionOf(context.Context, identity.Object) (string, bool, error) {
	return "payments", m.found, m.err
}

func placerWith(c memberCollections) *pulumi.Placer {
	return &pulumi.Placer{Rules: c.rules, Collections: c}
}

// writeCase is one state write of the Pulumi API. target names the path from
// the paths of a running update, and lease says that the write presents the
// lease token.
type writeCase struct {
	name   string
	method string
	target func(p updatePaths) string
	lease  bool
	body   string
}

func fixedTarget(path string) func(updatePaths) string {
	return func(updatePaths) string { return path }
}

func stateWrites() []writeCase {
	const base = "/api/stacks/acme/web/prod"

	return []writeCase{
		{"create update", http.MethodPost, fixedTarget(base + "/update"), false, `{}`},
		{"create preview", http.MethodPost, fixedTarget(base + "/preview"), false, `{}`},
		{"start update", http.MethodPost, func(p updatePaths) string { return p.Start }, false, `{}`},
		{
			"checkpoint", http.MethodPatch, func(p updatePaths) string { return p.Checkpoint }, true,
			`{"version":3,"deployment":{"resources":[]}}`,
		},
		{
			"checkpoint verbatim", http.MethodPatch, func(p updatePaths) string { return p.Verbatim }, true,
			`{"version":3,"sequenceNumber":1,"untypedDeployment":{"resources":[]}}`,
		},
		{
			"checkpoint delta", http.MethodPatch, func(p updatePaths) string { return p.Delta }, true,
			`{"version":3,"sequenceNumber":2,"checkpointHash":"x","deploymentDelta":[]}`,
		},
		{"import", http.MethodPost, fixedTarget(base + "/import"), false, `{"version":3,"deployment":{}}`},
		{"restore", http.MethodPost, fixedTarget(base + "/restore/1"), false, ``},
	}
}

// TestWritesToAStackInNoCollectionAreRefused sends every state write of the
// Pulumi API to an existing stack that no collection holds. Each write must
// answer 409 with the code unit.no-collection and the same message as the
// OpenTofu API, and must change nothing.
func TestWritesToAStackInNoCollectionAreRefused(t *testing.T) {
	t.Parallel()

	unplaced := placerWith(memberCollections{rules: newFake(), found: false, err: nil})

	for _, tc := range stateWrites() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The update service has no placement check, so a running update
			// exists on the stack, as it does for a stack that lost its
			// collection during an update.
			f := newUpdateFixtureWithAuthenticator(t, allowAll(), tokenAuthenticator{}, fixtureOptions{
				manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: unplaced,
			})
			id, lease := f.mintStartedMutating(t)
			before := historyLen(t, f)

			target := tc.target(mintedPaths(id))
			req := accessRequest(tc.method, target, tc.body)

			if tc.lease {
				req = leaseRequest(tc.method, target, lease, tc.body)
			}

			status, body := f.doUpdate(t, req)
			if status != http.StatusConflict {
				t.Fatalf("answers %d, want %d (%s)", status, http.StatusConflict, body)
			}

			if !strings.Contains(body, "unit.no-collection") ||
				!strings.Contains(body, "PUT /api/inventory/unit-collection") {
				t.Fatalf("body %q lacks the code or the command that helps", body)
			}

			if after := historyLen(t, f); after != before {
				t.Fatalf("refused write changed history: %d to %d", before, after)
			}
		})
	}
}

func historyLen(t *testing.T, f updateFixture) int {
	t.Helper()

	history, err := f.updates.History(t.Context(), f.ref, stack.Page{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	return len(history)
}

// TestReadsOfAStackInNoCollectionStayAllowed proves that the check refuses
// writes only.
func TestReadsOfAStackInNoCollectionStayAllowed(t *testing.T) {
	t.Parallel()

	f := newUpdateFixtureWithAuthenticator(t, allowAll(), tokenAuthenticator{}, fixtureOptions{
		manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: nil,
		placer: placerWith(memberCollections{rules: newFake(), found: false, err: nil}),
	})
	id, _ := f.mintStartedMutating(t)

	for _, target := range []string{
		"/api/stacks/acme/web/prod/export",
		"/api/stacks/acme/web/prod/updates",
		mintedPaths(id).Get,
	} {
		status, body := f.doUpdate(t, accessRequest(http.MethodGet, target, ""))
		if status == http.StatusConflict || status >= http.StatusInternalServerError {
			t.Errorf("GET %s answers %d (%s), want a read", target, status, body)
		}
	}
}

// TestWriteFailsClosedWhenPlacementCannotBeChecked covers a nil Placer and a
// failing collection store: neither may let the write through.
func TestWriteFailsClosedWhenPlacementCannotBeChecked(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		placer *pulumi.Placer
		want   int
	}{
		{"empty placer", &pulumi.Placer{Rules: nil, Collections: nil}, http.StatusNotImplemented},
		{"no collections", &pulumi.Placer{Rules: newFake(), Collections: nil}, http.StatusNotImplemented},
		{
			"store error", placerWith(memberCollections{rules: newFake(), found: true, err: errCollectionStoreDown}),
			http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newUpdateFixtureWithAuthenticator(t, allowAll(), tokenAuthenticator{}, fixtureOptions{
				manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: nil, placer: tc.placer,
			})

			status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/update", `{}`))
			if status != tc.want {
				t.Fatalf("answers %d, want %d (%s)", status, tc.want, body)
			}
		})
	}
}
