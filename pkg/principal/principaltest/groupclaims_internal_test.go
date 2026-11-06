package principaltest

import (
	"strings"
	"testing"
)

// TestDesignOnlyRefusal checks the guard that stops a design-only suite when
// it receives a fixture. A t.Fatal cannot be caught in the same test, so the
// test checks the text that the guard passes to t.Fatal. The test is white-box
// because the guard is unexported.
func TestDesignOnlyRefusal(t *testing.T) {
	t.Parallel()

	mapperFixture := func(*testing.T) GroupMapperFixture {
		return GroupMapperFixture{Mapper: nil, Store: nil, SetMapping: nil}
	}
	claimsFixture := func(*testing.T) GroupClaimsFixture {
		return GroupClaimsFixture{
			Applier: nil,
			Store:   nil,
			Mapping: GroupMapperFixture{Mapper: nil, Store: nil, SetMapping: nil},
		}
	}

	tests := []struct {
		name       string
		suite      string
		got        string
		wantRefuse bool
	}{
		{
			name:       "GroupMapperFixtureIsRefused",
			suite:      "RunGroupMapper",
			got:        designOnlyRefusal("RunGroupMapper", mapperFixture),
			wantRefuse: true,
		},
		{
			name:       "GroupClaimsFixtureIsRefused",
			suite:      "RunGroupClaims",
			got:        designOnlyRefusal("RunGroupClaims", claimsFixture),
			wantRefuse: true,
		},
		{
			name:       "GroupMapperNilFixturePasses",
			suite:      "RunGroupMapper",
			got:        designOnlyRefusal("RunGroupMapper", (func(*testing.T) GroupMapperFixture)(nil)),
			wantRefuse: false,
		},
		{
			name:       "GroupClaimsNilFixturePasses",
			suite:      "RunGroupClaims",
			got:        designOnlyRefusal("RunGroupClaims", (func(*testing.T) GroupClaimsFixture)(nil)),
			wantRefuse: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.wantRefuse {
				if tt.got != "" {
					t.Fatalf("refusal = %q, want none", tt.got)
				}
				return
			}
			if !strings.Contains(tt.got, "is design only") {
				t.Fatalf("refusal = %q, want the text %q", tt.got, "is design only")
			}
			if !strings.Contains(tt.got, tt.suite) {
				t.Fatalf("refusal = %q, want it to name %s", tt.got, tt.suite)
			}
		})
	}
}
