package profile_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	apiprofile "opendustpan.org/opendustpan/internal/api/profile"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// callCounter is the counter that the fixture hands to the handler.
type callCounter struct {
	opens atomic.Int64
	reads atomic.Int64
	svc   *profile.Service
}

func (c *callCounter) Open(ctx context.Context, a *profile.Actor, in profile.OpenInput) (profile.Opened, error) {
	c.opens.Add(1)

	return c.svc.Open(ctx, a, in)
}

func (c *callCounter) ReadSession(
	ctx context.Context, a *profile.Actor, ns namespace.Name, id string, paths []string,
) (profile.SessionData, error) {
	c.reads.Add(1)

	return c.svc.ReadSession(ctx, a, ns, id, paths)
}

// countedFixture returns a fixture whose handler calls the open service
// through a counter.
func countedFixture(t *testing.T) (fixture, *callCounter) {
	t.Helper()

	counter := &callCounter{opens: atomic.Int64{}, reads: atomic.Int64{}, svc: nil}

	f := buildFixtureWith(t, keyMiddleware,
		func(cfg *apiprofile.Config, _ *sqlite.Store, _ *namespace.Service) {
			counter.svc = cfg.Profiles
			cfg.Opens = counter
		})

	return f, counter
}

// TestOpenHandlerRefusesHostileInputBeforeTheService sends hostile open and
// read requests to the handler. Each answer is 400 or 404 and no call reaches
// the service: every guard of the handler has its own failing row. It fails
// when a guard of the handler goes, even though the service refuses the same
// input.
func TestOpenHandlerRefusesHostileInputBeforeTheService(t *testing.T) {
	t.Parallel()

	f, counter := countedFixture(t)
	f.create(t, "root", secretDoc)

	many := make([]string, profile.MaxSessionPaths+1)
	for i := range many {
		many[i] = `"region"`
	}

	long := strings.Repeat("a", profile.MaxSessionPathBytes+1)

	for name, body := range map[string]string{
		"too many paths":      `{"consumer":"api","paths":[` + strings.Join(many, ",") + `]}`,
		"empty path":          `{"consumer":"api","paths":[""]}`,
		"NUL in a path":       `{"consumer":"api","paths":["a\u0000b"]}`,
		"CR in a path":        `{"consumer":"api","paths":["a\rb"]}`,
		"LF in a path":        `{"consumer":"api","paths":["a\nb"]}`,
		"DEL in a path":       `{"consumer":"api","paths":["a\u007fb"]}`,
		"C1 in a path":        `{"consumer":"api","paths":["a\u0085b"]}`,
		"invalid UTF-8":       "{\"consumer\":\"api\",\"paths\":[\"a\xffb\"]}",
		"long path":           `{"consumer":"api","paths":["` + long + `"]}`,
		"empty segment":       `{"consumer":"api","paths":["a..b"]}`,
		"bad escape":          `{"consumer":"api","paths":["a\\b"]}`,
		"unknown consumer":    `{"consumer":"run"}`,
		"no consumer":         `{}`,
		"duration not a time": `{"consumer":"api","duration":"soon"}`,
		"duration of zero":    `{"consumer":"api","duration":"0s"}`,
		"duration below min":  `{"consumer":"api","duration":"30s"}`,
		"duration negative":   `{"consumer":"api","duration":"-1h"}`,
		"duration above max":  `{"consumer":"api","duration":"13h"}`,
	} {
		got := f.do(t, send("admin", http.MethodPost, base+"/root/open", body))
		wantCode(t, name, got, http.StatusBadRequest)
		errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, []byte(got.body),
			errcodetest.Want{Code: errcode.ProfileInvalid, Args: nil, RequestBody: nil})
	}

	if n := counter.opens.Load(); n != 0 {
		t.Errorf("%d hostile opens reached the service, want 0", n)
	}

	// The reads of a session that exists, so that only a handler guard stops
	// them.
	id := openSession(t, f, openBody(t))
	reads := counter.reads.Load()

	for _, raw := range []string{"%00", "%0d", "%0a", "%7f", "%c2%85", "%ff", "a..b", "a%5Cb", url.QueryEscape(long)} {
		got := readSession(t, f, id, "?path="+raw)
		wantCode(t, "path "+raw, got, http.StatusBadRequest)
	}

	for _, forged := range []string{"pos-", "ses-0001", "pos-%2F", "pos-A", "pos-a%00b", id + "X"} {
		got := readSession(t, f, forged, "")
		wantCode(t, "session "+forged, got, http.StatusNotFound)
	}

	// id+"X" is the one forged ID that the handler cannot tell from a good
	// one: its alphabet is lower case, so it is refused too.
	if n := counter.reads.Load() - reads; n != 0 {
		t.Errorf("%d hostile reads reached the service, want 0", n)
	}

	// A good open and a good read reach the service: the counter counts.
	if counter.opens.Load() != 1 {
		t.Errorf("opens = %d, want 1 from the good open", counter.opens.Load())
	}

	got := readSession(t, f, id, "?path=region")
	wantCode(t, "good read", got, http.StatusOK)

	if counter.reads.Load()-reads != 1 {
		t.Errorf("good reads = %d, want 1", counter.reads.Load()-reads)
	}
}

// TestOpenHandlerOwnerIsTheCaller opens as another principal that holds
// profile:open. The owner reads, admin gets 404, and the session stays.
func TestOpenHandlerOwnerIsTheCaller(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	got := f.do(t, send("opener", http.MethodPost, base+"/root/open", openBody(t)))
	wantCode(t, "open as opener", got, http.StatusCreated)

	var wire openWire
	if err := json.Unmarshal([]byte(got.body), &wire); err != nil {
		t.Fatalf("decode open: %v", err)
	}

	read := func(token string) response {
		return f.do(t, get(token, sessionBase+"/"+wire.SessionID))
	}

	wantCode(t, "owner reads", read("opener"), http.StatusOK)
	wantCode(t, "admin reads the session of opener", read("admin"), http.StatusNotFound)
	wantCode(t, "owner reads again", read("opener"), http.StatusOK)
}

// TestOpenHandlerRuntimeErrorNamesNoValue opens a profile with an expression
// that fails when it runs, on a profile that holds a secret: the 422 body
// names no value.
func TestOpenHandlerRuntimeErrorNamesNoValue(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{"int(values.db.password)", "float(values.db.password)"} {
		f := newFixture(t)
		f.create(t, "root", "schemaVersion: 1\nvalues:\n  db:\n    password: { $secret: hunter2 }\n  bad: ${"+
			expression+"}\n")

		got := f.do(t, send("admin", http.MethodPost, base+"/root/open", openBody(t)))
		wantCode(t, expression, got, http.StatusUnprocessableEntity)

		if strings.Contains(got.body, "hunter2") {
			t.Errorf("%s: the 422 body names a value: %s", expression, got.body)
		}

		errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusUnprocessableEntity, []byte(got.body),
			errcodetest.Want{Code: errcode.ProfileReference, Args: nil, RequestBody: nil})
	}
}
