package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/logtext"
	"opendustpan.org/opendustpan/internal/logtext/logtexttest"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	damagedToolText = "evil tool<script>"
	damagedNameText = "secret//name"
	damagedToolID   = "stk-damaged-tool"
	damagedNameID   = "stk-damaged-name"
	damagedColonID  = "stk-damaged-colon"
)

// damagedEntries is unitEntries with three stored rows that the resolver must
// not serve: a tool that is not valid, a unit name with an empty segment, and
// a unit name with a colon.
func damagedEntries() map[namespace.Name][]coreinventory.Entry {
	entries := unitEntries()

	acme := entries["acme"]
	if len(acme) == 0 {
		panic("unitEntries holds no unit of acme")
	}

	base := acme[0]

	badTool := base
	badTool.Tool, badTool.UnitID, badTool.Name = damagedToolText, damagedToolID, "web/dev"

	badName := base
	badName.Tool, badName.UnitID, badName.Name = "pulumi", damagedNameID, damagedNameText

	badColon := base
	badColon.Tool, badColon.UnitID, badColon.Name = "opentofu", damagedColonID, "a:b"

	entries["acme"] = append(entries["acme"], badTool, badName, badColon)

	return entries
}

// answer is the part of a response that a caller sees.
type answer struct {
	header http.Header
	body   string
	status int
}

func answerOf(rec *httptest.ResponseRecorder) answer {
	return answer{header: rec.Header().Clone(), body: rec.Body.String(), status: rec.Code}
}

// TestUnitByIDHostile proves that each input that names no unit that the
// caller may read gets the answer of an unknown ID: the same status, body and
// headers. This holds for a caller that holds every grant and for a caller
// that holds none. A stored row that fails its checks is one of the inputs: it
// must not answer another status, and the answer must not repeat the stored
// text.
func TestUnitByIDHostile(t *testing.T) {
	t.Parallel()

	for _, caller := range hostileCallers() {
		t.Run(caller.name, func(t *testing.T) {
			t.Parallel()

			handler := newUnitHandlerWithEntries(t, caller.authz, damagedEntries())

			want := answerOf(getUnitByID(t, handler, "stk-unknown", "test-token"))
			if want.status != http.StatusNotFound || want.body != notFoundBody {
				t.Fatalf("unknown ID: status %d body %q, want 404 %q", want.status, want.body, notFoundBody)
			}

			for _, row := range hostileRows() {
				t.Run(row.name, func(t *testing.T) {
					t.Parallel()

					checkSameAnswer(t, getUnitByID(t, handler, row.id, "test-token"), want, row.stored)
				})
			}
		})
	}
}

type hostileCaller struct {
	authz identity.Authorizer
	name  string
}

func hostileCallers() []hostileCaller {
	return []hostileCaller{
		{grantAuthorizer{allow: map[string]bool{
			grant(identity.ActionNamespaceRead, "acme"):            true,
			grant(identity.ActionUnitRead, "pulumi:acme/web/prod"): true,
		}}, "caller of acme with every grant"},
		{grantAuthorizer{allow: nil}, "caller with no grant"},
		{grantAuthorizer{allow: map[string]bool{
			grant(identity.ActionNamespaceRead, "acme"):            true,
			grant(identity.ActionUnitRead, "pulumi:acme/web/prod"): true,
			grant(identity.ActionUnitRead, "opentofu:globex/net"):  true,
		}}, "caller of acme with a unit grant in globex"},
	}
}

type hostileRow struct {
	name string
	id   string
	// stored lists the stored text that no answer may repeat.
	stored []string
}

func hostileRows() []hostileRow {
	return []hostileRow{
		{"another namespace", globexUnitID, nil},
		{"deleted unit", deletedUnitByID, nil},
		{"unit name in place of the ID", "web%2Fprod", nil},
		{"namespace name in place of the ID", "acme", nil},
		{"tool name in place of the ID", "pulumi", nil},
		{"empty-looking ID", "%20", nil},
		{"ID with a NUL byte", "stk-acme%00", nil},
		{"ID with a CR and an LF", "stk-acme%0d%0aX-Injected:%201", nil},
		{"ID with a path escape", "..%2f..%2fetc%2fpasswd", nil},
		{"ID with a quote and a tag", "%22%3Cscript%3E", nil},
		{"ID of 5000 bytes", strings.Repeat("a", 5000), nil},
		{"stored row with a tool that is not valid", damagedToolID, []string{"evil", "script", "web/dev"}},
		{"stored row with an empty name segment", damagedNameID, []string{"secret", "//"}},
		{"stored row with a colon in the name", damagedColonID, []string{"a:b"}},
	}
}

// TestUnitByIDDamagedRowNeverAuthorized proves that a stored row that fails
// its checks is answered before any authorization check: the authorizer sees
// no check on an object that the row names, so a caller cannot learn from the
// order of the answers that the row exists.
func TestUnitByIDDamagedRowNeverAuthorized(t *testing.T) {
	t.Parallel()

	for _, id := range []string{damagedToolID, damagedNameID, damagedColonID} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			authz := &recordingGrantAuthorizer{seen: nil}
			handler := newUnitHandlerWithEntries(t, authz, damagedEntries())

			rec := getUnitByID(t, handler, id, "test-token")
			if rec.Code != http.StatusNotFound || rec.Body.String() != notFoundBody {
				t.Fatalf("status %d body %q, want 404 %q", rec.Code, rec.Body.String(), notFoundBody)
			}

			if len(authz.seen) != 0 {
				t.Errorf("authorization checks = %v, want none before the 404", authz.seen)
			}
		})
	}
}

// recordingGrantAuthorizer allows nothing and records each check.
type recordingGrantAuthorizer struct {
	seen []string
}

func (a *recordingGrantAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	a.seen = append(a.seen, grant(action, obj.String()))

	return fmt.Errorf("%w: %s may not %s", identity.ErrDenied, p.Subject, action)
}

func (*recordingGrantAuthorizer) ListObjects(
	_ context.Context, p identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// TestUnitNotFoundFixedText proves that the classifier answers the fixed text
// for an error that holds the sentinel of an unknown unit, whatever text the
// wrap adds. Each case names the stored tool and name in its error text, as a
// careless wrap would. This test feeds the classifier directly, so it does not
// rely on the resolver to return a bare sentinel.
func TestUnitNotFoundFixedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		name string
	}{
		{
			fmt.Errorf("%w: tool %q name %q", coreinventory.ErrUnitNotFound, damagedToolText, damagedNameText),
			"the sentinel of the inventory",
		},
		{
			fmt.Errorf(
				"%w: %w: unit %q", access.ErrNotFound, access.ErrUnitMissing, damagedToolText+":"+damagedNameText,
			),
			"the sentinel of the access check",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			apiinventory.WriteError(t.Context(), rec, tc.err)

			if rec.Code != http.StatusNotFound || rec.Body.String() != notFoundBody {
				t.Errorf("status %d body %q, want 404 %q", rec.Code, rec.Body.String(), notFoundBody)
			}

			for _, text := range []string{"evil", "script", "secret"} {
				if strings.Contains(rec.Body.String(), text) {
					t.Errorf("body repeats the text %q of the error: %s", text, rec.Body.String())
				}
			}
		})
	}
}

// checkSameAnswer fails when rec differs from want in status, body or headers,
// or when the body repeats a text of stored.
func checkSameAnswer(t *testing.T, rec *httptest.ResponseRecorder, want answer, stored []string) {
	t.Helper()

	got := answerOf(rec)

	if got.status != want.status || got.body != want.body {
		t.Errorf("status %d body %q, want status %d body %q", got.status, got.body, want.status, want.body)
	}

	if !reflect.DeepEqual(got.header, want.header) {
		t.Errorf("headers = %v, want the headers of an unknown ID %v", got.header, want.header)
	}

	for _, text := range stored {
		if strings.Contains(got.body, text) {
			t.Errorf("body repeats the stored text %q: %s", text, got.body)
		}
	}
}

var errUnitStoreDown = errors.New("unit store is unavailable")

// countingReader is a unit reader that counts GetUnit calls and may fail them.
type countingReader struct {
	coreinventory.Reader

	err  error
	gets atomic.Int64
}

func (c *countingReader) GetUnit(ctx context.Context, unitID string) (coreinventory.Entry, error) {
	c.gets.Add(1)

	if c.err != nil {
		return coreinventory.Entry{}, c.err
	}

	return c.Reader.GetUnit(ctx, unitID)
}

// TestUnitByIDMalformedIDNeverReachesTheStore proves that the resolver refuses
// an ID that is malformed or too long before it reads the store, for a caller
// that holds every grant.
func TestUnitByIDMalformedIDNeverReachesTheStore(t *testing.T) {
	t.Parallel()

	ids := []string{
		"%20", "ws~bad", "a%20b", "stk-1%00", "stk-1%0d%0aX:%201", "%22%3Cscript%3E", ".hidden", "-lead",
		strings.Repeat("a", 101), strings.Repeat("a", 5000),
	}

	for _, id := range ids {
		t.Run(fmt.Sprintf("ID %.20q", id), func(t *testing.T) {
			t.Parallel()

			reader := &countingReader{
				Reader: &memReader{entries: damagedEntries(), err: nil},
				err:    nil,
				gets:   atomic.Int64{},
			}
			handler := newUnitHandlerLogged(t, hostileCallers()[0].authz, reader, slog.New(slog.DiscardHandler))

			rec := getUnitByID(t, handler, id, "test-token")
			if rec.Code != http.StatusNotFound || rec.Body.String() != notFoundBody {
				t.Errorf("status %d body %q, want 404 %q", rec.Code, rec.Body.String(), notFoundBody)
			}

			if n := reader.gets.Load(); n != 0 {
				t.Errorf("GetUnit calls = %d, want 0", n)
			}
		})
	}
}

// TestUnitByIDStorageFailureAnswersAnError proves that a GetUnit failure that
// is not an unknown unit answers 500, not the 404 of an unknown unit.
func TestUnitByIDStorageFailureAnswersAnError(t *testing.T) {
	t.Parallel()

	reader := &countingReader{
		Reader: &memReader{entries: damagedEntries(), err: nil},
		err:    errUnitStoreDown, gets: atomic.Int64{},
	}
	handler := newUnitHandlerLogged(t, hostileCallers()[0].authz, reader, slog.New(slog.DiscardHandler))

	if rec := getUnitByID(t, handler, "stk-acme", "test-token"); rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d body %q, want 500", rec.Code, rec.Body.String())
	}
}

// TestUnitByIDDamagedRowIsLoggedNotServed proves that the log names the unit ID
// and the cause of a stored row that fails its checks, that the log bounds the
// stored text, and that the answer holds neither.
func TestUnitByIDDamagedRowIsLoggedNotServed(t *testing.T) {
	t.Parallel()

	for _, id := range []string{damagedToolID, damagedNameID, damagedColonID} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			var logs strings.Builder

			handler := newUnitHandlerLogged(t, hostileCallers()[0].authz,
				&memReader{entries: damagedEntries(), err: nil}, slog.New(slog.NewTextHandler(&logs, nil)))

			rec := getUnitByID(t, handler, id, "test-token")
			if rec.Code != http.StatusNotFound || rec.Body.String() != notFoundBody {
				t.Fatalf("status %d body %q, want 404 %q", rec.Code, rec.Body.String(), notFoundBody)
			}

			checkDamagedLog(t, logs.String(), rec.Body.String(), id)
		})
	}
}

// TestUnitByIDLogBoundsStoredText proves that a stored tool of 5000 bytes does
// not make a log line of 5000 bytes.
func TestUnitByIDLogBoundsStoredText(t *testing.T) {
	t.Parallel()

	entries := damagedEntries()
	acme := entries["acme"]
	if len(acme) < 2 {
		t.Fatal("damagedEntries holds fewer than two units of acme")
	}

	acme[len(acme)-2].Name = strings.Repeat("x/", 2500)
	entries["acme"] = acme

	var logs strings.Builder

	handler := newUnitHandlerLogged(t, hostileCallers()[0].authz,
		&memReader{entries: entries, err: nil}, slog.New(slog.NewTextHandler(&logs, nil)))

	getUnitByID(t, handler, damagedNameID, "test-token")

	if logs.Len() == 0 || logs.Len() > 1000 {
		t.Errorf("log line of %d bytes, want 1..1000: %.100s", logs.Len(), logs.String())
	}
}

// checkDamagedLog fails when the log lacks the unit ID or the cause, or when
// the body repeats a stored text.
func checkDamagedLog(t *testing.T, logText, body, id string) {
	t.Helper()

	if !strings.Contains(logText, id) || !strings.Contains(logText, "stored unit has no valid object") {
		t.Errorf("log %q does not name the unit ID %q and the cause", logText, id)
	}

	for _, text := range []string{"evil", "secret", "a:b"} {
		if strings.Contains(body, text) {
			t.Errorf("body repeats the stored text %q: %s", text, body)
		}
	}
}

// wrongIDReader is a unit reader that answers each GetUnit with the row it
// holds, whatever unit ID it was asked for.
type wrongIDReader struct {
	coreinventory.Reader

	row coreinventory.Entry
}

func (w wrongIDReader) GetUnit(context.Context, string) (coreinventory.Entry, error) {
	return w.row, nil
}

// TestUnitByIDRechecksUnitID proves, with a store that ignores the unit ID,
// that the resolver alone refuses a row of another unit ID, for a caller that
// holds every grant.
func TestUnitByIDRechecksUnitID(t *testing.T) {
	t.Parallel()

	acme := unitEntries()["acme"]
	if len(acme) == 0 {
		t.Fatal("unitEntries holds no unit of acme")
	}

	row := acme[0]

	tests := []struct {
		name string
		id   string
		want int
	}{
		{"row of another unit ID", "stk-asked", http.StatusNotFound},
		{"control: row of the unit ID", row.UnitID, http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := wrongIDReader{Reader: &memReader{entries: unitEntries(), err: nil}, row: row}
			handler := newUnitHandlerLogged(t, hostileCallers()[0].authz, reader, slog.New(slog.DiscardHandler))

			rec := getUnitByID(t, handler, tc.id, "test-token")
			if rec.Code != tc.want {
				t.Errorf("status %d body %q, want %d", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

// TestUnitByIDLogHoldsNoLineBreak proves that the log line of a damaged row
// holds no CR and no LF and stays bounded. A text handler would quote a line
// break, so the test reads the raw values.
func TestUnitByIDLogHoldsNoLineBreak(t *testing.T) {
	t.Parallel()

	entries := damagedEntries()
	acme := entries["acme"]
	if len(acme) < 3 {
		t.Fatal("damagedEntries holds fewer than three units of acme")
	}

	// damagedEntries ends with the rows of a tool, a name and a colon that fail.
	acme[len(acme)-3].Tool = "evil\r\nERROR forged " + strings.Repeat("x", 5000)
	acme[len(acme)-2].Name = "a\r\nb//" + strings.Repeat("y", 5000)
	entries["acme"] = acme

	rec := logtexttest.New()
	handler := newUnitHandlerLogged(t, hostileCallers()[0].authz, &memReader{entries: entries, err: nil}, rec.Logger())

	for _, id := range []string{damagedToolID, damagedNameID, damagedColonID} {
		getUnitByID(t, handler, id, "test-token")
	}

	records := rec.Records()
	if len(records) == 0 {
		t.Fatal("no log record for a damaged row")
	}

	for _, r := range records {
		for key, value := range r.Attrs {
			if strings.ContainsAny(value, "\r\n") {
				t.Errorf("log attribute %s holds a line break: %q", key, value)
			}

			if len(value) > logtext.MaxBytes+len("...") {
				t.Errorf("log attribute %s has %d bytes, want at most %d", key, len(value), logtext.MaxBytes+3)
			}
		}
	}
}
