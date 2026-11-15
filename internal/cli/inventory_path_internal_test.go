package cli

import (
	"net/url"
	"strings"
	"testing"
)

// TestInventoryPathKeepsHostileSegmentsInOneSegment sends values that skip
// the operand allowlists straight to the path builder. The test fails when
// the path escape of one segment is removed.
func TestInventoryPathKeepsHostileSegmentsInOneSegment(t *testing.T) {
	t.Parallel()

	for _, seg := range []string{"a/../b", "../x", "a/b", "a?b", "a#b", "a b", "a%2Fb", "%2e%2e"} {
		t.Run(seg, func(t *testing.T) {
			t.Parallel()

			got := inventoryPath("namespaces", seg, "collections")
			want := inventoryAPIPrefix + "/namespaces/" + url.PathEscape(seg) + "/collections"

			if got != want {
				t.Fatalf("inventoryPath = %q, want %q", got, want)
			}

			if strings.Count(strings.TrimPrefix(got, inventoryAPIPrefix), "/") != 3 {
				t.Fatalf("inventoryPath = %q: segment %q spread over more than one path segment", got, seg)
			}
		})
	}
}
