package savedview_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/savedview"
)

const validID = savedview.ID("view-0123456789abcdef0123456789abcdef")

func TestIDValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   savedview.ID
		ok   bool
	}{
		{"valid", validID, true},
		{"empty", "", false},
		{"no prefix", "0123456789abcdef0123456789abcdef", false},
		{"other prefix", "stk-0123456789abcdef0123456789abcdef", false},
		{"short", "view-0123", false},
		{"long", validID + "0", false},
		{"upper case", "view-0123456789ABCDEF0123456789abcdef", false},
		{"newline at the end", validID + "\n", false},
		{"path traversal", "view-../../../../etc/passwd", false},
		{"sql", "view-' or 1=1 --", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.id.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("Validate(%q) = %v, want ok=%v", tc.id, err, tc.ok)
			}

			if err != nil && !errors.Is(err, savedview.ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func labels(l ...savedview.Label) savedview.Query {
	return savedview.Query{Labels: l, References: nil}
}

func refs(r ...string) savedview.Query {
	return savedview.Query{Labels: nil, References: r}
}

func TestQueryValidate(t *testing.T) {
	t.Parallel()

	ns := namespace.Name("acme")
	many := func(n int, f func(i int) savedview.Label) []savedview.Label {
		out := make([]savedview.Label, n)
		for i := range out {
			out[i] = f(i)
		}

		return out
	}

	manyRefs := make([]string, savedview.MaxReferenceFilters+1)
	for i := range manyRefs {
		manyRefs[i] = "pulumi:acme/web/prod"
	}

	tests := []struct {
		name string
		q    savedview.Query
		ok   bool
	}{
		{"empty query", labels(), true},
		{"one label", labels(savedview.Label{Key: "team", Value: "payments"}), true},
		{"empty value", labels(savedview.Label{Key: "team", Value: ""}), true},
		{"value with equals sign", labels(savedview.Label{Key: "k", Value: "a=b"}), true},
		{"one reference", refs("pulumi:acme/web/prod"), true},
		{
			"repeated label, as the unit list accepts",
			labels(savedview.Label{Key: "team", Value: "a"}, savedview.Label{Key: "team", Value: "a"}),
			true,
		},
		{"repeated reference, as the unit list accepts", refs("pulumi:acme/web/prod", "pulumi:acme/web/prod"), true},
		{"empty key", labels(savedview.Label{Key: "", Value: "x"}), false},
		{"key with space", labels(savedview.Label{Key: "a b", Value: "x"}), false},
		{"key with equals sign", labels(savedview.Label{Key: "a=b", Value: "x"}), false},
		{"key too long", labels(savedview.Label{Key: strings.Repeat("k", 129), Value: ""}), false},
		{"value too long", labels(savedview.Label{Key: "k", Value: strings.Repeat("v", 257)}), false},
		{"value with control character", labels(savedview.Label{Key: "k", Value: "a\nb"}), false},
		{"value with NUL", labels(savedview.Label{Key: "k", Value: "a\x00b"}), false},
		{"value not UTF-8", labels(savedview.Label{Key: "k", Value: "\xff\xfe"}), false},
		{
			"too many labels",
			labels(many(savedview.MaxLabelFilters+1, func(i int) savedview.Label {
				return savedview.Label{Key: "k" + strings.Repeat("x", i), Value: ""}
			})...),
			false,
		},
		{"too many references", refs(manyRefs...), false},
		{"reference of another namespace", refs("pulumi:other/web/prod"), false},
		{"reference that is a namespace", refs("acme"), false},
		{"reference with a newline", refs("pulumi:acme/web/prod\n"), false},
		{"empty reference", refs(""), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.q.Validate(ns)
			if tc.ok != (err == nil) {
				t.Fatalf("Validate = %v, want ok=%v", err, tc.ok)
			}

			if err != nil && !errors.Is(err, savedview.ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func TestViewValidate(t *testing.T) {
	t.Parallel()

	good := func() savedview.View {
		return savedview.View{
			CreatedAt: time.UnixMilli(1),
			ID:        validID,
			Namespace: "acme",
			Name:      "Payments, production",
			Owner:     "prn-0123456789abcdefghij",
			Query:     labels(savedview.Label{Key: "team", Value: "payments"}),
		}
	}

	tests := []struct {
		name   string
		change func(v *savedview.View)
		ok     bool
	}{
		{"valid", func(*savedview.View) {}, true},
		{"unicode name", func(v *savedview.View) { v.Name = "Équipe façade" }, true},
		{
			"name of the largest length",
			func(v *savedview.View) { v.Name = strings.Repeat("n", savedview.MaxNameLen) },
			true,
		},
		{"empty name", func(v *savedview.View) { v.Name = "" }, false},
		{"name too long", func(v *savedview.View) { v.Name = strings.Repeat("n", savedview.MaxNameLen+1) }, false},
		{"name with a leading space", func(v *savedview.View) { v.Name = " a" }, false},
		{"name with a trailing newline", func(v *savedview.View) { v.Name = "a\n" }, false},
		{"name with a tab inside", func(v *savedview.View) { v.Name = "a\tb" }, false},
		{"name with a right-to-left override", func(v *savedview.View) { v.Name = "a\u202eb" }, false},
		{"name with a line separator", func(v *savedview.View) { v.Name = "a\u2028b" }, false},
		{"name with a zero width space", func(v *savedview.View) { v.Name = "a\u200bb" }, false},
		{"name with a paragraph separator", func(v *savedview.View) { v.Name = "a\u2029b" }, false},
		{"name not UTF-8", func(v *savedview.View) { v.Name = "\xff" }, false},
		{"owner with a right-to-left override", func(v *savedview.View) { v.Owner = "prn-a\u202eb" }, false},
		{"owner with a line separator", func(v *savedview.View) { v.Owner = "prn-a\u2028b" }, false},
		{"owner with a zero width space", func(v *savedview.View) { v.Owner = "prn-a\u200bb" }, false},
		{"owner is a subject", func(v *savedview.View) { v.Owner = "user:alice" }, false},
		{"owner is a group ID", func(v *savedview.View) { v.Owner = "grp-0123456789abcdefghij" }, false},
		{"owner is the prefix only", func(v *savedview.View) { v.Owner = principal.IDPrefix }, false},
		{"owner too long", func(v *savedview.View) { v.Owner = "prn-" + strings.Repeat("a", 61) }, false},
		{"owner of the largest length", func(v *savedview.View) { v.Owner = "prn-" + strings.Repeat("a", 60) }, true},
		{"bad ID", func(v *savedview.View) { v.ID = "x" }, false},
		{"no owner", func(v *savedview.View) { v.Owner = "" }, false},
		{"owner with a control character", func(v *savedview.View) { v.Owner = "prn-a\r\nb" }, false},
		{"bad query", func(v *savedview.View) { v.Query.References = []string{"pulumi:other/a/b"} }, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := good()
			tc.change(&v)

			err := v.Validate()
			if tc.ok != (err == nil) {
				t.Fatalf("Validate = %v, want ok=%v", err, tc.ok)
			}

			if err != nil && !errors.Is(err, savedview.ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}
