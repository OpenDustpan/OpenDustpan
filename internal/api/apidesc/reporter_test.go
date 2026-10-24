package apidesc_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// readUndeclared reads the undeclared name evil of the list route, in the mode
// of the server, with rep on the context of the request.
func readUndeclared(t *testing.T, rep *apidesc.Reporter) {
	t.Helper()

	r := httptest.NewRequestWithContext(
		apidesc.WithReporter(t.Context(), rep), http.MethodGet, "/api/dustpan/v1/namespaces/acme/things?evil=1",
		http.NoBody,
	)
	r.Pattern = "GET " + listPath

	if got := apidesc.QueryOfServer(r, thingLabels()).Get("evil"); got != "" {
		t.Fatalf("Get(evil) = %q, want no value", got)
	}
}

// TestEachServerReportsToItsOwnLogger proves that two servers in one process
// keep their reports apart: each read goes to the logger of the Reporter on
// the context of its request, and a name that one server reported is still
// reported by the other.
func TestEachServerReportsToItsOwnLogger(t *testing.T) {
	t.Parallel()

	var bufA, bufB bytes.Buffer

	repA := apidesc.NewReporter(slog.New(slog.NewTextHandler(&bufA, nil)))
	repB := apidesc.NewReporter(slog.New(slog.NewTextHandler(&bufB, nil)))

	readUndeclared(t, repA)
	readUndeclared(t, repA)
	readUndeclared(t, repB)

	if got := strings.Count(bufA.String(), "name=evil"); got != 1 {
		t.Errorf("the log of server A holds name=evil %d times, want once:\n%s", got, bufA.String())
	}

	if got := strings.Count(bufB.String(), "name=evil"); got != 1 {
		t.Errorf("the log of server B holds name=evil %d times, want once:\n%s", got, bufB.String())
	}
}

// TestNewReporterWithNoLogger proves that a Reporter with no logger reports
// through the default logger of slog and does not panic.
func TestNewReporterWithNoLogger(t *testing.T) {
	t.Parallel()

	apidesc.NewReporter(nil).ReportInServer("query parameter", "GET /api/dustpan/v1/things", "evil")
}
