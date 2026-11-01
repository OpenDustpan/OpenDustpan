package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// maxPlacementBodyBytes bounds the body of a placement rule write. The body
// holds a tool, two patterns of at most 64 characters, a collection name and a
// position, so this is ample.
const maxPlacementBodyBytes = 2048

// maxRuleIDLength is the most characters of a rule ID in a path.
const maxRuleIDLength = 64

// errPlacementBody names the body that a placement rule write needs. Its text
// is the error of the 400 answer.
var errPlacementBody = errors.New(
	`invalid placement rule body: provide {"tool":"pulumi","project":"<pattern>","stack":"<pattern>",` +
		`"collection":"<collection name>"} and optionally "position"`,
)

// errPlacementRuleID reports a rule ID in the path that has an invalid form.
var errPlacementRuleID = errors.New("invalid placement rule ID: use the ID that the list of rules gives")

// placementRoutes returns the placement rule rows of the route table. A read
// needs namespace:read-placement-rules and a write needs
// namespace:manage-placement-rules, both on the namespace. A rule only names a
// collection: the caller who makes the unit still needs collection:create-unit
// on it.
func (h *Handler) placementRoutes() []Route {
	read := []identity.Action{identity.ActionNamespaceReadPlacementRules}
	manage := []identity.Action{identity.ActionNamespaceManagePlacementRules}
	list := v1Prefix + "/namespaces/{namespace}/placement-rules"
	one := list + "/{rule}"

	row := func(method, path string, actions []identity.Action, resolve ObjectResolver, serve RouteHandler) Route {
		return Route{
			Method:           method,
			Path:             path,
			Scope:            route.ScopeAuthorized,
			Actions:          actions,
			Resolve:          resolve,
			Serve:            serve,
			NamespaceActions: nil,
			DeniedAsNotFound: false,
		}
	}

	return []Route{
		row(http.MethodGet, list, read, resolveCollectionNamespace, h.listPlacementRules),
		row(http.MethodPost, list, manage, resolveCollectionNamespace, h.createPlacementRule),
		row(http.MethodGet, one, read, resolvePlacementRule, h.readPlacementRule),
		row(http.MethodPut, one, manage, resolvePlacementRule, h.updatePlacementRule),
		row(http.MethodDelete, one, manage, resolvePlacementRule, h.deletePlacementRule),
	}
}

// placementRuleEntry is one placement rule as the console reads it. Position
// runs from 1, and the first rule that matches a new unit wins.
//
// Contract: never remove or rename these fields.
type placementRuleEntry struct {
	ID         string `json:"id"`
	Namespace  string `json:"namespace"`
	Position   int    `json:"position"`
	Tool       string `json:"tool"`
	Project    string `json:"project"`
	Stack      string `json:"stack"`
	Collection string `json:"collection"`
	CreatedBy  string `json:"createdBy"`
	CreatedAt  int64  `json:"createdAt"`
}

// placementRulesResponse is the answered document of the rule list.
//
// Contract: never remove or rename the rules field.
type placementRulesResponse struct {
	Rules []placementRuleEntry `json:"rules"`
}

// placementRuleRequest is the body of a rule create or update. The fields are
// pointers so that a missing field differs from an empty one.
type placementRuleRequest struct {
	Tool       *string `dustpan:"required" json:"tool"`
	Project    *string `dustpan:"required" json:"project"`
	Stack      *string `dustpan:"required" json:"stack"`
	Collection *string `dustpan:"required" json:"collection"`
	Position   *int    `dustpan:"optional" json:"position"`
}

// resolvePlacementRule returns the namespace of the path as the object, and
// checks the form of the rule ID in the path before authorization runs.
func resolvePlacementRule(w http.ResponseWriter, r *http.Request) (identity.Object, error) {
	obj, err := resolveCollectionNamespace(w, r)
	if err != nil {
		return identity.Object{}, err
	}

	if !ident.Valid(r.PathValue("rule"), maxRuleIDLength) {
		return identity.Object{}, errPlacementRuleID
	}

	return obj, nil
}

// listPlacementRules serves GET /api/dustpan/v1/namespaces/{namespace}/placement-rules.
// It lists the rules in list order. An unknown namespace answers 404.
func (h *Handler) listPlacementRules(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	rules, err := h.rulesOfNamespace(r.Context(), obj.Namespace())
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	h.writeJSON(r.Context(), w, http.StatusOK, placementRulesResponse{Rules: placementEntries(rules)})
}

// rulesOfNamespace reads the rules of ns. It returns the error of the
// namespace service first, so a missing namespace is not an empty list.
func (h *Handler) rulesOfNamespace(ctx context.Context, ns namespace.Name) ([]collection.Rule, error) {
	if _, err := h.namespaces.Get(ctx, ns); err != nil {
		return nil, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	rules, err := h.placements.List(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list placement rules: %w", err)
	}

	return rules, nil
}

// createPlacementRule serves POST on the rule list. It answers 201 with the
// rule. A rule that an earlier rule hides, or that hides a later rule, answers
// 409 and is not stored. The write and its audit entry are one transaction in
// the store.
func (h *Handler) createPlacementRule(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	in, err := readPlacementRule(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rule, err := h.placements.Create(ctx, obj.Namespace(), in,
		auditEntry(r, p, obj, identity.ActionNamespaceManagePlacementRules, ""))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, placementRuleOf(&rule))
}

// readPlacementRule serves GET on one rule. A rule that the namespace does not
// hold answers 404.
func (h *Handler) readPlacementRule(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	rule, err := h.placements.Get(ctx, obj.Namespace(), r.PathValue("rule"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, placementRuleOf(&rule))
}

// updatePlacementRule serves PUT on one rule. The body replaces the tool, the
// patterns and the collection. A missing position keeps the place of the rule.
// It answers 200 with the rule. It does not change the units that exist.
func (h *Handler) updatePlacementRule(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	in, err := readPlacementRule(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rule, err := h.placements.Update(ctx, obj.Namespace(), r.PathValue("rule"), in,
		auditEntry(r, p, obj, identity.ActionNamespaceManagePlacementRules, ""))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, placementRuleOf(&rule))
}

// deletePlacementRule serves DELETE on one rule. It answers 204. The units
// that the rule placed keep their collections.
func (h *Handler) deletePlacementRule(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	err := h.placements.Delete(ctx, obj.Namespace(), r.PathValue("rule"),
		auditEntry(r, p, obj, identity.ActionNamespaceManagePlacementRules, ""))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// readPlacementRule decodes the body of a rule write. It refuses an unknown
// field, a body that is not one object, and a body without a required field.
// It parses the patterns and the collection name, so an invalid value fails
// before the service reads the store.
func readPlacementRule(w http.ResponseWriter, r *http.Request) (collection.RuleInput, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPlacementBodyBytes))
	if err != nil {
		return collection.RuleInput{}, errPlacementBody
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in placementRuleRequest

	if decErr := dec.Decode(&in); decErr != nil {
		return collection.RuleInput{}, errPlacementBody
	}

	if decErr := dec.Decode(&struct{}{}); !errors.Is(decErr, io.EOF) {
		return collection.RuleInput{}, errPlacementBody
	}

	if in.Tool == nil || in.Project == nil || in.Stack == nil || in.Collection == nil {
		return collection.RuleInput{}, errPlacementBody
	}

	return placementInput(&in)
}

// placementInput parses the fields of a decoded body. All pointers are set.
func placementInput(in *placementRuleRequest) (collection.RuleInput, error) {
	project, err := collection.ParsePattern(*in.Project)
	if err != nil {
		return collection.RuleInput{}, fmt.Errorf("project: %w", err)
	}

	stack, err := collection.ParsePattern(*in.Stack)
	if err != nil {
		return collection.RuleInput{}, fmt.Errorf("stack: %w", err)
	}

	name, err := collection.ParseName(*in.Collection)
	if err != nil {
		return collection.RuleInput{}, fmt.Errorf("collection: %w", err)
	}

	out := collection.RuleInput{Tool: *in.Tool, Project: project, Stack: stack, Collection: name, Position: 0}
	if in.Position != nil {
		out.Position = *in.Position
	}

	return out, nil
}

// placementEntries maps the rules to their wire shape, in the same order.
func placementEntries(rules []collection.Rule) []placementRuleEntry {
	out := make([]placementRuleEntry, 0, len(rules))
	for i := range rules {
		out = append(out, placementRuleOf(&rules[i]))
	}

	return out
}

// placementRuleOf maps a domain rule to its wire shape.
func placementRuleOf(r *collection.Rule) placementRuleEntry {
	return placementRuleEntry{
		ID:         r.ID,
		Namespace:  r.Namespace.String(),
		Position:   r.Position,
		Tool:       r.Tool,
		Project:    r.Project.String(),
		Stack:      r.Stack.String(),
		Collection: r.Collection.String(),
		CreatedBy:  r.CreatedBy,
		CreatedAt:  r.CreatedAt.Unix(),
	}
}
