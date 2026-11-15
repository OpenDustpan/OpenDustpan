package inventory_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memPlacements is a PlacementStore in memory. It knows the collections
// payments and sandbox, and it keeps the audit entries of its writes.
type memPlacements struct {
	mu      sync.Mutex
	rules   []collection.Rule
	entries []audit.Entry
}

func (m *memPlacements) CreatePlacementRule(
	_ context.Context,
	r collection.Rule,
	_ namespace.Fence,
	e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r.Collection.Key() != "payments" && r.Collection.Key() != "sandbox" {
		return collection.ErrPlacementCollectionMissing
	}

	if err := r.Validate(); err != nil {
		return err
	}

	if r.Position == 0 {
		r.Position = len(m.rules) + 1
	}

	if r.Position < 1 || r.Position > len(m.rules)+1 {
		return collection.ErrInvalidPosition
	}

	next := slices.Insert(slices.Clone(m.rules), r.Position-1, r)
	for i := range next {
		next[i].Position = i + 1
	}

	if err := collection.CheckPlacementOrder(next, r.ID); err != nil {
		return err
	}

	m.rules = next
	m.entries = append(m.entries, e)

	return nil
}

func (m *memPlacements) UpdatePlacementRule(
	_ context.Context,
	r collection.Rule,
	_ namespace.Fence,
	e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	i := slices.IndexFunc(m.rules, func(x collection.Rule) bool { return x.ID == r.ID })
	if i < 0 {
		return collection.ErrPlacementRuleNotFound
	}

	if r.Position == 0 {
		r.Position = i + 1
	}

	if r.Position < 1 || r.Position > len(m.rules) {
		return collection.ErrInvalidPosition
	}

	r.CreatedAt, r.CreatedBy = m.rules[i].CreatedAt, m.rules[i].CreatedBy
	next := slices.Insert(slices.Delete(slices.Clone(m.rules), i, i+1), r.Position-1, r)

	for j := range next {
		next[j].Position = j + 1
	}

	if err := collection.CheckPlacementOrder(next, r.ID); err != nil {
		return err
	}

	m.rules = next
	m.entries = append(m.entries, e)

	return nil
}

func (m *memPlacements) DeletePlacementRule(
	_ context.Context,
	_ namespace.Name,
	id string,
	_ namespace.Fence,
	e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	i := slices.IndexFunc(m.rules, func(x collection.Rule) bool { return x.ID == id })
	if i < 0 {
		return collection.ErrPlacementRuleNotFound
	}

	m.rules = slices.Delete(m.rules, i, i+1)
	m.renumber()
	m.entries = append(m.entries, e)

	return nil
}

func (m *memPlacements) GetPlacementRule(_ context.Context, _ namespace.Name, id string) (collection.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	i := slices.IndexFunc(m.rules, func(x collection.Rule) bool { return x.ID == id })
	if i < 0 {
		return collection.Rule{}, collection.ErrPlacementRuleNotFound
	}

	return m.rules[i], nil
}

func (m *memPlacements) ListPlacementRules(_ context.Context, _ namespace.Name) ([]collection.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.rules), nil
}

func (m *memPlacements) renumber() {
	for i := range m.rules {
		m.rules[i].Position = i + 1
	}
}

// newPlacementRouteHandler returns a handler with the placement routes of the
// acme namespace and the store behind them.
func newPlacementRouteHandler(t *testing.T, authz identity.Authorizer) (testHandler, *memPlacements) {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	store := &memPlacements{mu: sync.Mutex{}, rules: nil, entries: nil}

	svc, err := collection.NewPlacementService(collection.PlacementConfig{Store: store, Namespaces: nsStore, Now: nil})
	if err != nil {
		t.Fatalf("placement service: %v", err)
	}

	nsSvc, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, apiinventory.Config{
		OutputSharing: nil, Outputs: nil, Views: nil, Audit: newTestAudit(), OnAuditFailure: nil, Locks: nil,
		PublicURL: "", Namespaces: nsSvc, Placements: svc, Collections: nil, Moves: nil,
		Units:         &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Deleted:       &memDeleted{entries: nil},
		History:       nil,
		Lifecycles:    testLifecycles(),
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Now:           nil,
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		DenialLimiter: nil,
		StateGuard:    stategate.Off("the test keeps no gates"),
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	return handler, store
}

const placementBase = "/api/dustpan/v1/namespaces/acme/placement-rules"

// placementCall sends one signed-in request and returns the status and body.
func placementCall(t *testing.T, h http.Handler, method, target, body string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// errorCodeOf returns the errorCode of an error answer.
func errorCodeOf(t *testing.T, body string) string {
	t.Helper()

	var e struct {
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("decode error answer %q: %v", body, err)
	}

	return e.ErrorCode
}

func ruleBody(project, stack, coll string) string {
	return `{"tool":"pulumi","project":"` + project + `","stack":"` + stack + `","collection":"` + coll + `"}`
}

func idOf(t *testing.T, body string) string {
	t.Helper()

	var r struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(body), &r); err != nil || r.ID == "" {
		t.Fatalf("decode rule %q: %v", body, err)
	}

	return r.ID
}

// TestPlacementRouteActions checks the declared action of each placement route:
// a read needs the read action and a write needs the manage action, both on the
// namespace, and a denied action answers 403 before the handler runs.
func TestPlacementRouteActions(t *testing.T) {
	t.Parallel()

	handler, _ := newPlacementRouteHandler(t, selectiveAuthorizer{deny: nil})

	want := map[string]identity.Action{
		http.MethodGet + " " + "/api/dustpan/v1/namespaces/{namespace}/placement-rules":           identity.ActionNamespaceReadPlacementRules,
		http.MethodPost + " " + "/api/dustpan/v1/namespaces/{namespace}/placement-rules":          identity.ActionNamespaceManagePlacementRules,
		http.MethodGet + " " + "/api/dustpan/v1/namespaces/{namespace}/placement-rules/{rule}":    identity.ActionNamespaceReadPlacementRules,
		http.MethodPut + " " + "/api/dustpan/v1/namespaces/{namespace}/placement-rules/{rule}":    identity.ActionNamespaceManagePlacementRules,
		http.MethodDelete + " " + "/api/dustpan/v1/namespaces/{namespace}/placement-rules/{rule}": identity.ActionNamespaceManagePlacementRules,
	}

	seen := 0

	for _, rt := range handler.Routes() {
		if !strings.Contains(rt.Path, "placement-rules") {
			continue
		}

		seen++

		key := rt.Method + " " + rt.Path

		action, ok := want[key]
		if !ok || len(rt.Actions) != 1 || rt.Actions[0] != action {
			t.Errorf("route %q declares %v, want [%v]", key, rt.Actions, action)
		}

		denied, store := newPlacementRouteHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{action: true}})
		target := strings.NewReplacer("{namespace}", "acme", "{rule}", "plr-abc").Replace(rt.Path)

		code, _ := placementCall(t, denied, rt.Method, target, ruleBody("a", "*", "payments"))
		if code != http.StatusForbidden {
			t.Errorf("%s with the action denied answers %d, want 403", key, code)
		}

		if len(store.entries) != 0 || len(store.rules) != 0 {
			t.Errorf("%s with the action denied wrote to the store", key)
		}
	}

	if seen != len(want) {
		t.Fatalf("handler serves %d placement routes, the table names %d", seen, len(want))
	}
}

// expectStatus fails the test when code is not want, and returns the body.
func expectStatus(t *testing.T, code int, body string, want int) string {
	t.Helper()

	if code != want {
		t.Fatalf("answer = %d %s, want %d", code, body, want)
	}

	return body
}

// expectError fails the test unless the answer has the status and the errorCode.
func expectError(t *testing.T, code int, body string, status int, errorCode string) {
	t.Helper()

	if code != status || errorCodeOf(t, body) != errorCode {
		t.Fatalf("answer = %d %s, want %d %s", code, body, status, errorCode)
	}
}

// postRule makes a rule through the route and returns its ID.
func postRule(t *testing.T, h http.Handler, body string) string {
	t.Helper()

	code, resp := placementCall(t, h, http.MethodPost, placementBase, body)

	return idOf(t, expectStatus(t, code, resp, http.StatusCreated))
}

func TestPlacementRuleLifecycle(t *testing.T) {
	t.Parallel()

	h, store := newPlacementRouteHandler(t, selectiveAuthorizer{deny: nil})

	code, body := placementCall(t, h, http.MethodGet, placementBase, "")
	if !strings.Contains(expectStatus(t, code, body, http.StatusOK), `"rules":[]`) {
		t.Fatalf("list of no rules = %s, want an empty list", body)
	}

	first := postRule(t, h, ruleBody("payments", "prod*", "payments"))
	second := postRule(t, h, ruleBody("payments", "*", "payments"))

	// The narrower rule after the wider rule is hidden.
	code, body = placementCall(t, h, http.MethodPost, placementBase, ruleBody("payments", "dev*", "sandbox"))
	expectError(t, code, body, http.StatusConflict, errcode.PlacementConflict)

	// The same rule at position 1 is accepted.
	third := postRule(t, h, `{"tool":"pulumi","project":"payments","stack":"dev*","collection":"sandbox","position":1}`)

	order := make([]string, 0, len(store.rules))
	for i := range store.rules {
		order = append(order, store.rules[i].ID)
	}

	if want := []string{third, first, second}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}

	code, body = placementCall(t, h, http.MethodGet, placementBase+"/"+first, "")
	if !strings.Contains(expectStatus(t, code, body, http.StatusOK), `"position":2`) {
		t.Errorf("get = %s, want position 2", body)
	}

	code, body = placementCall(
		t,
		h,
		http.MethodPut,
		placementBase+"/"+first,
		ruleBody("payments", "prod-*", "payments"),
	)
	if got := expectStatus(t, code, body, http.StatusOK); !strings.Contains(got, `"stack":"prod-*"`) ||
		!strings.Contains(got, `"position":2`) {
		t.Errorf("update = %s, want the new stack at the same position", got)
	}

	code, body = placementCall(t, h, http.MethodDelete, placementBase+"/"+second, "")
	if got := expectStatus(t, code, body, http.StatusNoContent); got != "" {
		t.Errorf("delete body = %q, want none", got)
	}

	code, body = placementCall(t, h, http.MethodGet, placementBase+"/"+second, "")
	expectError(t, code, body, http.StatusNotFound, errcode.PlacementRuleNotFound)

	checkRuleAudit(t, store.entries, 5)
}

// checkRuleAudit checks that every write left an audit entry that names the
// action, the namespace, the rule ID and the subject.
func checkRuleAudit(t *testing.T, entries []audit.Entry, want int) {
	t.Helper()

	if len(entries) != want {
		t.Fatalf("audit entries = %d, want %d", len(entries), want)
	}

	for i := range entries {
		e := &entries[i]
		if e.Action != string(identity.ActionNamespaceManagePlacementRules) || e.Namespace != "acme" ||
			!strings.HasPrefix(e.ObjectID, "plr-") || e.Subject != "tester" {
			t.Errorf("audit entry = %+v, want the manage action, the namespace, the rule ID and the subject", *e)
		}
	}
}

// TestPlacementRuleHostileInput sends hostile bodies and paths. Each must fail
// with a 400-class answer and leave the store unchanged.
func TestPlacementRuleHostileInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, method, path, body string
		status                   int
		code                     string
	}{
		{
			"regexp group",
			http.MethodPost,
			placementBase,
			ruleBody("(a+)+$", "*", "payments"),
			400,
			errcode.PlacementInvalidPattern,
		},
		{
			"regexp class",
			http.MethodPost,
			placementBase,
			ruleBody("[a-z]+", "*", "payments"),
			400,
			errcode.PlacementInvalidPattern,
		},
		{
			"empty stack",
			http.MethodPost,
			placementBase,
			ruleBody("a", "", "payments"),
			400,
			errcode.PlacementInvalidPattern,
		},
		{
			"two stars",
			http.MethodPost,
			placementBase,
			ruleBody("a", "x**", "payments"),
			400,
			errcode.PlacementInvalidPattern,
		},
		{
			"slash",
			http.MethodPost,
			placementBase,
			ruleBody("a/b", "*", "payments"),
			400,
			errcode.PlacementInvalidPattern,
		},
		{
			"escaped line feed",
			http.MethodPost,
			placementBase,
			`{"tool":"pulumi","project":"a","stack":"x\ny","collection":"payments"}`,
			400,
			errcode.PlacementInvalidPattern,
		},
		{
			"null byte",
			http.MethodPost,
			placementBase,
			`{"tool":"pulumi","project":"a\u0000","stack":"*","collection":"payments"}`,
			400,
			errcode.PlacementInvalidPattern,
		},
		{
			"long pattern",
			http.MethodPost,
			placementBase,
			ruleBody(strings.Repeat("a", 65), "*", "payments"),
			400,
			errcode.PlacementInvalidPattern,
		},
		{
			"tool without rules",
			http.MethodPost,
			placementBase,
			`{"tool":"terraform","project":"a","stack":"*","collection":"payments"}`,
			400,
			errcode.PlacementInvalidTool,
		},
		{
			"invalid collection name",
			http.MethodPost,
			placementBase,
			ruleBody("a", "*", "../x"),
			400,
			errcode.CollectionInvalidName,
		},
		{
			"missing collection",
			http.MethodPost,
			placementBase,
			ruleBody("a", "*", "elsewhere"),
			404,
			errcode.CollectionNotFound,
		},
		{
			"position zero is the end, 9 is outside",
			http.MethodPost,
			placementBase,
			`{"tool":"pulumi","project":"a","stack":"*","collection":"payments","position":9}`,
			400,
			errcode.PlacementInvalidPosition,
		},
		{
			"negative position",
			http.MethodPost,
			placementBase,
			`{"tool":"pulumi","project":"a","stack":"*","collection":"payments","position":-1}`,
			400,
			errcode.PlacementInvalidPosition,
		},
		{
			"unknown field",
			http.MethodPost,
			placementBase,
			`{"tool":"pulumi","project":"a","stack":"*","collection":"payments","x":1}`,
			400,
			errcode.RequestBodyInvalid,
		},
		{
			"missing field",
			http.MethodPost,
			placementBase,
			`{"tool":"pulumi","project":"a","collection":"payments"}`,
			400,
			errcode.RequestBodyInvalid,
		},
		{
			"two documents",
			http.MethodPost,
			placementBase,
			ruleBody("a", "*", "payments") + ruleBody("b", "*", "payments"),
			400,
			errcode.RequestBodyInvalid,
		},
		{"not an object", http.MethodPost, placementBase, `[]`, 400, errcode.RequestBodyInvalid},
		{"empty body", http.MethodPost, placementBase, ``, 400, errcode.RequestBodyInvalid},
		{
			"pattern as number",
			http.MethodPost,
			placementBase,
			`{"tool":"pulumi","project":1,"stack":"*","collection":"payments"}`,
			400,
			errcode.RequestBodyInvalid,
		},
		{
			"oversized body",
			http.MethodPost,
			placementBase,
			ruleBody("a", strings.Repeat("*", 4000), "payments"),
			400,
			errcode.RequestBodyInvalid,
		},
		{"rule ID with a slash", http.MethodGet, placementBase + "/a%2Fb", ``, 400, errcode.RequestInvalidPath},
		{"rule ID with a line feed", http.MethodDelete, placementBase + "/a%0Ab", ``, 400, errcode.RequestInvalidPath},
		{
			"rule ID too long",
			http.MethodGet,
			placementBase + "/" + strings.Repeat("a", 65),
			``,
			400,
			errcode.RequestInvalidPath,
		},
		{
			"unknown rule on update",
			http.MethodPut,
			placementBase + "/plr-none",
			ruleBody("a", "*", "payments"),
			404,
			errcode.PlacementRuleNotFound,
		},
		{
			"unknown rule on delete",
			http.MethodDelete,
			placementBase + "/plr-none",
			``,
			404,
			errcode.PlacementRuleNotFound,
		},
		{
			"invalid namespace",
			http.MethodGet,
			"/api/dustpan/v1/namespaces/-x/placement-rules",
			``,
			400,
			errcode.NamespaceInvalidName,
		},
		{
			"unknown namespace",
			http.MethodGet,
			"/api/dustpan/v1/namespaces/nobody/placement-rules",
			``,
			404,
			errcode.NamespaceNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, store := newPlacementRouteHandler(t, selectiveAuthorizer{deny: nil})

			code, body := placementCall(t, h, tt.method, tt.path, tt.body)
			if code != tt.status || errorCodeOf(t, body) != tt.code {
				t.Errorf("answer = %d %s, want %d %s", code, body, tt.status, tt.code)
			}

			if len(store.rules) != 0 || len(store.entries) != 0 {
				t.Errorf(
					"a refused request wrote to the store: %d rules, %d entries",
					len(store.rules),
					len(store.entries),
				)
			}
		})
	}
}
