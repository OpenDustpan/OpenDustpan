package pulumijournal_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// URNs of the normalization tests. The pulumi:providers module is needed for
// the provider rules, and the old URNs are the ones that an alias names.
const (
	urnOldA       = "urn:pulumi:dev::app::pkg:mod:Old::a"
	urnNewA       = "urn:pulumi:dev::app::pkg:mod:New::a"
	urnOldParent  = "urn:pulumi:dev::app::pkg:mod:OldParent::p"
	urnNewParent  = "urn:pulumi:dev::app::pkg:mod:NewParent::p"
	urnChild      = "urn:pulumi:dev::app::pkg:mod:Child::c"
	urnChildMoved = "urn:pulumi:dev::app::pkg:mod:NewParent$pkg:mod:Child::c"
	urnProvider   = "urn:pulumi:dev::app::pulumi:providers:aws::default"
	urnOldProv    = "urn:pulumi:dev::app::pulumi:providers:aws::old-default"
	providerID    = "7d4f0b8e"
	unknownID     = "04da6b54-80e4-46f7-96ec-b56ff0331ba9"
)

// aliased returns a resource state that declares the aliases.
func aliased(urn string, aliases ...string) string {
	list := make([]string, 0, len(aliases))
	for _, a := range aliases {
		list = append(list, `"`+a+`"`)
	}
	return `{"urn":"` + urn + `","custom":true,"type":"t:mod:T","id":"x","aliases":[` +
		strings.Join(list, ",") + `]}`
}

// TestReplayNormalizesAliasedReferences checks that a reference to an alias becomes
// the URN of the resource that declares it, and that the aliases are removed.
func TestReplayNormalizesAliasedReferences(t *testing.T) {
	t.Parallel()
	dependent := `{"urn":"urn:b","custom":true,"type":"t:mod:T","dependencies":["` + urnOldA + `"]}`
	base := baseJSON(aliased(urnNewA, urnOldA), dependent)
	got, err := replay(base, batch())
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got,
		`{"urn":"`+urnNewA+`","custom":true,"type":"t:mod:T","id":"x"},`+
			`{"urn":"urn:b","custom":true,"type":"t:mod:T","dependencies":["`+urnNewA+`"]}`)
}

// TestReplayNormalizesAliasedParent checks that a child whose parent is an alias
// gets the parent's type in its URN, and that its parent is the new URN.
func TestReplayNormalizesAliasedParent(t *testing.T) {
	t.Parallel()
	parent := aliased(urnNewParent, urnOldParent)
	child := `{"urn":"` + urnChild + `","custom":true,"type":"t:mod:T","parent":"` + urnOldParent + `"}`
	base := baseJSON(parent, child)
	got, err := replay(base, batch())
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got,
		`{"urn":"`+urnNewParent+`","custom":true,"type":"t:mod:T","id":"x"},`+
			`{"urn":"`+urnChildMoved+`","custom":true,"type":"t:mod:T","parent":"`+urnNewParent+`"}`)
}

// TestReplayKeepsURNOfChildOfRootStack checks that a parent of the root stack type
// does not change the URN of its child.
func TestReplayKeepsURNOfChildOfRootStack(t *testing.T) {
	t.Parallel()
	const stack = "urn:pulumi:dev::app::pulumi:pulumi:Stack::app"
	const oldStack = "urn:pulumi:dev::app::pulumi:pulumi:Stack::old-app"
	root := aliased(stack, oldStack)
	child := `{"urn":"` + urnChild + `","custom":true,"type":"t:mod:T","parent":"` + oldStack + `"}`
	base := baseJSON(root, child)
	got, err := replay(base, batch())
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got,
		`{"urn":"`+stack+`","custom":true,"type":"t:mod:T","id":"x"},`+
			`{"urn":"`+urnChild+`","custom":true,"type":"t:mod:T","parent":"`+stack+`"}`)
}

// TestReplayNormalizesProvider checks that a provider reference keeps its ID and
// gets the URN of the provider that declares the alias.
func TestReplayNormalizesProvider(t *testing.T) {
	t.Parallel()
	provider := aliased(urnProvider, urnOldProv)
	user := `{"urn":"urn:b","custom":true,"type":"t:mod:T","provider":"` + urnOldProv + `::` + providerID + `"}`
	base := baseJSON(provider, user)
	got, err := replay(base, batch())
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got,
		`{"urn":"`+urnProvider+`","custom":true,"type":"t:mod:T","id":"x"},`+
			`{"urn":"urn:b","custom":true,"type":"t:mod:T","provider":"`+urnProvider+`::`+providerID+`"}`)
}

// TestReplayGivesProviderEmptyIDTheUnknownID checks that a provider reference with
// no ID gets the unknown ID, as upstream providers.NewReference does.
func TestReplayGivesProviderEmptyIDTheUnknownID(t *testing.T) {
	t.Parallel()
	user := `{"urn":"urn:b","custom":true,"type":"t:mod:T","provider":"` + urnProvider + `::"}`
	got, err := replay(baseJSON(user), batch())
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got,
		`{"urn":"urn:b","custom":true,"type":"t:mod:T","provider":"`+urnProvider+`::`+unknownID+`"}`)
}

// TestReplayNormalizesPropertyDependenciesAndLinks checks the rewrite of the
// property dependencies, deletedWith and replaceWith.
func TestReplayNormalizesPropertyDependenciesAndLinks(t *testing.T) {
	t.Parallel()
	target := aliased(urnNewA, urnOldA)
	user := `{"urn":"urn:b","custom":true,"type":"t:mod:T",` +
		`"propertyDependencies":{"k":["` + urnOldA + `"]},` +
		`"deletedWith":"` + urnOldA + `","replaceWith":["` + urnOldA + `"]}`
	got, err := replay(baseJSON(target, user), batch())
	if err != nil {
		t.Fatal(err)
	}
	expectReplaceWithDeployment(t, got,
		`{"urn":"`+urnNewA+`","custom":true,"type":"t:mod:T","id":"x"},`+
			`{"urn":"urn:b","custom":true,"type":"t:mod:T",`+
			`"propertyDependencies":{"k":["`+urnNewA+`"]},`+
			`"deletedWith":"`+urnNewA+`","replaceWith":["`+urnNewA+`"]}`)
}

// expectReplaceWithDeployment checks the output of a state with replaceWith, which
// sets the replaceWith feature and the latest schema version.
func expectReplaceWithDeployment(t *testing.T, got, resources string) {
	t.Helper()
	want := `{"version":4,"features":["replaceWith"],"deployment":{"manifest":{"time":"` + replayTime +
		`","magic":"` + magic() + `","version":"` + cliVersion + `"},"resources":[` + resources + `]}}`
	if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, want)) {
		t.Fatalf("output is\n%s\nwant\n%s", got, want)
	}
}

// TestReplayNormalizesSnippetReferences checks that a snippet reference to an
// alias is rewritten, and that the other members of the snippet keep their values.
func TestReplayNormalizesSnippetReferences(t *testing.T) {
	t.Parallel()
	target := aliased(urnNewA, urnOldA)
	snippet := `{"uuid":"s1","code":"x","references":{"ref":"` + urnOldA + `","other":"urn:unrelated"}}`
	base := `{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[` +
		target + `],"snippets":[` + snippet + `]}`
	got, err := replay(base, batch())
	if err != nil {
		t.Fatal(err)
	}
	want := `"references":{"other":"urn:unrelated","ref":"` + urnNewA + `"}`
	if !strings.Contains(got, want) {
		t.Fatalf("output does not hold %s:\n%s", want, got)
	}
	if strings.Contains(got, urnOldA) {
		t.Fatalf("output still holds the alias %s:\n%s", urnOldA, got)
	}
}

// TestReplayRefusesAliasConflict checks that two resources with one alias and
// different URNs are an error, and that nothing is written.
func TestReplayRefusesAliasConflict(t *testing.T) {
	t.Parallel()
	base := baseJSON(aliased(urnNewA, urnOldA), aliased("urn:b", urnOldA))
	got, err := replay(base, batch())
	if !errors.Is(err, pulumijournal.ErrAliasConflict) {
		t.Fatalf("error is %v, want ErrAliasConflict", err)
	}
	if got != "" {
		t.Fatalf("output is not empty on error:\n%s", got)
	}
}

// TestReplayRefusesInvalidURNOfAliasedChild checks that a child whose parent is an
// alias must have a valid URN. Upstream panics on it.
func TestReplayRefusesInvalidURNOfAliasedChild(t *testing.T) {
	t.Parallel()
	parent := aliased(urnNewParent, urnOldParent)
	child := `{"urn":"not-a-urn","custom":true,"type":"t:mod:T","parent":"` + urnOldParent + `"}`
	_, err := replay(baseJSON(parent, child), batch())
	if !errors.Is(err, pulumijournal.ErrInvalidURN) {
		t.Fatalf("error is %v, want ErrInvalidURN", err)
	}
}

// TestReplayRefusesMalformedProvider checks that a provider reference without the
// name delimiter is an error. Upstream asserts on it.
func TestReplayRefusesMalformedProvider(t *testing.T) {
	t.Parallel()
	user := `{"urn":"urn:b","custom":true,"type":"t:mod:T","provider":"no-delimiter"}`
	_, err := replay(baseJSON(user), batch())
	if !errors.Is(err, pulumijournal.ErrInvalidReference) {
		t.Fatalf("error is %v, want ErrInvalidReference", err)
	}
}
