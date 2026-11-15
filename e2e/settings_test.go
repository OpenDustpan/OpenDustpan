//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// settingsRoute is the route of the settings of the installation.
const settingsRoute = "/api/dustpan/v1/settings"

// settingItem is the part of one setting that the steps read.
type settingItem struct {
	Key           string `json:"key"`
	Value         any    `json:"value"`
	Source        string `json:"source"`
	Locked        bool   `json:"locked"`
	Secret        bool   `json:"secret"`
	Set           bool   `json:"set"`
	DatabaseLayer bool   `json:"databaseLayer"`
	DatabaseValue any    `json:"databaseValue"`
	Apply         string `json:"apply"`
}

// settingsDoc is the part of the settings resource that the steps read.
type settingsDoc struct {
	Revision  int64         `json:"revision"`
	Settings  []settingItem `json:"settings"`
	ErrorCode string        `json:"errorCode"`
	Message   string        `json:"message"`
}

// TestSettings reads and changes the settings resource through the API on a
// server of each store. No CLI reads it, so the steps call the routes with the
// token of the administrator, as the console does. They prove the effective
// value and the source of each setting, a change with its revision, the
// refusals (a stale revision, no revision, a setting that only a flag, a
// variable or the file sets, a value that is not valid), the delete of a
// database value, the resource of a namespace and the audit entries.
func TestSettings(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			settingsScenario(t, env)
		})
	}
}

// settingsReq is one request to a settings route: the path, the If-Match
// header and the body of a merge patch.
type settingsReq struct {
	path, ifMatch, body string
}

// typed returns the request as a typed request with the method and the token.
func (r settingsReq) typed(method, token string) e2e.TypedRequest {
	contentType := ""
	if r.body != "" {
		contentType = prefsPatchType
	}

	return e2e.TypedRequest{
		Token: token, Method: method, Path: r.path, ContentType: contentType, Body: r.body, IfMatch: r.ifMatch,
	}
}

// settingsPatch sends a merge patch with the token of the administrator, and
// returns the answer and its decoded body.
func settingsPatch(ctx context.Context, t *testing.T, env *e2e.Env, in settingsReq) (e2e.APIResponse, settingsDoc) {
	t.Helper()

	return settingsSend(ctx, t, env, in.typed(http.MethodPatch, env.AdminToken()))
}

// settingsSend sends the request and decodes the answer.
func settingsSend(
	ctx context.Context, t *testing.T, env *e2e.Env, in e2e.TypedRequest,
) (e2e.APIResponse, settingsDoc) {
	t.Helper()

	resp, err := env.ServerAPIStatusTyped(ctx, in)
	if err != nil {
		t.Fatalf("%s %s: %v", in.Method, in.Path, err)
	}

	var doc settingsDoc
	if err := json.Unmarshal([]byte(resp.Body), &doc); err != nil {
		t.Fatalf("%s %s answered %d with %q, not a JSON object: %v", in.Method, in.Path, resp.Status, resp.Body, err)
	}

	return resp, doc
}

// settingsRead reads path and fails unless it answers 200.
func settingsRead(ctx context.Context, t *testing.T, env *e2e.Env, path string) (e2e.APIResponse, settingsDoc) {
	t.Helper()

	resp, doc := settingsSend(ctx, t, env, e2e.TypedRequest{
		Token: env.AdminToken(), Method: http.MethodGet, Path: path, ContentType: "", Body: "", IfMatch: "",
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("GET %s answers %d (%s), want 200", path, resp.Status, resp.Body)
	}

	return resp, doc
}

// settingsAnswer is the answer that a step expects: the status and, for an
// error, the code.
type settingsAnswer struct {
	status int
	code   string
}

// settingsWant sends a patch and fails unless it answers want.
func settingsWant(ctx context.Context, t *testing.T, env *e2e.Env, in settingsReq, want settingsAnswer) settingsDoc {
	t.Helper()

	resp, doc := settingsPatch(ctx, t, env, in)
	if resp.Status != want.status || doc.ErrorCode != want.code {
		t.Fatalf("PATCH %s %s answers %d %q (%s), want %d %q",
			in.path, in.body, resp.Status, doc.ErrorCode, resp.Body, want.status, want.code)
	}

	return doc
}

func settingsScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()

	resp, doc := settingsRead(ctx, t, env, settingsRoute)
	first := resp.Header.Get("ETag")

	settingsDefaults(t, doc)
	settingsChange(ctx, t, env, first)
	settingsRefusals(ctx, t, env)
	settingsRemovalAlert(ctx, t, env)
	settingsNamespace(ctx, t, env)
	settingsAudit(ctx, t, env)
}

// settingsDefaults checks the settings of the first read: the defaults, the
// locks, the layers and a secret that shows no value.
func settingsDefaults(t *testing.T, doc settingsDoc) {
	t.Helper()

	preset := settingFor(t, doc, "branding.preset")
	if preset.Source != "default" || preset.Locked || !preset.DatabaseLayer || preset.Value != "" {
		t.Fatalf("branding.preset = %+v, want the default, unlocked, with a database layer", preset)
	}

	settingsPlainDefault(t, doc)

	if listen := settingFor(t, doc, "listen"); !listen.Locked || listen.DatabaseLayer || listen.Apply != "restart" {
		t.Fatalf("listen = %+v, want a setting that only the static layer sets", listen)
	}

	database := settingFor(t, doc, "database")
	if !database.Secret || database.Value != nil || database.DatabaseValue != nil || database.DatabaseLayer {
		t.Fatalf("database = %+v, want a secret that shows no value", database)
	}
}

// settingsPlainDefault checks that the first read shows plain connections as
// refused by default, unlocked, with a database layer, and applied live.
func settingsPlainDefault(t *testing.T, doc settingsDoc) {
	t.Helper()

	plain := settingFor(t, doc, "egress.allowPlainConnections")
	allowed, isBool := plain.Value.(bool)

	if plain.Source != "default" || plain.Locked || !plain.DatabaseLayer || !isBool || allowed ||
		plain.Apply != "live" {
		t.Fatalf("egress.allowPlainConnections = %+v, want the default, unlocked, with a database layer", plain)
	}
}

// settingFor returns the setting key of doc.
func settingFor(t *testing.T, doc settingsDoc, key string) settingItem {
	t.Helper()

	i := slices.IndexFunc(doc.Settings, func(s settingItem) bool { return s.Key == key })
	if i < 0 {
		t.Fatalf("the resource has no setting %s", key)
	}

	return doc.Settings[i]
}

// settingsChange proves a change with its revision: the patch needs the
// revision of the read, the answer holds the next one, a stale revision
// changes nothing, a patch that changes nothing keeps the revision, and null
// deletes the database value.
func settingsChange(ctx context.Context, t *testing.T, env *e2e.Env, first string) {
	t.Helper()

	resp, doc := settingsPatch(ctx, t, env, settingsReq{
		path: settingsRoute, ifMatch: first, body: `{"branding.preset":"high-contrast"}`,
	})
	if resp.Status != http.StatusOK || resp.Header.Get("ETag") == first {
		t.Fatalf("PATCH answers %d %s with the ETag %q (was %q), want 200 and a new revision",
			resp.Status, resp.Body, resp.Header.Get("ETag"), first)
	}

	if preset := settingFor(t, doc, "branding.preset"); preset.Value != "high-contrast" ||
		preset.Source != "database" || preset.DatabaseValue != "high-contrast" {
		t.Fatalf("branding.preset = %+v, want the value from the database", preset)
	}

	second := resp.Header.Get("ETag")

	// The old revision no longer matches, and nothing changes.
	settingsWant(ctx, t, env, settingsReq{path: settingsRoute, ifMatch: first, body: `{"branding.preset":"other"}`},
		settingsAnswer{status: http.StatusPreconditionFailed, code: "request.precondition-failed"})

	if read, _ := settingsRead(ctx, t, env, settingsRoute); read.Header.Get("ETag") != second {
		t.Fatalf("ETag after a refused change = %q, want %q", read.Header.Get("ETag"), second)
	}

	settingsKeepAndDelete(ctx, t, env, second)
}

// settingsKeepAndDelete proves that the same value keeps the revision, and
// that null deletes the database value.
func settingsKeepAndDelete(ctx context.Context, t *testing.T, env *e2e.Env, tag string) {
	t.Helper()

	same, _ := settingsPatch(ctx, t, env, settingsReq{
		path: settingsRoute, ifMatch: tag, body: `{"branding.preset":"high-contrast"}`,
	})
	if same.Status != http.StatusOK || same.Header.Get("ETag") != tag {
		t.Fatalf("PATCH of the same value answers %d with the ETag %q, want 200 and %q",
			same.Status, same.Header.Get("ETag"), tag)
	}

	deleted, doc := settingsPatch(ctx, t, env, settingsReq{
		path: settingsRoute, ifMatch: tag, body: `{"branding.preset":null}`,
	})
	if preset := settingFor(t, doc, "branding.preset"); deleted.Status != http.StatusOK ||
		preset.Source != "default" || preset.Value != "" || preset.DatabaseValue != nil {
		t.Fatalf("DELETE by null answers %d with %+v, want the default", deleted.Status, preset)
	}
}

// settingsRefusals proves the refusals and that none of them changes the
// resource.
func settingsRefusals(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	before, _ := settingsRead(ctx, t, env, settingsRoute)
	tag := before.Header.Get("ETag")

	refusals := []struct {
		name string
		in   settingsReq
		want settingsAnswer
	}{
		{
			"no revision",
			settingsReq{settingsRoute, "", `{"branding.preset":"a"}`},
			settingsAnswer{http.StatusPreconditionRequired, "request.precondition-required"},
		},
		{
			"a revision that is not valid",
			settingsReq{settingsRoute, "3", `{"branding.preset":"a"}`},
			settingsAnswer{http.StatusBadRequest, "request.if-match-invalid"},
		},
		{
			"a setting that only the static layer sets",
			settingsReq{settingsRoute, tag, `{"listen":":9999"}`},
			settingsAnswer{http.StatusConflict, "settings.static-only"},
		},
		{
			"a secret",
			settingsReq{settingsRoute, tag, `{"database":"sqlite:x"}`},
			settingsAnswer{http.StatusConflict, "settings.static-only"},
		},
		{
			"a setting that does not exist",
			settingsReq{settingsRoute, tag, `{"nope":1}`},
			settingsAnswer{http.StatusBadRequest, "settings.key-unknown"},
		},
		{
			"a value that is not a preset name",
			settingsReq{settingsRoute, tag, `{"branding.preset":"Not Valid"}`},
			settingsAnswer{http.StatusBadRequest, "settings.value-invalid"},
		},
		{
			"a value of the wrong kind",
			settingsReq{settingsRoute, tag, `{"branding.preset":5}`},
			settingsAnswer{http.StatusBadRequest, "settings.value-invalid"},
		},
		{
			"a body that is not an object",
			settingsReq{settingsRoute, tag, `[]`},
			settingsAnswer{http.StatusBadRequest, "settings.patch-invalid"},
		},
	}

	for _, tc := range refusals {
		doc := settingsWant(ctx, t, env, tc.in, tc.want)
		if strings.Contains(doc.Message, "sqlite:x") {
			t.Fatalf("%s: the message repeats the value of the request: %s", tc.name, doc.Message)
		}
	}

	if after, _ := settingsRead(ctx, t, env, settingsRoute); after.Header.Get("ETag") != tag {
		t.Fatalf("ETag after the refusals = %q, want %q", after.Header.Get("ETag"), tag)
	}

	settingsMediaAndCredential(ctx, t, env, tag)
}

// settingsRemovalAlert proves the three settings of the removal alert: one
// patch sets them, the resource shows each value from the database, and each
// one refuses a value outside its bounds. Each value is tighter than the
// default, so no change reduces security and none needs a fresh
// authentication.
func settingsRemovalAlert(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	read, _ := settingsRead(ctx, t, env, settingsRoute)
	tag := read.Header.Get("ETag")

	set, doc := settingsPatch(ctx, t, env, settingsReq{
		path: settingsRoute, ifMatch: tag,
		body: `{"principal.removalAlertCount":5,"principal.removalAlertPercent":5,` +
			`"principal.removalAlertWindow":"20m"}`,
	})
	if set.Status != http.StatusOK {
		t.Fatalf("PATCH of the removal alert settings answers %d %s, want 200", set.Status, set.Body)
	}

	want := map[string]any{
		"principal.removalAlertCount":   float64(5),
		"principal.removalAlertPercent": float64(5),
		"principal.removalAlertWindow":  "20m",
	}

	for key, value := range want {
		item := settingFor(t, doc, key)
		if item.Source != "database" || item.Value != value || item.DatabaseValue != value {
			t.Fatalf("%s = %+v, want %v from the database", key, item, value)
		}
	}

	tag = set.Header.Get("ETag")

	refusals := []struct {
		name string
		body string
	}{
		{"a count above the bounds", `{"principal.removalAlertCount":10001}`},
		{"a count that is not a whole number", `{"principal.removalAlertCount":1.5}`},
		{"a percent below the bounds", `{"principal.removalAlertPercent":0}`},
		{"a window above the bounds", `{"principal.removalAlertWindow":"25h"}`},
		{"a window that is not whole seconds", `{"principal.removalAlertWindow":"10m0.5s"}`},
	}

	for _, tc := range refusals {
		settingsWant(ctx, t, env, settingsReq{path: settingsRoute, ifMatch: tag, body: tc.body},
			settingsAnswer{status: http.StatusBadRequest, code: "settings.value-invalid"})
	}
}

// settingsMediaAndCredential proves that a request with the wrong content
// type is refused, and that a request with no credential is refused.
func settingsMediaAndCredential(ctx context.Context, t *testing.T, env *e2e.Env, tag string) {
	t.Helper()

	resp, err := env.ServerAPIStatusTyped(ctx, e2e.TypedRequest{
		Token: env.AdminToken(), Method: http.MethodPatch, Path: settingsRoute, ContentType: "application/json",
		Body: `{}`, IfMatch: tag,
	})
	if err != nil || resp.Status != http.StatusUnsupportedMediaType {
		t.Fatalf("PATCH as application/json = %v, %v; want 415", resp, err)
	}

	resp, err = env.ServerAPIStatusTyped(ctx, e2e.TypedRequest{
		Token: "", Method: http.MethodGet, Path: settingsRoute, ContentType: "", Body: "", IfMatch: "",
	})
	if err != nil || resp.Status != http.StatusUnauthorized {
		t.Fatalf("GET with no credential = %v, %v; want 401", resp, err)
	}
}

// settingsNamespace proves the resource of a namespace: it reads, it takes a
// patch that changes nothing, and it refuses a key of the installation.
func settingsNamespace(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	env.CreateNamespace(ctx, t, "settings-ns")

	path := "/api/dustpan/v1/namespaces/settings-ns/settings"

	read, _ := settingsRead(ctx, t, env, path)
	tag := read.Header.Get("ETag")

	settingsWant(
		ctx,
		t,
		env,
		settingsReq{path: path, ifMatch: tag, body: `{}`},
		settingsAnswer{status: http.StatusOK, code: ""},
	)
	settingsWant(ctx, t, env, settingsReq{path: path, ifMatch: tag, body: `{"branding.preset":"x"}`},
		settingsAnswer{status: http.StatusBadRequest, code: "settings.key-unknown"})

	resp, _ := settingsSend(ctx, t, env, e2e.TypedRequest{
		Token: env.AdminToken(), Method: http.MethodGet, Path: "/api/dustpan/v1/namespaces/no-such-namespace/settings",
		ContentType: "", Body: "", IfMatch: "",
	})
	if resp.Status != http.StatusNotFound {
		t.Fatalf("GET of a namespace that does not exist answers %d, want 404", resp.Status)
	}
}

// settingsAudit proves that each real change wrote one entry in the server
// scope, and each refused request one failure entry.
func settingsAudit(ctx context.Context, t *testing.T, env *e2e.Env) {
	t.Helper()

	entries, err := fetchAuditEntries(ctx, env, "@server")
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	counts := map[string]int{}

	for i := range entries {
		if entries[i].Action == "server:manage-settings" && entries[i].Channel == "api" {
			counts[entries[i].Outcome]++
		}
	}

	// Three changes (the set, the delete and the removal alert settings), and
	// fourteen refusals that the server attributes to a change: the eight of
	// settingsRefusals, the request with the wrong media type and the five of
	// the removal alert settings. A stale revision changes nothing and writes
	// none. The refusal in the scope of the namespace is not in this scope.
	if counts["success"] != 3 || counts["failure"] != 14 {
		t.Fatalf("settings entries by outcome = %v, want 3 successes and 14 failures", counts)
	}
}
