package problem

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// TestWriteProblemKeepsTheCatalogArgumentsOverTheDenialArguments checks that a
// code that declares arguments answers with the arguments of the catalog, also
// when the problem carries the arguments of a denial. The arguments of a
// denial belong to a code that declares none.
func TestWriteProblemKeepsTheCatalogArgumentsOverTheDenialArguments(t *testing.T) {
	t.Parallel()

	p := New(http.StatusConflict, errcode.SettingsStaticOnly, "the static layer sets the setting",
		errcode.Identifier("key", "audit.retention"))
	p.denialArgs = map[string]any{"action": "unit:read", "objectKind": "unit"}

	var buf bytes.Buffer

	wr := Writer{Logger: slog.New(slog.NewTextHandler(&buf, nil)), KeepDenied: nil, Classify: nil}
	rec := httptest.NewRecorder()

	wr.WriteProblem(t.Context(), rec, p)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}

	var got Body
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body, err)
	}

	want := map[string]any{"key": "audit.retention"}
	if !reflect.DeepEqual(got.ErrorArgs, want) {
		t.Errorf("errorArgs = %v, want %v", got.ErrorArgs, want)
	}

	for _, denialArg := range []string{"action", "objectKind"} {
		if _, ok := got.ErrorArgs[denialArg]; ok {
			t.Errorf("errorArgs holds the denial argument %q: %v", denialArg, got.ErrorArgs)
		}
	}
}
