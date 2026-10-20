package viewstore_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/viewstore"
)

// TestViewRefusesTextAfterTheQuery proves that a stored query with text after
// the document is a corrupt row. The text can be a second document, a stray
// closing brace or a stray closing bracket. The check of the row does not
// depend on any other field, so the row has none.
func TestViewRefusesTextAfterTheQuery(t *testing.T) {
	t.Parallel()

	for _, query := range []string{`{}}`, `{}]`, `{} {}`, `{}x`, `{"labels":[]}}`} {
		_, err := viewstore.View(viewstore.Row{QueryJSON: query})
		if !errors.Is(err, viewstore.ErrCorrupt) {
			t.Errorf("query %q: err = %v, want ErrCorrupt", query, err)

			continue
		}

		if !strings.Contains(err.Error(), "text after the document") {
			t.Errorf("query %q: err = %v, want the text-after-the-document refusal", query, err)
		}
	}
}
