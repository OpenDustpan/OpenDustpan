package collection_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
)

func TestParsePattern(t *testing.T) {
	t.Parallel()

	valid := []string{
		"payments", "*", "prod*", "a*b", "*a*b*", "my.app_v2-x", strings.Repeat("a", collection.MaxPatternLength),
	}
	for _, in := range valid {
		got, err := collection.ParsePattern(in)
		if err != nil || got.String() != in {
			t.Errorf("ParsePattern(%q) = %q, %v, want the same pattern", in, got, err)
		}
	}
}

func TestParsePatternRejectsHostileInput(t *testing.T) {
	t.Parallel()

	invalid := map[string]string{
		"empty":           "",
		"too long":        strings.Repeat("a", collection.MaxPatternLength+1),
		"two stars":       "a**",
		"four stars":      "*a*b*c*",
		"regexp group":    "(a+)+$",
		"regexp class":    "[a-z]+",
		"question mark":   "a?",
		"slash":           "a/b",
		"backslash":       `a\*`,
		"space":           "a b",
		"line feed":       "a\nb",
		"carriage return": "a\r",
		"null byte":       "a\x00",
		"unicode letter":  "caf\u00e9",
		"unicode dash":    "a\u2011b",
		"template":        "${secret}",
	}
	for name, in := range invalid {
		got, err := collection.ParsePattern(in)
		if !errors.Is(err, collection.ErrInvalidPattern) || got != "" {
			t.Errorf("%s: ParsePattern(%q) = %q, %v, want empty and ErrInvalidPattern", name, in, got, err)
		}
	}
}

func TestPatternMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"web", "web", true},
		{"web", "Web", false},
		{"web", "webs", false},
		{"web", "", false},
		{"*", "", true},
		{"*", "anything-at.all", true},
		{"prod*", "prod", true},
		{"prod*", "prod-eu", true},
		{"prod*", "preprod", false},
		{"*prod", "eu-prod", true},
		{"*prod", "prod-eu", false},
		{"a*b", "ab", true},
		{"a*b", "axxb", true},
		{"a*b", "axxbx", false},
		{"*a*b*", "xxaxxbxx", true},
		{"*a*b*", "xxbxxaxx", false},
		{"a*a*a", "aaa", true},
		{"a*a*a", "aa", false},
		{"*.yaml", "x.yaml", true},
		{"a.b", "axb", false},
		{"*", strings.Repeat("a", collection.MaxNameLength), true},
		{"*", strings.Repeat("a", collection.MaxNameLength+1), false},
		{"*", "a\nb", true},
	}

	for _, tt := range tests {
		p, err := collection.ParsePattern(tt.pattern)
		if err != nil {
			t.Fatalf("ParsePattern(%q): %v", tt.pattern, err)
		}

		if got := p.Matches(tt.name); got != tt.want {
			t.Errorf("%q matches %q = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

// TestPatternMatchesIsBounded sends the names that make a backtracking
// regexp matcher run for a very long time. The glob matcher must finish in
// time that is bounded by the length of the pattern times the length of the
// name.
func TestPatternMatchesIsBounded(t *testing.T) {
	t.Parallel()

	hostile := strings.Repeat("a", collection.MaxNameLength-1) + "b"
	patterns := []string{"*a*a*a", "a*a*a*", "*a*a*b", "*aaaaaaaaaaaaaaaaaaaaaaaaaaaa*c*"}

	start := time.Now()

	for range 2000 {
		for _, s := range patterns {
			p, err := collection.ParsePattern(s)
			if err != nil {
				t.Fatalf("ParsePattern(%q): %v", s, err)
			}

			_ = p.Matches(hostile)
		}
	}

	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("8000 hostile matches took %v, want a bounded run time", elapsed)
	}
}

// TestPatternMatchesBuiltWithoutParse sends a value that never passed
// ParsePattern. The matcher must not panic and must not treat the text as a
// regular expression.
func TestPatternMatchesBuiltWithoutParse(t *testing.T) {
	t.Parallel()

	for _, p := range []collection.Pattern{
		"(a+)+$", ".*", "[a-z]", "a\x00", collection.Pattern(strings.Repeat("*", 10_000)),
	} {
		if p.Matches("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!") && p != ".*" {
			t.Errorf("pattern %q matched, want no match", string(p)[:min(len(p), 20)])
		}
	}

	if collection.Pattern(".*").Matches("abc") {
		t.Error(`pattern ".*" matched "abc" as a regular expression`)
	}
}

func TestPatternCovers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wide, narrow string
		want         bool
	}{
		{"*", "*", true},
		{"*", "prod", true},
		{"prod*", "prod-eu*", true},
		{"prod*", "prod", true},
		{"prod-eu*", "prod*", false},
		{"prod", "prod*", false},
		{"prod", "prod", true},
		{"a*c", "a*b*c", true},
		{"a*b*c", "a*c", false},
		{"*a*", "*b*", false},
		{"*a*", "x*a*y", true},
	}

	for _, tt := range tests {
		w, wErr := collection.ParsePattern(tt.wide)
		n, nErr := collection.ParsePattern(tt.narrow)

		if wErr != nil || nErr != nil {
			t.Fatalf("ParsePattern: %v, %v", wErr, nErr)
		}

		if got := w.Covers(n); got != tt.want {
			t.Errorf("%q covers %q = %v, want %v", tt.wide, tt.narrow, got, tt.want)
		}
	}
}

func rule(t *testing.T, project, stack, coll string) collection.Rule {
	t.Helper()

	name, err := collection.ParseName(coll)
	if err != nil {
		t.Fatalf("ParseName(%q): %v", coll, err)
	}

	return collection.Rule{
		CreatedAt: time.Time{}, ID: "", Namespace: "", CreatedBy: "",
		Tool: collection.ToolPulumi, Project: collection.Pattern(project), Stack: collection.Pattern(stack),
		Collection: name, Position: 1,
	}
}

func TestFirstMatch(t *testing.T) {
	t.Parallel()

	rules := []collection.Rule{
		rule(t, "payments", "prod*", "payments-prod"),
		rule(t, "payments", "*", "payments"),
		rule(t, "*", "dev", "sandbox"),
	}

	tests := []struct {
		name                 string
		tool, project, stack string
		wantCollection       string
		wantMatch            bool
	}{
		{"specific rule first", "pulumi", "payments", "prod-eu", "payments-prod", true},
		{"general rule after", "pulumi", "payments", "dev", "payments", true},
		{"third rule", "pulumi", "web", "dev", "sandbox", true},
		{"no match", "pulumi", "web", "prod", "", false},
		{"other tool", "terraform", "payments", "prod", "", false},
		{"project is case-sensitive", "pulumi", "Payments", "prod", "", false},
		{"empty names", "pulumi", "", "", "", false},
		{"name over the limit", "pulumi", strings.Repeat("a", collection.MaxNameLength+1), "dev", "", false},
	}

	for _, tt := range tests {
		got, ok := collection.FirstMatch(rules, tt.tool, tt.project, tt.stack)
		if ok != tt.wantMatch || got.Collection.String() != tt.wantCollection {
			t.Errorf("%s: FirstMatch = %q, %v, want %q, %v",
				tt.name, got.Collection, ok, tt.wantCollection, tt.wantMatch)
		}
	}

	if _, ok := collection.FirstMatch(nil, "pulumi", "a", "b"); ok {
		t.Error("FirstMatch of no rules matched")
	}
}

func TestRuleHides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		first, later collection.Rule
		want         bool
	}{
		{"same patterns", rule(t, "a", "b", "one"), rule(t, "a", "b", "two"), true},
		{"wider first", rule(t, "a", "*", "one"), rule(t, "a", "prod*", "two"), true},
		{"narrower first", rule(t, "a", "prod*", "one"), rule(t, "a", "*", "two"), false},
		{"only the stack is wider", rule(t, "a", "*", "one"), rule(t, "*", "x", "two"), false},
		{"disjoint", rule(t, "a", "x", "one"), rule(t, "b", "x", "two"), false},
	}

	for _, tt := range tests {
		if got := tt.first.Hides(tt.later); got != tt.want {
			t.Errorf("%s: Hides = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRuleValidate(t *testing.T) {
	t.Parallel()

	good := rule(t, "a", "*", "payments")
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate of a good rule: %v", err)
	}

	tests := []struct {
		name string
		edit func(r *collection.Rule)
		want error
	}{
		{"tool", func(r *collection.Rule) { r.Tool = "terraform" }, collection.ErrInvalidPlacementTool},
		{"empty tool", func(r *collection.Rule) { r.Tool = "" }, collection.ErrInvalidPlacementTool},
		{"project", func(r *collection.Rule) { r.Project = "a b" }, collection.ErrInvalidPattern},
		{"stack", func(r *collection.Rule) { r.Stack = "" }, collection.ErrInvalidPattern},
		{"collection", func(r *collection.Rule) { r.Collection = "-x-" }, collection.ErrInvalidName},
		{"position", func(r *collection.Rule) { r.Position = -1 }, collection.ErrInvalidPosition},
	}

	for _, tt := range tests {
		r := good
		tt.edit(&r)

		if err := r.Validate(); !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestPlacementErrorText(t *testing.T) {
	t.Parallel()

	err := &collection.PlacementError{
		Namespace: "acme", Tool: "pulumi", Project: "pay\nments\x1b[31m", Stack: strings.Repeat("s", 200),
	}

	if !errors.Is(err, collection.ErrNoPlacement) {
		t.Error("PlacementError does not match ErrNoPlacement")
	}

	text := err.Error()
	if strings.ContainsAny(text, "\n\r\x1b") {
		t.Errorf("error text has a control character: %q", text)
	}

	if len(text) > 400 {
		t.Errorf("error text has %d bytes, want a bounded text", len(text))
	}

	for _, want := range []string{"acme", "placement-rules"} {
		if !strings.Contains(text, want) {
			t.Errorf("error text %q lacks %q", text, want)
		}
	}
}
