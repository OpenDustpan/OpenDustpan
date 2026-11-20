package viewstore_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/store/internal/viewstore"
)

// TestCheckCounts proves the check of the saved view limits: a count at a
// limit passes, and a count over a limit fails with ErrOverLimit and a text
// that names the owner or the namespace, the count and the limit.
func TestCheckCounts(t *testing.T) {
	t.Parallel()

	v := savedview.View{
		CreatedAt: time.UnixMilli(1).UTC(), ID: savedview.NewID(), Namespace: "acme", Name: "Payments",
		Owner: "prn-aaaaaaaaaaaaaaaaaaaa", Query: savedview.Query{Labels: nil, References: nil},
	}

	cases := []struct {
		name        string
		want        string
		ofOwner     int64
		inNamespace int64
	}{
		{
			name: "both counts at the limit", ofOwner: savedview.MaxPerOwner, inNamespace: savedview.MaxPerNamespace,
			want: "",
		},
		{
			name: "owner over the limit", ofOwner: savedview.MaxPerOwner + 1, inNamespace: savedview.MaxPerOwner + 1,
			want: `owner "prn-aaaaaaaaaaaaaaaaaaaa" has 101 saved views in namespace "acme", the limit is 100`,
		},
		{
			name: "namespace over the limit", ofOwner: 1, inNamespace: savedview.MaxPerNamespace + 1,
			want: `namespace "acme" has 10001 saved views, the limit is 10000`,
		},
		{
			name: "both over the limit", ofOwner: savedview.MaxPerOwner + 1, inNamespace: savedview.MaxPerNamespace + 1,
			want: `owner "prn-aaaaaaaaaaaaaaaaaaaa" has 101 saved views`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := viewstore.CheckCounts(v, tc.ofOwner, tc.inNamespace)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("check = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, viewstore.ErrOverLimit) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("check = %v, want ErrOverLimit with %q", err, tc.want)
			}
		})
	}
}

// TestViewRefusesTextAfterTheQuery proves that a stored query with text after
// the document is a corrupt row. The text can be a second document, a stray
// closing brace or a stray closing bracket. The check of the row does not
// depend on any other field, so the row has none.
func TestViewRefusesTextAfterTheQuery(t *testing.T) {
	t.Parallel()

	for _, query := range []string{`{}}`, `{}]`, `{} {}`, `{}x`, `{"labels":[]}}`} {
		row := viewstore.Row{ID: "", Namespace: "", Owner: "", Name: "", QueryJSON: query, CreatedAtMs: 0}

		_, err := viewstore.View(row)
		if !errors.Is(err, viewstore.ErrCorrupt) {
			t.Errorf("query %q: err = %v, want ErrCorrupt", query, err)

			continue
		}

		if !strings.Contains(err.Error(), "text after the document") {
			t.Errorf("query %q: err = %v, want the text-after-the-document refusal", query, err)
		}
	}
}
