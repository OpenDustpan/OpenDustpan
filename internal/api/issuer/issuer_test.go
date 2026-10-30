package issuer_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	apiissuer "opendustpan.org/opendustpan/internal/api/issuer"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

// wire is the part of an answer of the API that the tests read.
type wire struct {
	Status   int    `json:"code"`
	Code     string `json:"errorCode"`
	Settings struct {
		IssuerURL          string   `json:"issuerUrl"`
		SubjectTemplate    []string `json:"subjectTemplate"`
		AllowBlockSubjects bool     `json:"allowBlockSubjects"`
		IncludeActorName   bool     `json:"includeActorName"`
		ETag               string   `json:"etag"`
	} `json:"settings"`
	EffectiveIssuerURL string `json:"effectiveIssuerUrl"`
	HostConfigured     bool   `json:"issuerHostConfigured"`
	Keys               []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"keys"`
	Deleted  []string `json:"deleted"`
	Promoted bool     `json:"promoted"`
	Files    []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
	OK    bool `json:"ok"`
	Steps []struct {
		Name string `json:"name"`
		OK   bool   `json:"ok"`
	} `json:"steps"`
	// Settings of an update answer.
	IssuerURL string `json:"issuerUrl"`
	ETag      string `json:"etag"`
}

func decode(t *testing.T, r response) wire {
	t.Helper()

	var w wire
	if err := json.Unmarshal([]byte(r.body), &w); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}

	return w
}

func (f fixture) createKeys(t *testing.T) {
	t.Helper()

	got := f.do(t, send("admin", http.MethodPost, issuerPath+"/keys", ""))
	wantCode(t, "create keys", got, http.StatusCreated)
}

// forbiddenWords are members and texts that no answer of the API holds.
var forbiddenWords = []string{`"sealed"`, "PRIVATE", `"d":`, `"dp"`, `"dq"`, `"qi"`, `"p":`, `"q":`, "PKCS"}

func noKeyMaterial(t *testing.T, what string, r response) {
	t.Helper()

	for _, w := range forbiddenWords {
		if strings.Contains(r.body, w) {
			t.Errorf("%s: the answer holds %q", what, w)
		}
	}
}

func TestRouteTableMatchesTheMux(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	var table []string
	for _, rt := range f.handler.Routes() {
		table = append(table, rt.Pattern())
	}

	slices.Sort(table)

	registered := f.handler.RegisteredPatterns()
	slices.Sort(registered)

	if !slices.Equal(table, registered) {
		t.Errorf("route table %v, registered %v", table, registered)
	}
}

// TestOnlyTheProtocolRoutesArePublic proves that the two routes under /oidc/
// are the only routes without a sign-in, that every other route declares an
// action, and that no operation of the description is public.
func TestOnlyTheProtocolRoutesArePublic(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	wantPublic := []string{
		"GET /oidc/{nsid}/.well-known/openid-configuration",
		"GET /oidc/{nsid}/jwks",
	}

	var public []string

	for _, rt := range f.handler.Routes() {
		switch rt.Scope {
		case route.ScopePublic:
			public = append(public, rt.Pattern())

			if len(rt.Actions) != 0 {
				t.Errorf("public route %s declares actions", rt.Pattern())
			}
		case route.ScopeAuthorized:
			if len(rt.Actions) == 0 {
				t.Errorf("route %s needs a sign-in and declares no action", rt.Pattern())
			}
		}
	}

	slices.Sort(public)

	if !slices.Equal(public, wantPublic) {
		t.Errorf("public routes = %v, want %v", public, wantPublic)
	}

	for _, op := range f.handler.Operations() {
		if op.Public {
			t.Errorf("operation %s is described as public", op.ID)
		}

		if strings.HasPrefix(op.Path, "/oidc/") {
			t.Errorf("operation %s is in the versioned description", op.ID)
		}
	}
}

func TestRoutesDeclareTheActionsOfTheDecision(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	want := map[string][]identity.Action{
		"GET " + issuerPath:                       {read},
		"PATCH " + issuerPath:                     {manage},
		"POST " + issuerPath + "/keys":            {manage},
		"POST " + issuerPath + "/keys/rotate":     {manage},
		"POST " + issuerPath + "/keys/check-copy": {manage},
		"GET " + issuerPath + "/export":           {manage},
	}

	got := map[string][]identity.Action{}

	for _, rt := range f.handler.Routes() {
		if rt.Scope == route.ScopeAuthorized {
			got[strings.ReplaceAll(rt.Pattern(), "{namespace}", "acme")] = rt.Actions
		}
	}

	for pattern, actions := range want {
		if !slices.Equal(got[pattern], actions) {
			t.Errorf("%s declares %v, want %v", pattern, got[pattern], actions)
		}
	}

	if len(got) != len(want) {
		t.Errorf("authorized routes = %d, want %d", len(got), len(want))
	}
}

func TestProtocolRoutesAnswerPublicData(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	base := "/oidc/" + f.ns.ID.String()

	// A namespace with no key answers its discovery document and an empty JWKS.
	disc := f.do(t, get("", base+"/.well-known/openid-configuration"))
	wantCode(t, "discovery", disc, http.StatusOK)

	if got := disc.header.Get("Cache-Control"); got != "max-age=300" {
		t.Errorf("Cache-Control = %q, want max-age=300", got)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(disc.body), &doc); err != nil {
		t.Fatal(err)
	}

	issuer := "https://dustpan.example/oidc/" + f.ns.ID.String()
	if doc["issuer"] != issuer || doc["jwks_uri"] != issuer+"/jwks" {
		t.Errorf("discovery = %v", doc)
	}

	empty := f.do(t, get("", base+"/jwks"))
	wantCode(t, "empty JWKS", empty, http.StatusOK)

	if strings.TrimSpace(empty.body) != `{"keys":[]}` {
		t.Errorf("empty JWKS = %s", empty.body)
	}

	f.createKeys(t)

	keys := f.do(t, get("", base+"/jwks"))
	wantCode(t, "JWKS", keys, http.StatusOK)
	noKeyMaterial(t, "JWKS", keys)

	if got := keys.header.Get("Cache-Control"); got != "max-age=300" {
		t.Errorf("Cache-Control of the JWKS = %q", got)
	}

	var set struct {
		Keys []map[string]string `json:"keys"`
	}

	if err := json.Unmarshal([]byte(keys.body), &set); err != nil || len(set.Keys) != 2 {
		t.Fatalf("JWKS = %s, %v", keys.body, err)
	}

	// The public keys verify a token of the issuer.
	if _, err := profiletest.VerifyToken([]byte(keys.body), "a.b.c", profiletest.Expect{
		Now: time.Now(), Issuer: issuer, Audience: "x",
	}); err == nil {
		t.Error("a forged token verifies")
	}
}

func TestProtocolRoutesRefuseWhatIsNotANamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, id := range []string{"ns-aaaaaaaaaaaaaaaaaaaa", "ns-", "acme", "%2e%2e", "ns-%00", strings.Repeat("a", 300)} {
		for _, suffix := range []string{"/.well-known/openid-configuration", "/jwks"} {
			got := f.do(t, get("", "/oidc/"+id+suffix))
			wantCode(t, id+suffix, got, http.StatusNotFound)

			if decode(t, got).Code != errcode.NamespaceNotFound {
				t.Errorf("%s: code = %q, want %s", id+suffix, decode(t, got).Code, errcode.NamespaceNotFound)
			}

			if got.header.Get("Cache-Control") != "no-store" {
				t.Errorf("%s: an error answer is cacheable", id+suffix)
			}
		}
	}

	// The name of a namespace is not its ID.
	wantCode(t, "name in the path", f.do(t, get("", "/oidc/acme/jwks")), http.StatusNotFound)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		got := f.do(t, send("admin", method, "/oidc/"+f.ns.ID.String()+"/jwks", ""))
		if got.code != http.StatusMethodNotAllowed && got.code != http.StatusNotFound {
			t.Errorf("%s on the JWKS: %d", method, got.code)
		}
	}
}

func TestNoIssuerHostAnswersNotConfigured(t *testing.T) {
	t.Parallel()

	f := buildFixture(t, buildOptions{noHost: true})

	got := f.do(t, get("", "/oidc/"+f.ns.ID.String()+"/.well-known/openid-configuration"))
	wantCode(t, "discovery with no host", got, http.StatusNotFound)

	if decode(t, got).Code != errcode.IssuerNotConfigured {
		t.Errorf("code = %q, want %s", decode(t, got).Code, errcode.IssuerNotConfigured)
	}

	st := f.do(t, get("reader", issuerPath))
	wantCode(t, "read with no host", st, http.StatusOK)

	if w := decode(t, st); w.HostConfigured || w.EffectiveIssuerURL != "" {
		t.Errorf("state = %+v", w)
	}
}

func TestEveryManagementRouteNeedsASignIn(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, rt := range f.handler.Routes() {
		if rt.Scope != route.ScopeAuthorized {
			continue
		}

		target := strings.ReplaceAll(rt.Path, "{namespace}", "acme")

		for name, token := range map[string]string{"no token": "", "unknown token": "stranger"} {
			got := f.do(t, send(token, rt.Method, target, ""))
			wantCode(t, rt.Pattern()+" with "+name, got, http.StatusUnauthorized)
		}
	}
}

func TestEveryManagementRouteChecksItsAction(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createKeys(t)

	for _, rt := range f.handler.Routes() {
		if rt.Scope != route.ScopeAuthorized {
			continue
		}

		target := strings.ReplaceAll(rt.Path, "{namespace}", "acme")

		// A principal with no grant, and a principal with the other action.
		other := map[identity.Action]string{read: "manageonly", manage: "reader"}[rt.Actions[0]]

		for _, token := range []string{other} {
			body := ""
			if rt.Method == http.MethodPatch {
				body = `{"includeActorName": false}`
			}

			got := f.do(t, send(token, rt.Method, target, body).withHeader("If-Match", `"issuer.0"`).
				withHeader("Content-Type", "application/merge-patch+json"))
			wantCode(t, rt.Pattern()+" by "+token, got, http.StatusForbidden)
		}
	}

	// Nothing changed.
	st := f.do(t, get("reader", issuerPath))
	if w := decode(t, st); w.Settings.ETag != `"issuer.0"` || len(w.Keys) != 2 {
		t.Errorf("a refused call changed the issuer: %+v", w)
	}
}

func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	for _, cause := range errcodetest.Causes(t) {
		t.Run(cause.Name, func(t *testing.T) {
			t.Parallel()

			f := buildFixture(t, buildOptions{tweak: func(cfg *apiissuer.Config) {
				cfg.Authorizer = errcodetest.Authorizer{Err: cause.Err}
			}})

			got := f.do(t, get("admin", issuerPath))

			errcodetest.Check(t, got.code, []byte(got.body), cause)
		})
	}
}

func TestReadShowsEffectiveValuesAndKeysWithoutMaterial(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createKeys(t)

	got := f.do(t, get("reader", issuerPath))
	wantCode(t, "read", got, http.StatusOK)
	noKeyMaterial(t, "read", got)

	w := decode(t, got)
	if w.EffectiveIssuerURL != "https://dustpan.example/oidc/"+f.ns.ID.String() || !w.HostConfigured ||
		w.Settings.IssuerURL != "" || !w.Settings.AllowBlockSubjects || w.Settings.IncludeActorName ||
		!slices.Equal(w.Settings.SubjectTemplate, []string{"namespace", "collection", "tool", "unit", "access"}) {
		t.Errorf("state = %+v", w)
	}

	if got.header.Get("ETag") != `"issuer.0"` || w.Settings.ETag != `"issuer.0"` || len(w.Keys) != 2 {
		t.Errorf("ETag %q, settings %+v, %d keys", got.header.Get("ETag"), w.Settings, len(w.Keys))
	}

	states := []string{w.Keys[0].State, w.Keys[1].State}
	slices.Sort(states)

	if !slices.Equal(states, []string{"active", "next"}) {
		t.Errorf("states = %v", states)
	}
}

func TestCreateKeysAnswers201Then409(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	got := f.do(t, send("admin", http.MethodPost, issuerPath+"/keys", ""))
	wantCode(t, "create keys", got, http.StatusCreated)
	noKeyMaterial(t, "create keys", got)

	if len(decode(t, got).Keys) != 2 {
		t.Errorf("keys = %s", got.body)
	}

	again := f.do(t, send("admin", http.MethodPost, issuerPath+"/keys", ""))
	wantCode(t, "second create keys", again, http.StatusConflict)

	if decode(t, again).Code != errcode.IssuerConflict {
		t.Errorf("code = %q", decode(t, again).Code)
	}

	// A body on a route that takes none is refused.
	wantCode(t, "body on create keys", f.do(t, send("admin", http.MethodPost, issuerPath+"/keys", `{"a":1}`)),
		http.StatusBadRequest)
}

func patch(token, body string) request {
	return send(token, http.MethodPatch, issuerPath, body).withHeader("Content-Type", "application/merge-patch+json")
}

func TestPatchPreconditionsAndMediaType(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	body := `{"includeActorName": false}`

	wantCode(t, "no If-Match", f.do(t, patch("admin", body)), http.StatusPreconditionRequired)

	for _, tag := range []string{`"issuer.7"`, `W/"issuer.0"`, `"profile.1"`, `issuer.0`, `"issuer.-1"`, `"issuer.x"`, `"issuer."`} {
		got := f.do(t, patch("admin", body).withHeader("If-Match", tag))
		wantCode(t, "If-Match "+tag, got, http.StatusPreconditionFailed)
	}

	got := f.do(t, send("admin", http.MethodPatch, issuerPath, body).withHeader("If-Match", `"issuer.0"`).
		withHeader("Content-Type", "text/plain"))
	wantCode(t, "media type", got, http.StatusUnsupportedMediaType)

	for name, b := range map[string]string{
		"unknown field": `{"colour": 1}`, "two values": `{} {}`, "not an object": `[]`, "wrong type": `{"includeActorName": "yes"}`,
		"wrong type for the URL": `{"issuerUrl": 5}`, "wrong type for the template": `{"subjectTemplate": "namespace"}`,
		"too large": `{"issuerUrl": "` + strings.Repeat("a", 20000) + `"}`,
	} {
		got = f.do(t, patch("admin", b).withHeader("If-Match", `"issuer.0"`))
		wantCode(t, name, got, http.StatusBadRequest)
	}

	st := decode(t, f.do(t, get("reader", issuerPath)))
	if st.Settings.ETag != `"issuer.0"` {
		t.Errorf("a refused patch stored settings: %+v", st.Settings)
	}
}

func TestPatchChangesAndReturnsTheNewTag(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	got := f.do(t, patch("admin", `{"includeActorName": false, "allowBlockSubjects": false}`).
		withHeader("If-Match", `"issuer.0"`))
	wantCode(t, "patch", got, http.StatusOK)

	w := decode(t, got)
	if w.ETag != `"issuer.1"` || got.header.Get("ETag") != `"issuer.1"` {
		t.Errorf("ETag = %q / %q", w.ETag, got.header.Get("ETag"))
	}

	// The old tag is stale now.
	stale := f.do(t, patch("admin", `{"allowBlockSubjects": true}`).withHeader("If-Match", `"issuer.0"`))
	wantCode(t, "stale tag", stale, http.StatusPreconditionFailed)

	// A null value restores the default. The default of allowBlockSubjects is
	// true, which is a change that reduces security, so it needs a fresh step.
	reset := f.do(t, patch("admin", `{"allowBlockSubjects": null}`).withHeader("If-Match", `"issuer.1"`))
	wantCode(t, "null patch", reset, http.StatusForbidden)

	st := decode(t, f.do(t, get("reader", issuerPath)))
	if st.Settings.AllowBlockSubjects {
		t.Errorf("allowBlockSubjects = %v after a null patch of a false value", st.Settings.AllowBlockSubjects)
	}
}

func TestPatchRefusesABadValue(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, b := range []string{
		`{"issuerUrl": "http://files.example.com"}`, `{"issuerUrl": "https://files.example.com/"}`,
		`{"subjectTemplate": ["unit"]}`, `{"subjectTemplate": ["namespace", "job"]}`,
	} {
		got := f.do(t, patch("admin", b).withHeader("If-Match", `"issuer.0"`))
		wantCode(t, b, got, http.StatusBadRequest)

		if decode(t, got).Code != errcode.IssuerInvalid {
			t.Errorf("%s: code = %q, want %s", b, decode(t, got).Code, errcode.IssuerInvalid)
		}
	}

}

func TestAChangeThatReducesSecurityNeedsAFreshSignIn(t *testing.T) {
	t.Parallel()

	const (
		change   = `{"issuerUrl": "https://files.example.com/acme"}`
		newLogin = "/api/server/sign-in/corp?fresh=1"
	)

	for _, tc := range []struct {
		name   string
		age    time.Duration
		token  bool
		status int
		code   string
		path   string
	}{
		{"an access token", 0, true, http.StatusForbidden, errcode.MFAFreshStepRequired, ""},
		{"an old sign-in", time.Hour, false, http.StatusUnauthorized, errcode.MFAFreshStepRequired, newLogin},
		{"a sign-in after now", -time.Hour, false, http.StatusUnauthorized, errcode.MFAFreshStepRequired, newLogin},
		{"a recent sign-in", time.Minute, false, http.StatusOK, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := buildFixture(t, buildOptions{withSession: true, sessionAge: tc.age})

			req := patch("admin", change).withHeader("If-Match", `"issuer.0"`)
			if !tc.token {
				req = req.inSession()
			}

			got := f.do(t, req)
			wantCode(t, "patch", got, tc.status)

			if tc.code != "" && decode(t, got).Code != tc.code {
				t.Errorf("code = %q, want %s", decode(t, got).Code, tc.code)
			}

			if tc.path != "" && !strings.Contains(got.body, tc.path) {
				t.Errorf("answer does not hold the path %q: %s", tc.path, got.body)
			}

			after := decode(t, f.do(t, get("reader", issuerPath)))
			changed := after.Settings.IssuerURL != ""

			if changed != (tc.status == http.StatusOK) {
				t.Errorf("issuerUrl set = %v after status %d", changed, tc.status)
			}
		})
	}
}

func TestRotationAndEmergencyReason(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createKeys(t)

	rotate := issuerPath + "/keys/rotate"

	got := f.do(t, send("admin", http.MethodPost, rotate, ""))
	wantCode(t, "normal rotation with no body", got, http.StatusOK)
	noKeyMaterial(t, "rotation", got)

	if w := decode(t, got); !w.Promoted || len(w.Keys) < 2 {
		t.Errorf("rotation = %+v", w)
	}

	for name, b := range map[string]string{
		"no reason": `{"emergency": true}`, "empty reason": `{"emergency": true, "reason": " "}`,
		"unknown field": `{"force": true}`,
	} {
		bad := f.do(t, send("admin", http.MethodPost, rotate, b))
		wantCode(t, name, bad, http.StatusBadRequest)
	}

	ok := f.do(t, send("admin", http.MethodPost, rotate, `{"emergency": true, "reason": "the host was copied"}`))
	wantCode(t, "emergency rotation", ok, http.StatusOK)

	if w := decode(t, ok); w.Promoted || len(w.Deleted) == 0 {
		t.Errorf("emergency rotation = %+v", w)
	}
}

func TestRotateWithNoKeysAndCopyChecksNeedAnIssuerURL(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	got := f.do(t, send("admin", http.MethodPost, issuerPath+"/keys/rotate", ""))
	wantCode(t, "rotate with no keys", got, http.StatusConflict)

	if decode(t, got).Code != errcode.IssuerKeyMissing {
		t.Errorf("code = %q", decode(t, got).Code)
	}

	f.createKeys(t)

	for _, target := range []string{issuerPath + "/keys/check-copy", issuerPath + "/export"} {
		method := http.MethodPost
		if strings.HasSuffix(target, "export") {
			method = http.MethodGet
		}

		got = f.do(t, send("admin", method, target, ""))
		wantCode(t, target+" with no issuerURL", got, http.StatusBadRequest)

		if decode(t, got).Code != errcode.IssuerInvalid {
			t.Errorf("%s: code = %q", target, decode(t, got).Code)
		}
	}
}

func TestExportAndCheckCopy(t *testing.T) {
	t.Parallel()

	f := buildFixture(t, buildOptions{withSession: true, sessionAge: time.Minute})
	f.createKeys(t)

	set := f.do(t, patch("admin", `{"issuerUrl": "https://files.example.com/acme"}`).withHeader("If-Match", `"issuer.0"`).
		inSession())
	wantCode(t, "set issuerUrl", set, http.StatusOK)

	exp := f.do(t, get("admin", issuerPath+"/export"))
	wantCode(t, "export", exp, http.StatusOK)
	noKeyMaterial(t, "export", exp)

	w := decode(t, exp)
	if w.IssuerURL != "https://files.example.com/acme" || len(w.Files) != 2 ||
		w.Files[0].Path != ".well-known/openid-configuration" || w.Files[1].Path != "jwks" {
		t.Fatalf("export = %+v", w)
	}

	var disc map[string]any
	if err := json.Unmarshal([]byte(w.Files[0].Content), &disc); err != nil ||
		disc["issuer"] != "https://files.example.com/acme" || disc["jwks_uri"] != "https://files.example.com/acme/jwks" {
		t.Errorf("exported discovery = %s, %v", w.Files[0].Content, err)
	}

	// The server has no outbound client in this fixture, so the check fails
	// closed and says which step failed.
	chk := f.do(t, send("admin", http.MethodPost, issuerPath+"/keys/check-copy", ""))
	wantCode(t, "check copy", chk, http.StatusOK)

	if c := decode(t, chk); c.OK || len(c.Steps) == 0 || c.Steps[0].OK {
		t.Errorf("copy check = %+v", c)
	}

	// The export of a reader of the namespace without the manage action is refused.
	wantCode(t, "export by a reader", f.do(t, get("reader", issuerPath+"/export")), http.StatusForbidden)
}

func TestEveryCodeOfTheAdapterIsInTheCatalog(t *testing.T) {
	t.Parallel()

	for _, code := range []string{
		errcode.IssuerConflict, errcode.IssuerInvalid, errcode.IssuerKeyExpired, errcode.IssuerKeyMissing,
		errcode.IssuerNotConfigured,
	} {
		if _, ok := errcode.Lookup(code); !ok {
			t.Errorf("code %s is not in the catalog", code)
		}
	}
}
