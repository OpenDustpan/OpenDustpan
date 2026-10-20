package manage_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/manage"
)

func TestRefValidate(t *testing.T) {
	t.Parallel()

	valid := manage.Ref{Kind: manage.KindCollection, Namespace: "payments", ID: "core-banking"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	for _, kind := range manage.Kinds() {
		ref := manage.Ref{Kind: kind, Namespace: "n", ID: "a.b_c-1"}
		if err := ref.Validate(); err != nil {
			t.Errorf("Validate(%s): %v", kind, err)
		}
	}

	hostile := []string{
		"", "..", "../x", "a/b", "a b", "a?b=c", "a#b", "a%2Fb", "a\r\nHost: x", "a\x00b", "-a", ".a",
		"a\u202eb", strings.Repeat("a", 129),
	}

	for _, name := range hostile {
		for _, ref := range []manage.Ref{
			{Kind: manage.KindCollection, Namespace: name, ID: "x"},
			{Kind: manage.KindCollection, Namespace: "x", ID: name},
		} {
			if err := ref.Validate(); !errors.Is(err, manage.ErrInvalidRef) {
				t.Errorf("Validate(%+v) error = %v, want ErrInvalidRef", ref, err)
			}
		}
	}

	for _, kind := range []manage.Kind{"", "unit", "Collection", "collection/x"} {
		ref := manage.Ref{Kind: kind, Namespace: "x", ID: "x"}
		if err := ref.Validate(); !errors.Is(err, manage.ErrInvalidRef) {
			t.Errorf("Validate(kind %q) error = %v, want ErrInvalidRef", kind, err)
		}
	}
}
