package inventory_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// outputsStates answers the latest state of each unit of the test: the ID
// "state-1", client-encrypted for the units in encrypted. A unit with an ID
// that is not in ids is an unknown producer.
type outputsStates struct {
	ids       map[string]bool
	encrypted map[string]bool
	// empty holds the units that exist and have no state yet.
	empty   map[string]bool
	stateID string
}

func (s *outputsStates) Latest(_ context.Context, u identity.Object) (outputshare.State, error) {
	if !u.IsUnit() {
		return outputshare.State{}, outputshare.ErrInvalid
	}

	if !s.ids[u.UnitID()] {
		return outputshare.State{}, outputshare.ErrUnknownProducer
	}

	if s.empty[u.UnitID()] {
		return outputshare.State{ID: "", ClientEncrypted: false}, nil
	}

	return outputshare.State{ID: s.stateID, ClientEncrypted: s.encrypted[u.UnitID()]}, nil
}

// outputsSource answers the outputs of a unit, with the value of a secret
// output set, as a source that forgets to leave it out. A source with fail set
// answers that error.
type outputsSource struct {
	mu     sync.Mutex
	moved  bool
	fail   error
	reads  int
	stored []outputshare.Output
}

func (s *outputsSource) Outputs(_ context.Context, _ identity.Object, _ string) ([]outputshare.Output, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.reads++

	if s.moved {
		return nil, outputshare.ErrStateMoved
	}

	if s.fail != nil {
		return nil, s.fail
	}

	return s.stored, nil
}

// outputsEntry returns the inventory entry of a unit.
func outputsEntry(ns namespace.Name, tool, name, id string) coreinventory.Entry {
	return coreinventory.Entry{
		LastChanged: time.Time{}, Namespace: ns, Tool: tool, Name: name, UnitID: id, DisplayName: "", Status: "",
		ResourceCount: nil, Lock: nil, ManualLock: nil, Collection: "", Labels: map[string]string{},
	}
}

type outputsFixture struct {
	handler *apiinventory.Handler
	states  *outputsStates
	source  *outputsSource
	audit   *memAudit
}

func newOutputsFixture(t *testing.T, authz identity.Authorizer) outputsFixture {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	nsSvc, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	states := &outputsStates{
		ids: map[string]bool{"ws-1": true, "stk-1": true}, encrypted: map[string]bool{},
		empty: map[string]bool{}, stateID: "state-1",
	}
	source := &outputsSource{mu: sync.Mutex{}, moved: false, fail: nil, reads: 0, stored: []outputshare.Output{
		{Name: "greeting", Secret: false, Value: json.RawMessage(`"hello"`)},
		{Name: "token", Secret: true, Value: json.RawMessage(`"s3cr3t"`)},
	}}
	auditStore := &memAudit{mu: sync.Mutex{}, entries: nil}

	checker, err := outputsharesvc.NewChecker(outputsharesvc.CheckerConfig{
		Authorizer: authz, States: states, Namespaces: nsSvc, Audit: auditStore,
		Logger: slog.New(slog.DiscardHandler), Now: nil,
	})
	if err != nil {
		t.Fatalf("checker: %v", err)
	}

	reader, err := outputsharesvc.NewReader(outputsharesvc.ReaderConfig{
		States: states, Checker: checker,
		Sources: map[string]outputshare.Outputs{"opentofu": source, "pulumi": source},
	})
	if err != nil {
		t.Fatalf("reader: %v", err)
	}

	reads := &memReader{entries: map[namespace.Name][]coreinventory.Entry{
		"acme": {
			outputsEntry("acme", "opentofu", "prod", "ws-1"),
			outputsEntry("acme", "pulumi", "web/prod", "stk-1"),
		},
		"globex": {outputsEntry("globex", "opentofu", "prod", "ws-2")},
	}, err: nil}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		Outputs: reader, OutputSharing: nil, Views: nil, Namespaces: nsSvc, Units: reads,
		Deleted: &memDeleted{entries: nil}, History: nil, Lifecycles: testLifecycles(),
		ReadScope: fixedScope{err: nil, ids: nil, all: true}, Collections: nil, Locks: nil, Moves: nil,
		Authenticator: subjectAuthenticator{}, Authorizer: authz, Audit: orTestAudit(auditStore),
		OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, PublicURL: "", Logger: slog.New(slog.DiscardHandler),
		Now: nil, StateGuard: nil,
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	return outputsFixture{handler: handler, states: states, source: source, audit: auditStore}
}

func (f outputsFixture) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.Header.Set("Authorization", "token alice-token")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

const (
	outputsTofu    = "/api/dustpan/v1/namespaces/acme/units/ws-1/outputs"
	outputsPulumi  = "/api/dustpan/v1/namespaces/acme/units/stk-1/outputs"
	outputsOther   = "/api/dustpan/v1/namespaces/acme/units/ws-2/outputs"
	outputsWrongNS = "/api/dustpan/v1/namespaces/globex/units/ws-1/outputs"
)

// TestOutputsRouteDeclaresItsActions proves the row of the route: unit:read-outputs
// on the unit and no other action, a denial answered as an unknown unit, and
// the object that the resolver returns.
func TestOutputsRouteDeclaresItsActions(t *testing.T) {
	t.Parallel()

	f := newOutputsFixture(t, selectiveAuthorizer{deny: nil})

	found := false

	for _, route := range f.handler.Routes() {
		if route.Method+" "+route.Path != "GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs" {
			continue
		}

		found = true

		checkRouteScope(t, route, map[string]bool{})

		if len(route.Actions) != 1 || route.Actions[0] != identity.ActionUnitReadOutputs {
			t.Errorf("actions = %v, want [unit:read-outputs]", route.Actions)
		}

		if len(route.NamespaceActions) != 0 {
			t.Errorf(
				"namespace actions = %v, want none: a principal with unit:read-outputs only reads",
				route.NamespaceActions,
			)
		}

		if !route.DeniedAsNotFound {
			t.Error("a denied caller does not get the 404 of an unknown unit")
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, outputsTofu, http.NoBody)
		req.SetPathValue("namespace", "acme")
		req.SetPathValue("unit_id", "ws-1")

		obj, err := route.Resolve(nil, req)
		if err != nil || obj.String() != "opentofu:acme/prod" {
			t.Errorf("object = %q, %v; want opentofu:acme/prod", obj.String(), err)
		}
	}

	if !found {
		t.Fatal("the handler serves no outputs route")
	}
}

// TestNoOutputsRouteWithoutTheReader proves that a handler built without the
// reader serves no outputs route.
func TestNoOutputsRouteWithoutTheReader(t *testing.T) {
	t.Parallel()

	f := newLockFixture(t, selectiveAuthorizer{deny: nil})

	for _, route := range f.handler.Routes() {
		if strings.HasSuffix(route.Path, "/outputs") {
			t.Errorf("route %s %s is served without the reader", route.Method, route.Path)
		}
	}
}

// outputsCase is one row of TestOutputsRouteAnswers.
type outputsCase struct {
	name   string
	target string
	// deny holds the actions that the authorizer refuses.
	deny map[identity.Action]bool
	// encrypted is the unit ID whose state the client encrypted, or "".
	encrypted string
	moved     bool
	want      int
}

// outputsRow returns a case that reads target with every grant and expects the
// status 200, and lets edit change it.
func outputsRow(name, target string, edit func(*outputsCase)) outputsCase {
	c := outputsCase{
		name: name, target: target, deny: map[identity.Action]bool{}, encrypted: "", moved: false,
		want: http.StatusOK,
	}
	edit(&c)

	return c
}

func outputsCases() []outputsCase {
	const unknownUnit = "/api/dustpan/v1/namespaces/acme/units/nope/outputs"

	return []outputsCase{
		outputsRow("an OpenTofu workspace", outputsTofu, func(*outputsCase) {}),
		outputsRow("a Pulumi stack", outputsPulumi, func(*outputsCase) {}),
		outputsRow("without unit:read-outputs the unit is unknown", outputsTofu, func(c *outputsCase) {
			c.deny[identity.ActionUnitReadOutputs] = true
			c.want = http.StatusNotFound
		}),
		outputsRow("no namespace:read is needed", outputsTofu, func(c *outputsCase) {
			c.deny[identity.ActionNamespaceRead] = true
		}),
		outputsRow("an unknown unit ID", unknownUnit, func(c *outputsCase) { c.want = http.StatusNotFound }),
		outputsRow("a unit of another namespace than the path", outputsWrongNS, func(c *outputsCase) {
			c.want = http.StatusNotFound
		}),
		outputsRow("outputs only, client-encrypted state", outputsTofu, func(c *outputsCase) {
			c.encrypted = "ws-1"
			c.deny[identity.ActionUnitRead] = true
			c.want = http.StatusConflict
		}),
		outputsRow("unit:read holder, client-encrypted state", outputsTofu, func(c *outputsCase) {
			c.encrypted = "ws-1"
			c.want = http.StatusConflict
		}),
		outputsRow("a state that moved during the read", outputsTofu, func(c *outputsCase) {
			c.moved = true
			c.want = http.StatusConflict
		}),
	}
}

// TestOutputsRouteAnswers proves each way a read of outputs ends: the answer
// for each tool, the denial of the route as the 404 of an unknown unit, and
// the refusals of the checker.
func TestOutputsRouteAnswers(t *testing.T) {
	t.Parallel()

	for _, tt := range outputsCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newOutputsFixture(t, selectiveAuthorizer{deny: tt.deny})
			if tt.encrypted != "" {
				f.states.encrypted[tt.encrypted] = true
			}

			f.source.moved = tt.moved

			rec := f.get(t, tt.target)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}

			if tt.want != http.StatusOK {
				checkOutputsRefusal(t, f, tt.want, rec)
			}
		})
	}
}

// checkOutputsRefusal proves that a refusal answered no output, and that a
// refusal of the route did not call the source.
func checkOutputsRefusal(t *testing.T, f outputsFixture, want int, rec *httptest.ResponseRecorder) {
	t.Helper()

	body := rec.Body.String()
	if strings.Contains(body, "hello") || strings.Contains(body, "s3cr3t") {
		t.Errorf("a refusal answered an output: %s", body)
	}

	if want == http.StatusNotFound && f.source.reads != 0 {
		t.Errorf("a refused read called the source %d times", f.source.reads)
	}
}

// outputsDocument is the answer of the outputs route, as a client decodes it.
type outputsDocument struct {
	Tool    string        `json:"tool"`
	UnitID  string        `json:"unitId"`  //nolint:tagliatelle // The tool-neutral API names this field in lower camel case.
	StateID string        `json:"stateId"` //nolint:tagliatelle // The tool-neutral API names this field in lower camel case.
	Outputs []outputEntry `json:"outputs"`
}

// outputEntry is one output of an outputsDocument.
type outputEntry struct {
	Name   string          `json:"name"`
	Secret bool            `json:"secret"`
	Value  json.RawMessage `json:"value"`
}

// TestOutputsRouteAnswersNoSecretValue proves the body of an allowed read: the
// plain value, and a secret output with its mark and no value, also when the
// source returned one.
func TestOutputsRouteAnswersNoSecretValue(t *testing.T) {
	t.Parallel()

	f := newOutputsFixture(t, selectiveAuthorizer{deny: nil})

	rec := f.get(t, outputsTofu)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var doc outputsDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	checkOutputsDocument(t, &doc)

	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Errorf("the body holds the secret value: %s", rec.Body.String())
	}
}

// checkOutputsDocument proves the header of the document, the plain output and
// the secret output with no value.
func checkOutputsDocument(t *testing.T, doc *outputsDocument) {
	t.Helper()

	if doc.Tool != "opentofu" || doc.UnitID != "ws-1" || doc.StateID != "state-1" || len(doc.Outputs) != 2 {
		t.Fatalf("document = %+v", *doc)
	}

	greeting, token := doc.Outputs[0], doc.Outputs[1]
	if greeting.Name != "greeting" || greeting.Secret || string(greeting.Value) != `"hello"` {
		t.Errorf("plain output = %+v", greeting)
	}

	if token.Name != "token" || !token.Secret || len(token.Value) != 0 {
		t.Errorf("secret output = %+v, want the mark and no value", token)
	}
}

// TestOutputsRouteDeniesAUnitTheStatesPortDoesNotKnow proves that a unit that
// the inventory lists and the states port does not know answers 404 and not
// 403: the checker names the producer as unknown.
func TestOutputsRouteDeniesAUnitTheStatesPortDoesNotKnow(t *testing.T) {
	t.Parallel()

	f := newOutputsFixture(t, selectiveAuthorizer{deny: nil})
	delete(f.states.ids, "stk-1")

	rec := f.get(t, outputsPulumi)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestOutputsRouteAnswersAUnitWithNoState proves that a stack or a workspace
// that has no state yet answers 404 with a message that names the unit and
// says that it has no state, to a caller who holds unit:read-outputs, and that
// the request writes no denial entry and does not call the source.
func TestOutputsRouteAnswersAUnitWithNoState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		unitID string
		unit   string
	}{
		{name: "a Pulumi stack", target: outputsPulumi, unitID: "stk-1", unit: "pulumi:acme/web/prod"},
		{name: "an OpenTofu workspace", target: outputsTofu, unitID: "ws-1", unit: "opentofu:acme/prod"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newOutputsFixture(t, selectiveAuthorizer{deny: nil})
			f.states.empty[tt.unitID] = true

			rec := f.get(t, tt.target)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
			}

			body := rec.Body.String()
			if !strings.Contains(body, tt.unit) || !strings.Contains(body, "has no state yet") {
				t.Errorf("body = %s, want the unit %q and %q", body, tt.unit, "has no state yet")
			}

			if f.source.reads != 0 {
				t.Errorf("a unit with no state called the source %d times", f.source.reads)
			}

			if n := len(f.audit.entries); n != 0 {
				t.Errorf("a unit with no state wrote %d audit entries, want 0", n)
			}
		})
	}
}
