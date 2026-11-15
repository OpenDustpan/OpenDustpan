package collection_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// uncheckedRules accepts each write without validation.
type uncheckedRules struct {
	collection.PlacementStore
	writes int
	rules  []collection.Rule
}

func (s *uncheckedRules) ListPlacementRules(_ context.Context, _ namespace.Name) ([]collection.Rule, error) {
	return s.rules, nil
}

func TestPlacementServiceRefusesHostileStoredRule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edit func(*collection.Rule)
		want error
	}{
		{"collection name", func(r *collection.Rule) { r.CollectionID = "payments" }, collection.ErrInvalidName},
		{"SQL text", func(r *collection.Rule) { r.CollectionID = "col-a' OR '1'='1" }, collection.ErrInvalidName},
		{"foreign namespace", func(r *collection.Rule) { r.Namespace = "foreign" }, collection.ErrPlacementCollectionMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var r collection.Rule
			r.Namespace, r.Tool, r.Project, r.Stack, r.CollectionID = "acme", "pulumi", "*", "*", "col-payments"
			tc.edit(&r)
			store := &uncheckedRules{PlacementStore: nil, writes: 0, rules: []collection.Rule{r}}
			svc, err := collection.NewPlacementService(collection.PlacementConfig{
				Store: store, Namespaces: placementNamespaces{}, Now: nil,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.Place(t.Context(), "acme", "pulumi", "web", "prod"); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func (s *uncheckedRules) CreatePlacementRule(
	_ context.Context, _ collection.Rule, _ namespace.Fence, _ audit.Entry,
) error {
	s.writes++
	return nil
}

func (s *uncheckedRules) UpdatePlacementRule(
	_ context.Context, _ collection.Rule, _ namespace.Fence, _ audit.Entry,
) error {
	s.writes++
	return nil
}

func (*uncheckedRules) GetPlacementRule(_ context.Context, _ namespace.Name, _ string) (collection.Rule, error) {
	return collection.Rule{}, nil
}

type placementNamespaces struct {
	namespace.Store
}

func (placementNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	var n namespace.Namespace
	n.ID, n.Name, n.FailoverVersion = namespace.NewID(), name, 1
	return n, nil
}

func TestPlacementServiceRefusesHostileCollectionID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, id string
	}{
		{"empty", ""},
		{"name", "payments"},
		{"prefix only", "col-"},
		{"wrong prefix", "plr-payments"},
		{"SQL text", "col-a' OR '1'='1"},
		{"line break", "col-a\nb"},
		{"slash", "col-a/b"},
		{"too long", "col-" + strings.Repeat("a", 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &uncheckedRules{}
			svc, err := collection.NewPlacementService(collection.PlacementConfig{
				Store: store, Namespaces: placementNamespaces{}, Now: nil,
			})
			if err != nil {
				t.Fatal(err)
			}
			in := collection.RuleInput{
				Tool: collection.ToolPulumi, Project: "web", Stack: "*", CollectionID: tc.id, Position: 0,
			}
			for _, run := range []func() error{
				func() error { _, err := svc.Create(t.Context(), "acme", in, audit.Entry{}); return err },
				func() error { _, err := svc.Update(t.Context(), "acme", "plr-rule", in, audit.Entry{}); return err },
			} {
				if err := run(); !errors.Is(err, collection.ErrInvalidName) {
					t.Errorf("got %v, want ErrInvalidName", err)
				}
			}
			if store.writes != 0 {
				t.Errorf("invalid input made %d store writes, want 0", store.writes)
			}
		})
	}
}
