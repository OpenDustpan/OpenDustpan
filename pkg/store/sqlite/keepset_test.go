package sqlite_test

import (
	"reflect"
	"regexp"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

func TestKeepSetRules(t *testing.T) {
	t.Parallel()

	storetest.RunKeepSetRules(
		t,
		func(t *testing.T) storetest.UpdatesStore {
			t.Helper()

			return openSQLUpdates(t)
		},
		func(t *testing.T) storetest.WorkspaceStore {
			t.Helper()

			return openAuditedWorkspaces(t)
		},
	)
}

// TestKeepSetRulesCoverEveryKeepSetMethod fails when the store has a
// Referenced*Keys method that the keep set rules do not cover, and when the
// rules cover a method that the store does not have.
func TestKeepSetRulesCoverEveryKeepSetMethod(t *testing.T) {
	t.Parallel()

	keepSetMethod := regexp.MustCompile(`^Referenced\w*Keys$`)
	typ := reflect.TypeFor[*sqlite.Store]()

	var got []string

	for method := range typ.Methods() {
		if keepSetMethod.MatchString(method.Name) {
			got = append(got, method.Name)
		}
	}

	slices.Sort(got)

	if want := storetest.KeepSetPortNames(); !slices.Equal(got, want) {
		t.Fatalf("keep set methods of the store = %v, rules cover %v; add a row to keepSetPorts", got, want)
	}
}
