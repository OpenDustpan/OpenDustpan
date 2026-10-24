package triggers_test

import (
	"net/http"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/trigger"
)

// TestSourceNameNeedsRead reads a follows trigger of net whose source is app.
// A reader without unit:read on app gets the ID and the tool of the source,
// not its name.
func TestSourceNameNeedsRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		holds    []string
		wantName string
	}{
		{name: "reader of the source", holds: holdsAll(), wantName: "app"},
		{
			name: "no read on the source",
			holds: slices.DeleteFunc(holdsAll(), func(h string) bool {
				return h == "unit:read pulumi:acme/app"
			}),
			wantName: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer(tc.holds...))
			r.store.plant(trigger.Trigger{
				ID: "trg-follows", Unit: unitNet(), Kind: trigger.KindFollows,
				Frequency: trigger.Frequency{Preset: "", Cron: ""}, Source: graphUnit("pulumi", appID),
				Enabled: true, LastSaver: ada, LastSaverID: "prn-" + ada,
			})

			for _, path := range []string{basePath + "/trg-follows", basePath} {
				src := readSource(t, r, path)
				if src == nil || src.ID != appID || src.Tool != "Pulumi" || src.Name != tc.wantName {
					t.Errorf("GET %s: source = %+v, want ID %s, tool Pulumi, name %q", path, src, appID, tc.wantName)
				}
			}
		})
	}
}

// readSource reads the source of the one follows trigger at path: a trigger
// document, or the list of the triggers of net.
func readSource(t *testing.T, r *rig, path string) *unitRef {
	t.Helper()

	code, body, _ := do(t, r, http.MethodGet, path, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", path, code, body)
	}

	if path != basePath {
		return decode[answer](t, body).Source
	}

	list := decode[triggerList](t, body)
	if len(list.Triggers) != 1 {
		t.Fatalf("list = %s, want one trigger", body)
	}

	return list.Triggers[0].Source
}
