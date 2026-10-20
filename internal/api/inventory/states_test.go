package inventory_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// stateTimes are the two lock times of the state tests: early is before late.
func stateTimes() [2]time.Time {
	early := time.Date(2026, time.October, 1, 8, 0, 0, 0, time.UTC)

	return [2]time.Time{early, early.Add(48 * time.Hour)}
}

// lockCombinationEntries returns four units in acme, one for each combination
// of the lock and the manual lock. The manual lock of "both" is the later one.
func lockCombinationEntries() []coreinventory.Entry {
	times := stateTimes()
	early, late := times[0], times[1]
	lock := &coreinventory.Lock{Since: late, Holder: "u", Operation: "lock"}
	manual := &coreinventory.ManualLock{Since: early, Kind: "unit", Object: "o", Holder: "u", Reason: "r"}

	entry := func(id string, l *coreinventory.Lock, m *coreinventory.ManualLock) coreinventory.Entry {
		return coreinventory.Entry{
			LastChanged: changeTime(), Namespace: "acme", Tool: "opentofu", Name: id, UnitID: id,
			DisplayName: id, Status: "", ResourceCount: nil, Lock: l, ManualLock: m, Collection: "", Labels: nil,
		}
	}

	return []coreinventory.Entry{
		entry("wsp-1-neither", nil, nil),
		entry("wsp-2-lock", lock, nil),
		entry("wsp-3-manual", nil, manual),
		entry("wsp-4-both", lock, manual),
	}
}

func newStateHandler(t *testing.T, entries []coreinventory.Entry) *apiinventory.Handler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	reader := &memReader{entries: map[namespace.Name][]coreinventory.Entry{"acme": entries}, err: nil}

	return newBehaviorHandlerWith(t, nsStore, reader, selectiveAuthorizer{deny: nil}, behaviorWiring{
		sessions: nil, lifecycles: testLifecycles(), publicURL: "",
	})
}

func statesByUnit(units []unitBody) map[string][]stateBody {
	out := map[string][]stateBody{}
	for i := range units {
		out[units[i].UnitID] = units[i].States
	}

	return out
}

// TestUnitListStatesLocked checks that locked is in states if and only if the
// entry holds a lock or a manual lock, and that since is the earlier time.
func TestUnitListStatesLocked(t *testing.T) {
	t.Parallel()

	times := stateTimes()
	early, late := times[0], times[1]
	lockedEarly := []stateBody{{State: "locked", Since: early.Format("2006-01-02T15:04:05Z")}}
	lockedLate := []stateBody{{State: "locked", Since: late.Format("2006-01-02T15:04:05Z")}}

	handler := newStateHandler(t, lockCombinationEntries())
	rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body)
	}

	var raw struct {
		Units []map[string]json.RawMessage `json:"units"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, u := range raw.Units {
		if got := string(u["states"]); got == "null" || got == "" {
			t.Errorf("states = %q, want a list that is never null", got)
		}
	}

	var list unitsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := statesByUnit(list.Units)
	want := map[string][]stateBody{
		"wsp-1-neither": {},
		"wsp-2-lock":    lockedLate,
		"wsp-3-manual":  lockedEarly,
		"wsp-4-both":    lockedEarly,
	}

	for id, w := range want {
		if !slices.Equal(got[id], w) {
			t.Errorf("states of %s = %v, want %v", id, got[id], w)
		}
	}
}

// TestUnitByIDStatesLocked checks that the one-unit route serves the states
// that the list serves.
func TestUnitByIDStatesLocked(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, lockCombinationEntries())

	for id, wantLocked := range map[string]bool{
		"wsp-1-neither": false, "wsp-2-lock": true, "wsp-3-manual": true, "wsp-4-both": true,
	} {
		rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/-/units/"+id, "test-token")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (%s)", id, rec.Code, rec.Body)
		}

		var unit unitBody
		if err := json.Unmarshal(rec.Body.Bytes(), &unit); err != nil {
			t.Fatalf("%s: decode: %v", id, err)
		}

		if got := len(unit.States) == 1 && unit.States[0].State == "locked"; got != wantLocked {
			t.Errorf("%s: locked in %v = %v, want %v", id, unit.States, got, wantLocked)
		}
	}
}

// TestUnitListStateFilter checks the state query value: any of the states, AND
// with the other filters, and the same answer as the derivation.
func TestUnitListStateFilter(t *testing.T) {
	t.Parallel()

	entries := lockCombinationEntries()
	entries[1].Labels = map[string]string{"team": "payments"}
	entries[2].Labels = map[string]string{"team": "platform"}

	handler := newStateHandler(t, entries)

	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"locked", "state=locked", []string{"wsp-2-lock", "wsp-3-manual", "wsp-4-both"}},
		{"locked and a label", "state=locked&label=team=payments", []string{"wsp-2-lock"}},
		{"locked and a reference", "state=locked&reference=opentofu:acme/wsp-3-manual", []string{"wsp-3-manual"}},
		{"locked and a reference that is not locked", "state=locked&reference=opentofu:acme/wsp-1-neither", nil},
		{"any of two", "state=locked&state=in-sync", []string{"wsp-2-lock", "wsp-3-manual", "wsp-4-both"}},
		{"duplicates", "state=locked&state=locked", []string{"wsp-2-lock", "wsp-3-manual", "wsp-4-both"}},
		{"a state no unit has", "state=failed", nil},
		{"no filter", "", []string{"wsp-1-neither", "wsp-2-lock", "wsp-3-manual", "wsp-4-both"}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, page := getPageFrom(t, handler, "acme", tt.query)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}

			if got := unitIDsOf(page.Units); !slices.Equal(got, tt.want) && (len(got) != 0 || len(tt.want) != 0) {
				t.Errorf("units = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestUnitListStateFilterMatchesDerivation checks, for each combination of
// the lock and the manual lock, that state=locked returns the unit if and only
// if the entry derives locked.
func TestUnitListStateFilterMatchesDerivation(t *testing.T) {
	t.Parallel()

	for _, e := range lockCombinationEntries() {
		handler := newStateHandler(t, []coreinventory.Entry{e})

		_, page := getPageFrom(t, handler, "acme", "state=locked")
		if got, want := len(page.Units) == 1, e.HasState(coreinventory.StateLocked); got != want {
			t.Errorf("%s: filter returns the unit = %v, derivation says %v", e.UnitID, got, want)
		}
	}
}

// TestUnitListStateFilterRefuses checks the values that answer 400.
func TestUnitListStateFilterRefuses(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, lockCombinationEntries())

	nine := strings.Repeat("&state=locked", 9)[1:]
	tenDuplicates := "state=" + strings.Join(strings.Fields(strings.Repeat("locked ", 10)), "&state=")

	cases := []struct {
		name  string
		query string
		code  int
	}{
		{"nine raw values", nine, http.StatusOK},
		{"ten raw values, all duplicates", tenDuplicates, http.StatusBadRequest},
		{"nine duplicates and one other raw value", nine + "&state=in-sync", http.StatusBadRequest},
		{"unknown value", "state=winning", http.StatusBadRequest},
		{"other case", "state=Locked", http.StatusBadRequest},
		{"trailing space", "state=" + url.QueryEscape("locked "), http.StatusBadRequest},
		{"leading space", "state=" + url.QueryEscape(" locked"), http.StatusBadRequest},
		{"empty value", "state=", http.StatusBadRequest},
		{"underscore", "state=in_sync", http.StatusBadRequest},
		{"one good, one bad", "state=locked&state=nope", http.StatusBadRequest},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := authorizedGet(
				t, handler, "/api/dustpan/v1/namespaces/acme/units?"+tt.query, "test-token",
			)
			if rec.Code != tt.code {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.code, rec.Body)
			}

			if tt.code != http.StatusBadRequest {
				return
			}

			for _, s := range coreinventory.States() {
				if !strings.Contains(rec.Body.String(), string(s)) {
					t.Errorf("error body %s does not name %q", rec.Body, s)
				}
			}
		})
	}
}

// TestUnitListStateFilterPageToken checks that a page token from a query with
// one state filter does not work for a query with another.
func TestUnitListStateFilterPageToken(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, lockCombinationEntries())

	_, page := getPageFrom(t, handler, "acme", "state=locked&pageSize=1")
	if page.NextPageToken == "" {
		t.Fatal("no next page token for the first page")
	}

	rec := authorizedGet(
		t, handler, "/api/dustpan/v1/namespaces/acme/units?pageSize=1&pageToken="+url.QueryEscape(page.NextPageToken),
		"test-token",
	)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("token without the state filter: status = %d, want 400", rec.Code)
	}

	code, next := getPageFrom(
		t,
		handler,
		"acme",
		"state=locked&pageSize=1&pageToken="+url.QueryEscape(page.NextPageToken),
	)
	if code != http.StatusOK || len(next.Units) != 1 {
		t.Errorf("token with the same filter: status = %d, units = %d, want 200 and 1", code, len(next.Units))
	}
}
