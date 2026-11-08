package apidesc_test

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// thing is the body of the sample routes.
type thing struct {
	Name string `json:"name"`
}

type createThingBody struct {
	Name string `json:"name"`
}

type updateThingBody struct {
	Name *string `json:"name,omitempty"`
}

// pathsDoc is the paths object of a document.
type pathsDoc struct {
	Paths map[string]map[string]map[string]any `json:"paths"`
}

// sampleDocument builds the document of the operations and decodes the parts
// that the tests read.
func sampleDocument(t *testing.T, ops ...apidesc.Operation) map[string]map[string]map[string]any {
	t.Helper()

	doc, err := apidesc.Build(ops)
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	var parsed pathsDoc
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("the document is not JSON: %v", err)
	}

	return parsed.Paths
}

// withBodies returns the operation with the request and the answer given.
func withBodies(o apidesc.Operation, request, response apidesc.Body) apidesc.Operation {
	o.Bodies = &apidesc.Bodies{Request: request, Response: response}

	if !response.IsNone() {
		o.Statuses = []int{http.StatusOK}
	}

	return o
}

func TestBuildRefusesAnUnmarkedBody(t *testing.T) {
	t.Parallel()

	var unset apidesc.Body

	noResponse := withBodies(op("GET", "/api/dustpan/v1/things", "thing:read"), apidesc.NoBody, unset)
	postNoRequest := withBodies(op("POST", "/api/dustpan/v1/things", "thing:write"), unset, apidesc.NoBody)
	putNoRequest := withBodies(op("PUT", "/api/dustpan/v1/things/{thing_id}", "thing:write"), unset, apidesc.NoBody)
	patchNoRequest := withBodies(op("PATCH", "/api/dustpan/v1/things/{thing_id}", "thing:write"), unset, apidesc.NoBody)
	getWithRequest := withBodies(
		op("GET", "/api/dustpan/v1/things", "thing:read"), apidesc.Typed[thing]("Thing"), apidesc.NoBody,
	)
	deleteWithRequest := withBodies(
		op(
			"DELETE",
			"/api/dustpan/v1/things/{thing_id}",
			"thing:delete",
		),
		apidesc.Typed[thing]("Thing"),
		apidesc.NoBody,
	)

	noBodies := op("GET", "/api/dustpan/v1/things", "thing:read")
	noBodies.Bodies = nil

	tests := []struct {
		name string
		op   apidesc.Operation
	}{
		{"a GET with no response type", noResponse},
		{"a POST with no request type", postNoRequest},
		{"a PUT with no request type", putNoRequest},
		{"a PATCH with no request type", patchNoRequest},
		{"a GET with a request type", getWithRequest},
		{"a DELETE with a request type", deleteWithRequest},
		{"an operation that names no bodies at all", noBodies},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.op.ValidateBodies(); !errors.Is(err, apidesc.ErrInvalidBody) {
				t.Errorf("ValidateBodies() = %v, want ErrInvalidBody", err)
			}

			if _, err := apidesc.Build([]apidesc.Operation{tt.op}); !errors.Is(err, apidesc.ErrInvalidBody) {
				t.Errorf("Build() = %v, want ErrInvalidBody", err)
			}
		})
	}
}

func TestBuildAcceptsAMarkedBody(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		o := op(method, "/api/dustpan/v1/things", "thing:read")

		if err := o.ValidateBodies(); err != nil {
			t.Errorf("%s with NoBody: ValidateBodies() = %v", method, err)
		}
	}
}

func TestBuildWritesTheBodies(t *testing.T) {
	t.Parallel()

	create := withBodies(
		op("POST", "/api/dustpan/v1/things", "thing:write"),
		apidesc.Typed[createThingBody]("CreateThingRequest"), apidesc.Typed[thing]("Thing"),
	)
	create.ID = "createThing"

	patch := withBodies(
		op("PATCH", "/api/dustpan/v1/things/{thing_id}", "thing:write"),
		apidesc.Typed[updateThingBody]("UpdateThingRequest"), apidesc.Typed[thing]("Thing"),
	)
	patch.ID = "updateThing"

	remove := op("DELETE", "/api/dustpan/v1/things/{thing_id}", "thing:delete")
	remove.ID = "deleteThing"

	paths := sampleDocument(t, create, patch, remove)

	post := paths["/api/dustpan/v1/things"]["post"]
	if got := jsonOf(post["requestBody"]); got !=
		`{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/CreateThingRequest"}}},"required":true}` {
		t.Errorf("POST requestBody = %s", got)
	}

	responses := asMap(t, post["responses"])
	if got := jsonOf(responses["200"]); !strings.Contains(got, `"$ref":"#/components/schemas/Thing"`) {
		t.Errorf("POST 200 = %s, want a reference to Thing", got)
	}

	merge := jsonOf(paths["/api/dustpan/v1/things/{thing_id}"]["patch"]["requestBody"])
	if !strings.Contains(merge, `"application/merge-patch+json"`) {
		t.Errorf("PATCH requestBody = %s, want the merge patch media type", merge)
	}

	del := paths["/api/dustpan/v1/things/{thing_id}"]["delete"]
	if _, has := del["requestBody"]; has {
		t.Error("DELETE with NoBody has a requestBody")
	}

	delResponses := asMap(t, del["responses"])
	if got := jsonOf(delResponses["204"]); got != `{"description":"The request succeeded."}` {
		t.Errorf("DELETE 204 = %s, want an answer with no content", got)
	}
}

func jsonOf(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}

	return string(raw)
}

func TestObjectKinds(t *testing.T) {
	t.Parallel()

	missing := op("GET", "/api/dustpan/v1/things", "thing:read")
	missing.ActionObjects = nil

	unknownKind := op("GET", "/api/dustpan/v1/things", "thing:read")
	unknownKind.ActionObjects = map[string]string{"thing:read": "Namespace"}

	emptyKind := op("GET", "/api/dustpan/v1/things", "thing:read")
	emptyKind.ActionObjects = map[string]string{"thing:read": ""}

	extra := op("GET", "/api/dustpan/v1/things", "thing:read")
	extra.ActionObjects = map[string]string{"thing:read": apidesc.KindUnit, "thing:write": apidesc.KindUnit}

	publicWithKind := publicOp("/api/dustpan/v1/capabilities")
	publicWithKind.ActionObjects = map[string]string{"caller:read": apidesc.KindCaller}

	for name, o := range map[string]apidesc.Operation{
		"an action with no kind": missing, "a kind outside the allowlist": unknownKind, "an empty kind": emptyKind,
		"a kind for an action the route lacks": extra, "a kind on a public route": publicWithKind,
	} {
		if err := o.Validate(); !errors.Is(err, apidesc.ErrInvalidOperation) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidOperation", name, err)
		}
	}

	for _, kind := range []string{
		apidesc.KindNamespace, apidesc.KindUnit, apidesc.KindCollection, apidesc.KindCaller, apidesc.KindServer,
	} {
		o := op("GET", "/api/dustpan/v1/things", "thing:read")
		o.ActionObjects = map[string]string{"thing:read": kind}

		if err := o.Validate(); err != nil {
			t.Errorf("kind %q: Validate() = %v", kind, err)
		}
	}
}

func TestObjectKindsRoundTripAndDiff(t *testing.T) {
	t.Parallel()

	a := op("PUT", "/api/dustpan/v1/namespaces/{namespace}/lock", "unit:lock", "namespace:read")
	a.ActionObjects = map[string]string{"unit:lock": apidesc.KindCollection, "namespace:read": apidesc.KindNamespace}

	doc, err := apidesc.Build([]apidesc.Operation{a})
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	read, err := apidesc.Read(doc)
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}

	if len(read) != 1 || !maps.Equal(read[0].ActionObjects, a.ActionObjects) ||
		!slices.Equal(read[0].Actions, a.Actions) {
		t.Fatalf("Read() = %+v, want the actions %v with the kinds %v", read, a.Actions, a.ActionObjects)
	}

	drift, err := apidesc.Diff([]apidesc.Operation{a}, read)
	if err != nil || !drift.Empty() {
		t.Fatalf("Diff() = %+v, %v; want no drift", drift, err)
	}

	moved := a
	moved.ActionObjects = map[string]string{"unit:lock": apidesc.KindUnit, "namespace:read": apidesc.KindNamespace}

	drift, err = apidesc.Diff([]apidesc.Operation{moved}, read)
	if err != nil || len(drift.Mismatch) != 1 {
		t.Fatalf("Diff() = %+v, %v; want one mismatch for the changed kind", drift, err)
	}
}

func TestCollectGivesEachActionItsObjectKind(t *testing.T) {
	t.Parallel()

	rows := []apidesc.Row{{
		Method: "GET", Path: "/api/dustpan/v1/things", Actions: []string{"thing:read", "namespace:read", "thing:read"},
		NamespaceActions: []string{"namespace:read"}, Public: false, IdempotencyKey: false,
	}}
	labels := map[string]apidesc.Label{
		"GET /api/dustpan/v1/things": apidesc.Name("listThings", "things").
			On(apidesc.KindCollection).
			WithBodies(apidesc.NoBody, apidesc.Typed[thing]("Thing")),
	}

	got := apidesc.Collect(rows, labels)
	if len(got) != 1 {
		t.Fatalf("Collect() = %v, want one operation", got)
	}

	want := map[string]string{"thing:read": apidesc.KindCollection, "namespace:read": apidesc.KindNamespace}
	if !maps.Equal(got[0].ActionObjects, want) {
		t.Errorf("ActionObjects = %v, want %v", got[0].ActionObjects, want)
	}

	if got[0].Bodies == nil || !got[0].Bodies.Request.IsNone() || got[0].Bodies.Response.Name() != "Thing" {
		t.Errorf("bodies = %v, want NoBody and Thing", got[0].Bodies)
	}

	if err := got[0].Validate(); err != nil {
		t.Errorf("Validate() = %v", err)
	}
}

func TestCollectWithoutAKindFailsValidation(t *testing.T) {
	t.Parallel()

	rows := []apidesc.Row{{
		Method: "GET", Path: "/api/dustpan/v1/things", Actions: []string{"thing:read"}, NamespaceActions: nil,
		Public: false, IdempotencyKey: false,
	}}
	labels := map[string]apidesc.Label{"GET /api/dustpan/v1/things": apidesc.Name("listThings", "things")}

	got := apidesc.Collect(rows, labels)
	if len(got) != 1 {
		t.Fatalf("Collect() = %v, want one operation", got)
	}

	if err := got[0].Validate(); !errors.Is(err, apidesc.ErrInvalidOperation) {
		t.Errorf("Validate() = %v, want ErrInvalidOperation", err)
	}

	if err := got[0].ValidateBodies(); !errors.Is(err, apidesc.ErrInvalidBody) {
		t.Errorf("ValidateBodies() = %v, want ErrInvalidBody", err)
	}
}

// TestLegacyFieldNamesAreExactlyTheKnownOnes pins the list of wire names that
// break the camelCase rule. A new name in the list is a review decision.
func TestLegacyFieldNamesAreExactlyTheKnownOnes(t *testing.T) {
	t.Parallel()

	got := slices.Sorted(maps.Keys(apidesc.LegacyFieldNames()))
	want := []string{
		"appID", "backupID", "effectiveIssuerURL", "hookID", "installationID", "issuerURL", "objectID", "poolID",
		"preBackupID", "setID", "unitID", "versionID",
	}
	if !slices.Equal(got, want) {
		t.Errorf("LegacyFieldNames() = %v, want %v", got, want)
	}

	for name, reason := range apidesc.LegacyFieldNames() {
		if reason == "" {
			t.Errorf("legacy name %q has no reason", name)
		}
	}

	// The returned map is a copy.
	apidesc.LegacyFieldNames()["other"] = "x"

	if _, has := apidesc.LegacyFieldNames()["other"]; has {
		t.Error("LegacyFieldNames() returns the map itself")
	}
}

type dangling struct {
	Value own `json:"value"`
}

func TestBuildRefusesADanglingReference(t *testing.T) {
	t.Parallel()

	body := apidesc.Typed[dangling]("Dangling", apidesc.Declare[own](map[string]any{
		"$ref": "#/components/schemas/Nowhere",
	}))

	if _, err := schemasOf(t, body); !errors.Is(err, apidesc.ErrInvalidBody) {
		t.Fatalf("Build() = %v, want ErrInvalidBody", err)
	}
}

// wireOwn is the JSON that own writes.
type wireOwn struct {
	V string `json:"v"`
}

// ownObject is a type whose MarshalJSON writes an object, as the types of the
// domain packages do.
type ownObject struct{ v string }

//nolint:unparam // The method implements json.Marshaler.
func (o ownObject) MarshalJSON() ([]byte, error) {
	return []byte(`{"v":` + strconv.Quote(o.v) + `}`), nil
}

type extraWire struct {
	V     string `json:"v"`
	Other string `json:"other"`
}

func TestVerifyAs(t *testing.T) {
	t.Parallel()

	if err := apidesc.VerifyAs[ownObject, wireOwn](ownObject{v: "x"}); err != nil {
		t.Errorf("VerifyAs() with the right wire type = %v", err)
	}

	// The wire type adds a member that the type never writes.
	if err := apidesc.VerifyAs[ownObject, extraWire](ownObject{v: "x"}); err == nil {
		t.Error("VerifyAs() with a wire type that adds a required member = nil, want an error")
	}

	// The wire type lacks a member that the type writes.
	if err := apidesc.VerifyAs[extraWire, wireOwn](extraWire{V: "x", Other: "y"}); err == nil {
		t.Error("VerifyAs() with a wire type that lacks a member = nil, want an error")
	}
}

type holdsOwnObject struct {
	Value ownObject `json:"value"`
}

func TestDeclareAs(t *testing.T) {
	t.Parallel()

	got, err := schemasOf(t, apidesc.Typed[holdsOwnObject]("HoldsOwnObject", apidesc.DeclareAs[ownObject, wireOwn]()))
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	want := wantSchemas(t, `{
	"HoldsOwnObject":{"type":"object","required":["value"],"properties":{"value":{"$ref":"#/components/schemas/WireOwn"}}},
	"WireOwn":{"type":"object","required":["v"],"properties":{"v":{"type":"string"}}}}`)

	if jsonOf(got) != jsonOf(want) {
		t.Errorf("schemas = %s\nwant %s", jsonOf(got), jsonOf(want))
	}

	// A wire type with a marshaler of its own only moves the problem.
	marshalerAsWire := apidesc.Typed[holdsOwnObject]("HoldsOwnObject", apidesc.DeclareAs[ownObject, own]())

	if _, err = schemasOf(t, marshalerAsWire); !errors.Is(err, apidesc.ErrInvalidBody) {
		t.Errorf("Build() with a marshaler as the wire type = %v, want ErrInvalidBody", err)
	}
}
