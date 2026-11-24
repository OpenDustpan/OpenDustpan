package decisionrecord_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/decisionrecord"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// withApprovers returns the stored record id of ns with one requirement that
// has the approvers a.
func withApprovers(ns namespace.Name, id string, a gate.Approvers) gate.Record {
	rec := storedRecord(ns, id)
	rec.Requirements = []gate.Requirement{{
		Name: "rule:dba-review", Version: 1, Approvers: a, Count: 1, AuthorMayApprove: false,
		FreshAuth: gate.NoFreshAuth(), TimeoutSeconds: 0, Signatures: nil, Evaluate: false,
	}}

	return rec
}

// approversJSON returns the JSON value of each field of the approvers of the
// first requirement of rec, by JSON key.
func approversJSON(t *testing.T, rec gate.Record) map[string]string {
	t.Helper()

	if len(rec.Requirements) != 1 {
		t.Fatalf("requirements = %d, want 1", len(rec.Requirements))
	}

	b, err := json.Marshal(rec.Requirements[0].Approvers)
	if err != nil {
		t.Fatalf("encode the approvers: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("decode the approvers: %v", err)
	}

	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = string(v)
	}

	return out
}

// readAcme reads the record of acme that records holds, through the service.
func readAcme(t *testing.T, stored gate.Record) gate.Record {
	t.Helper()

	records := newFakeRecords()
	records.byKey[recordKey("acme", recordID)] = stored

	svc, err := decisionrecord.NewService(records)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	rec, err := svc.Read(t.Context(), gate.NamespaceScope("acme"), recordID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	return rec
}

// TestReadAnswersEmptyApproverSlices proves that the read gives [] for each
// nil slice of the approvers, keeps an empty and a filled slice as they are,
// and does not change the stored value.
func TestReadAnswersEmptyApproverSlices(t *testing.T) {
	t.Parallel()

	setters := map[string]func(a *gate.Approvers, v []string){
		"groups":   func(a *gate.Approvers, v []string) { a.Groups = v },
		"groupIds": func(a *gate.Approvers, v []string) { a.GroupIDs = v },
		"users":    func(a *gate.Approvers, v []string) { a.Users = v },
		"userIds":  func(a *gate.Approvers, v []string) { a.UserIDs = v },
	}

	states := []struct {
		name  string
		value []string
		want  string
	}{
		{name: "nil", value: nil, want: "[]"},
		{name: "empty", value: []string{}, want: "[]"},
		{name: "filled", value: []string{"x"}, want: `["x"]`},
	}

	for key, set := range setters {
		for _, st := range states {
			t.Run(key+" "+st.name, func(t *testing.T) {
				t.Parallel()

				full := []string{"f"}
				a := gate.Approvers{Groups: full, GroupIDs: full, Users: full, UserIDs: full, UnitOwners: false}
				set(&a, st.value)

				stored := withApprovers("acme", recordID, a)
				got := approversJSON(t, readAcme(t, stored))

				if got[key] != st.want {
					t.Errorf("%s = %s, want %s", key, got[key], st.want)
				}

				storedField := reflect.ValueOf(stored.Requirements[0].Approvers).FieldByName(fieldOf(key))
				if st.value == nil && !storedField.IsNil() {
					t.Errorf("the read changed the stored %s", key)
				}
			})
		}
	}
}

// fieldOf returns the Go field name of Approvers for the JSON key.
func fieldOf(key string) string {
	for field := range reflect.TypeFor[gate.Approvers]().Fields() {
		if jsonKey(field) == key {
			return field.Name
		}
	}

	return ""
}

// jsonKey returns the JSON key of field.
func jsonKey(field reflect.StructField) string {
	key, _, _ := strings.Cut(field.Tag.Get("json"), ",")

	return key
}

// TestReadAnswersEveryApproverSliceField proves that the read gives [] for
// every slice field of the approvers when all of them are nil, so a new slice
// field cannot answer null.
func TestReadAnswersEveryApproverSliceField(t *testing.T) {
	t.Parallel()

	got := approversJSON(t, readAcme(t, withApprovers("acme", recordID, gate.Approvers{
		Groups: nil, GroupIDs: nil, Users: nil, UserIDs: nil, UnitOwners: false,
	})))

	for field := range reflect.TypeFor[gate.Approvers]().Fields() {
		if field.Type.Kind() != reflect.Slice {
			continue
		}

		key := jsonKey(field)
		if got[key] != "[]" {
			t.Errorf("%s = %s, want []", key, got[key])
		}
	}
}

// TestReadAnswersUserIDsOfAnOldBody proves that a body stored before the
// field userIds existed answers "userIds": [].
func TestReadAnswersUserIDsOfAnOldBody(t *testing.T) {
	t.Parallel()

	var a gate.Approvers

	err := json.Unmarshal([]byte(`{"groups":["sre"],"groupIds":["g-sre"],"users":[],"unitOwners":false}`), &a)
	if err != nil {
		t.Fatalf("decode the old body: %v", err)
	}

	got := approversJSON(t, readAcme(t, withApprovers("acme", recordID, a)))

	if got["userIds"] != "[]" {
		t.Errorf("userIds = %s, want []", got["userIds"])
	}

	if got["groups"] != `["sre"]` || got["groupIds"] != `["g-sre"]` || got["users"] != "[]" {
		t.Errorf("approvers = %v, want the stored groups, groupIds and users", got)
	}
}

// TestRoutesAnswerNoNullApprovers proves that the JSON of each of the two
// routes gives [] and no null for the approvers of a stored record whose
// slices are nil.
func TestRoutesAnswerNoNullApprovers(t *testing.T) {
	t.Parallel()

	nilApprovers := gate.Approvers{Groups: nil, GroupIDs: nil, Users: nil, UserIDs: nil, UnitOwners: false}

	cases := []struct {
		name  string
		ns    namespace.Name
		id    string
		path  string
		holds []string
	}{
		{
			name: "namespace route", ns: "acme", id: recordID,
			path:  "/api/dustpan/v1/namespaces/acme/decision-records/" + recordID,
			holds: namespaceHolds("acme"),
		},
		{
			name: "installation route", ns: audit.ServerScope, id: instalRecord,
			path:  "/api/dustpan/v1/decision-records/" + instalRecord,
			holds: installationHolds(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			records := newFakeRecords()
			records.byKey[recordKey(tc.ns, tc.id)] = withApprovers(tc.ns, tc.id, nilApprovers)
			handler := newHandler(t, newAuthorizer(tc.holds...), records)

			w := get(t.Context(), t, handler, tc.path, testToken)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body)
			}

			checkNoNullApprovers(t, w.Body.Bytes())
		})
	}
}

// answeredRequirement is the part of an answered requirement that the route
// test reads: the raw JSON value of each field of its approvers.
type answeredRequirement struct {
	Approvers map[string]json.RawMessage `json:"approvers"`
}

// answeredRecord is the part of an answered record that the route test reads.
type answeredRecord struct {
	Requirements []answeredRequirement `json:"requirements"`
}

// checkNoNullApprovers fails unless body holds one requirement whose approvers
// give [] for each slice field.
func checkNoNullApprovers(t *testing.T, body []byte) {
	t.Helper()

	var rec answeredRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatalf("decode the record: %v; body: %s", err, body)
	}

	if len(rec.Requirements) != 1 {
		t.Fatalf("requirements = %d, want 1; body: %s", len(rec.Requirements), body)
	}

	for _, key := range []string{"groups", "groupIds", "users", "userIds"} {
		if got := string(rec.Requirements[0].Approvers[key]); got != "[]" {
			t.Errorf("%s = %s, want []", key, got)
		}
	}
}
