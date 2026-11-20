package accesstoken_test

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
)

// cacheProbe is a MakerChecker that records whether the context it gets
// holds a request cache, and answers that the maker has no path.
type cacheProbe struct {
	cached *bool
}

func (p cacheProbe) CanActAs(ctx context.Context, _, _ string) (bool, error) {
	*p.cached = access.HasRequestCache(ctx)

	return false, nil
}

// TestRevokeLostMakersDropsTheRequestCache sends contexts that hold a request
// cache to RevokeLostMakers. The checker must never get one: a cache holds the
// reads of the route check of the request before the change, so a decision
// on it can keep a token whose maker just lost the path. The guard is in the
// service, so it holds for each MakerChecker.
func TestRevokeLostMakersDropsTheRequestCache(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
	}{
		{"a context with a request cache", func(t *testing.T) context.Context {
			t.Helper()

			return access.WithRequestCache(t.Context())
		}},
		{"a context with a request cache under other values", func(t *testing.T) context.Context {
			t.Helper()

			type key struct{}

			return context.WithValue(access.WithRequestCache(context.WithValue(t.Context(), key{}, 1)), key{}, 2)
		}},
		{"a context with no request cache", func(t *testing.T) context.Context {
			t.Helper()

			return t.Context()
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			seen := true
			f := newMakerFixture(t, cacheProbe{cached: &seen})

			cause := accesstoken.Cause{Subject: "tina", Action: "server:manage-groups", Object: "group:grp-1"}

			got, err := f.checked.RevokeLostMakers(tc.ctx(t), cause)
			if err != nil {
				t.Fatalf("revoke lost makers: %v", err)
			}

			if seen {
				t.Fatal("the checker got a context with the request cache")
			}

			if len(got.Revoked) != 1 {
				t.Fatalf("revoked = %v, want the service token", got.Revoked)
			}
		})
	}
}
