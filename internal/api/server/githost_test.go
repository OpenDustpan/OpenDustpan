package server_test

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/identity"
)

func hostAllowAll() hostAuthorizer { return hostAuthorizer{deny: nil, anyNamespace: false} }

// isFirstApp reports whether a is the registration that the first put makes.
func isFirstApp(a *appDoc) bool {
	return a.Kind == "github" && a.AppID == hostAppID && a.APIAddress == hostAPIAddress && a.Version == 1 &&
		a.HookID != "" && a.PrivateKey.Set && a.EventSecret.Set && a.PrivateKey.SetID != ""
}

func TestGitHostAppLifecycle(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())

	created := e.send(t, http.MethodPut, hostAppPath, hostAppBody())
	wantHost(t, created, http.StatusCreated)

	first := decodeHost[appDoc](t, created)
	if !isFirstApp(&first) {
		t.Fatalf("created = %+v", first)
	}

	if got := created.header.Get("ETag"); got != `"1"` {
		t.Fatalf("ETag = %q, want \"1\"", got)
	}

	read := e.send(t, http.MethodGet, hostAppPath, "")
	wantHost(t, read, http.StatusOK)

	if got := decodeHost[appDoc](t, read); got != first {
		t.Fatalf("read = %+v, want %+v", got, first)
	}

	replaceAndDeleteApp(t, e, &first)
}

func TestGitHostAppPutWithIfMatchNeedsARegistration(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())

	put := hostCall{
		headers: map[string]string{"If-Match": `"1"`}, method: http.MethodPut, target: hostAppPath, body: hostAppBody(),
	}

	wantHost(t, e.do(t, put), http.StatusNotFound)
	wantHost(t, e.send(t, http.MethodGet, hostAppPath, ""), http.StatusNotFound)
}

// replaceAndDeleteApp replaces the registration with the version that it read,
// proves that the old version is stale, and deletes it.
func replaceAndDeleteApp(t *testing.T, e *hostEnv, first *appDoc) {
	t.Helper()

	// A replacement keeps the hook ID, and the set IDs change.
	put := hostCall{
		headers: map[string]string{"If-Match": `"1"`}, method: http.MethodPut, target: hostAppPath, body: hostAppBody(),
	}

	replaced := e.do(t, put)
	wantHost(t, replaced, http.StatusOK)

	second := decodeHost[appDoc](t, replaced)
	if second.HookID != first.HookID || second.Version != 2 || second.PrivateKey.SetID == first.PrivateKey.SetID {
		t.Fatalf("replaced = %+v, want the hook ID %s at version 2 and a new set ID", second, first.HookID)
	}

	wantHost(t, e.do(t, put), http.StatusPreconditionFailed)
	wantHost(t, e.send(t, http.MethodDelete, hostAppPath, ""), http.StatusNoContent)
	wantHost(t, e.send(t, http.MethodGet, hostAppPath, ""), http.StatusNotFound)
}

func TestGitHostAppRefusesInputThatIsNotValid(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"an event secret of 31 bytes": `{"appID":"1","apiAddress":"` + hostAPIAddress + `","privateKey":"k","eventSecret":"` +
			strings.Repeat("a", 31) + `"}`,
		"no event secret": `{"appID":"1","apiAddress":"` + hostAPIAddress + `","privateKey":"k"}`,
		"no key":          `{"appID":"1","apiAddress":"` + hostAPIAddress + `","eventSecret":"` + strings.Repeat("a", 40) + `"}`,
		"no address":      `{"appID":"1","privateKey":"k","eventSecret":"` + strings.Repeat("a", 40) + `"}`,
		"an unknown field": `{"appID":"1","apiAddress":"` + hostAPIAddress + `","privateKey":"k","eventSecret":"` +
			strings.Repeat("a", 40) + `","hookID":"x"}`,
		"the http scheme": `{"appID":"1","apiAddress":"http://api.github.com","privateKey":"k","eventSecret":"` +
			strings.Repeat("a", 40) + `"}`,
		"a loopback address": `{"appID":"1","apiAddress":"https://127.0.0.1","privateKey":"k","eventSecret":"` +
			strings.Repeat("a", 40) + `"}`,
		"the metadata address": `{"appID":"1","apiAddress":"https://169.254.169.254","privateKey":"k","eventSecret":"` +
			strings.Repeat("a", 40) + `"}`,
		"a private address": `{"appID":"1","apiAddress":"https://10.0.0.5","privateKey":"k","eventSecret":"` +
			strings.Repeat("a", 40) + `"}`,
		"text after the object": hostAppBody() + `{}`,
		"not JSON":              `key=value`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newHostEnv(t, hostAllowAll())

			wantHost(t, e.send(t, http.MethodPut, hostAppPath, body), http.StatusBadRequest)
			wantHost(t, e.send(t, http.MethodGet, hostAppPath, ""), http.StatusNotFound)
		})
	}

	t.Run("a kind that has no adapter", func(t *testing.T) {
		t.Parallel()

		e := newHostEnv(t, hostAllowAll())

		wantHost(
			t,
			e.send(t, http.MethodPut, "/api/dustpan/v1/git-host-apps/gitlab", hostAppBody()),
			http.StatusBadRequest,
		)
	})

	t.Run("a body over the limit", func(t *testing.T) {
		t.Parallel()

		e := newHostEnv(t, hostAllowAll())
		big := `{"appID":"1","apiAddress":"` + hostAPIAddress + `","privateKey":"` + strings.Repeat("a", 70<<10) +
			`","eventSecret":"` + strings.Repeat("a", 40) + `"}`

		wantHost(t, e.send(t, http.MethodPut, hostAppPath, big), http.StatusRequestEntityTooLarge)
	})
}

func TestGitHostAppRefusesAKeyThatTheAdapterCannotUse(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.opener.setOpenErr(forge.ErrInvalid)

	wantHost(t, e.send(t, http.MethodPut, hostAppPath, hostAppBody()), http.StatusBadRequest)
	wantHost(t, e.send(t, http.MethodGet, hostAppPath, ""), http.StatusNotFound)
}

// TestGitHostAppInvalidKeyAnswerHoldsTheReasonOnly sends a refusal of the key
// that the adapter and the service wrapped, and checks that the answer holds
// the sentence of the refusal and the reason of the adapter, and no name of an
// internal step.
func TestGitHostAppInvalidKeyAnswerHoldsTheReasonOnly(t *testing.T) {
	t.Parallel()

	const reason = "the key is not a PEM block"

	e := newHostEnv(t, hostAllowAll())
	e.opener.setOpenErr(fmt.Errorf("the adapter read the key: %w",
		fmt.Errorf("the parse step: %w", fmt.Errorf("%w: %s", forge.ErrInvalid, reason))))

	r := e.send(t, http.MethodPut, hostAppPath, hostAppBody())
	wantHost(t, r, http.StatusBadRequest)

	if !strings.Contains(r.body, forge.ErrInvalid.Error()) {
		t.Fatalf("answer = %s, want the sentence of the refusal", r.body)
	}

	if !strings.Contains(r.body, reason) {
		t.Fatalf("answer = %s, want the reason %q", r.body, reason)
	}

	for _, step := range []string{"the adapter read the key", "the parse step", "the private key"} {
		if strings.Contains(r.body, step) {
			t.Fatalf("answer = %s, want no name of the step %q", r.body, step)
		}
	}
}

func TestGitHostAppNeverAnswersAnyOfItsSecrets(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)
	id := e.connect(t, "101")

	var texts []string

	for _, r := range []hostResponse{
		e.send(t, http.MethodGet, hostAppPath, ""),
		e.send(t, http.MethodGet, hostInstallationsPath, ""),
		e.send(t, http.MethodGet, hostConnectionsPath, ""),
		e.send(t, http.MethodGet, hostConnectionsPath+"/"+id, ""),
		e.send(t, http.MethodPost, hostConnectionsPath+"/"+id+"/check", ""),
		e.send(t, http.MethodPut, hostAppPath, hostAppBody()),
	} {
		texts = append(texts, r.body)
	}

	// A refusal that names the body must not echo it.
	bad := e.send(t, http.MethodPut, hostAppPath, strings.Replace(hostAppBody(), `"appID":"12345"`, `"appID":"x"`, 1))
	texts = append(texts, bad.body)

	for _, scope := range []string{"@server", "acme"} {
		for _, entry := range e.auditEntries(t, audit.ServerScope) {
			texts = append(texts, entry.Action+entry.Object+entry.ObjectID)

			for _, c := range entry.Changes {
				texts = append(texts, c.Key+c.New)
			}
		}

		_ = scope
	}

	for _, text := range texts {
		for _, canary := range []string{hostKeyCanary, hostSecretCanary} {
			if strings.Contains(text, canary) {
				t.Fatalf("a secret reached %q", text)
			}
		}
	}
}

func TestGitHostInstallationAssignments(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())

	created := e.send(t, http.MethodPut, hostInstallationsPath+"/100", `{"namespace":"acme"}`)
	wantHost(t, created, http.StatusCreated)

	got := decodeHost[installationDoc](t, created)
	if got.InstallationID != "100" || got.Namespace != "acme" || got.Version != 1 {
		t.Fatalf("created = %+v", got)
	}

	wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/9", `{"namespace":"beta"}`), http.StatusCreated)

	list := decodeHost[installationsDoc](t, e.send(t, http.MethodGet, hostInstallationsPath, ""))
	if len(list.Installations) != 2 || list.Installations[0].InstallationID != "9" ||
		list.Installations[1].InstallationID != "100" {
		t.Fatalf("list = %+v, want 9 then 100", list)
	}

	moved := e.send(t, http.MethodPut, hostInstallationsPath+"/100", `{"namespace":"beta"}`)
	wantHost(t, moved, http.StatusOK)

	if got := decodeHost[installationDoc](t, moved); got.Namespace != "beta" || got.Version != 2 {
		t.Fatalf("moved = %+v", got)
	}

	wantHost(t, e.send(t, http.MethodDelete, hostInstallationsPath+"/100", ""), http.StatusNoContent)
	wantHost(t, e.send(t, http.MethodGet, hostInstallationsPath+"/100", ""), http.StatusNotFound)
	wantHost(t, e.send(t, http.MethodDelete, hostInstallationsPath+"/100", ""), http.StatusNotFound)
}

func TestGitHostInstallationRefusesInputThatIsNotValid(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())

	for name, tc := range map[string]struct {
		target string
		body   string
		want   int
	}{
		"a namespace that does not exist": {hostInstallationsPath + "/1", `{"namespace":"nowhere"}`, http.StatusNotFound},
		"a namespace name that is bad":    {hostInstallationsPath + "/1", `{"namespace":"../x"}`, http.StatusBadRequest},
		"no namespace":                    {hostInstallationsPath + "/1", `{}`, http.StatusBadRequest},
		"an unknown field":                {hostInstallationsPath + "/1", `{"namespace":"acme","x":1}`, http.StatusBadRequest},
		"an installation ID that is bad":  {hostInstallationsPath + "/1%3BDROP", `{"namespace":"acme"}`, http.StatusBadRequest},
		"a leading zero":                  {hostInstallationsPath + "/01", `{"namespace":"acme"}`, http.StatusBadRequest},
	} {
		wantHost(t, e.send(t, http.MethodPut, tc.target, tc.body), tc.want)

		_ = name
	}

	list := decodeHost[installationsDoc](t, e.send(t, http.MethodGet, hostInstallationsPath, ""))
	if len(list.Installations) != 0 {
		t.Fatalf("list = %+v, want no assignment after refused requests", list)
	}
}

func TestGitHostRegistrationInUseCannotBeDeleted(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	wantHost(t, e.send(t, http.MethodDelete, hostAppPath, ""), http.StatusConflict)
	wantHost(t, e.send(t, http.MethodDelete, hostInstallationsPath+"/101", ""), http.StatusNoContent)
	wantHost(t, e.send(t, http.MethodDelete, hostAppPath, ""), http.StatusNoContent)
}

// hasRegistrationValues reports whether c shows the values of the registration.
func (c *connectionDoc) hasRegistrationValues() bool {
	return c.AppID == hostAppID && c.APIAddress == hostAPIAddress && c.HookID != ""
}

// isFirstConnection reports whether c is the connection that the first create
// makes for installation 101.
func isFirstConnection(c *connectionDoc) bool {
	return c.Default && c.Kind == "github" && c.Auth == "app" && c.AppScope == "installation" &&
		c.InstallationID == "101" && c.Version == 1 && c.Push && c.Namespace == "acme" && c.CredentialSets != nil &&
		len(c.CredentialSets) == 0 && c.hasRegistrationValues()
}

func TestGitHostConnectionLifecycle(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	created := e.send(t, http.MethodPost, hostConnectionsPath, createBody("101"))
	wantHost(t, created, http.StatusCreated)

	conn := decodeHost[connectionDoc](t, created)
	if !isFirstConnection(&conn) {
		t.Fatalf("created = %+v", conn)
	}

	if got := created.header.Get("Location"); got != hostConnectionsPath+"/"+conn.ID {
		t.Fatalf("Location = %q", got)
	}

	read := e.send(t, http.MethodGet, hostConnectionsPath+"/"+conn.ID, "")
	wantHost(t, read, http.StatusOK)

	if got := decodeHost[connectionDoc](t, read); got.ID != conn.ID || got.Version != 1 {
		t.Fatalf("read = %+v", got)
	}

	list := decodeHost[connectionsDoc](t, e.send(t, http.MethodGet, hostConnectionsPath, ""))
	if len(list.Connections) != 1 || list.Connections[0].ID != conn.ID {
		t.Fatalf("list = %+v", list)
	}

	wantHost(t, e.send(t, http.MethodDelete, hostConnectionsPath+"/"+conn.ID, ""), http.StatusNoContent)
	wantHost(t, e.send(t, http.MethodGet, hostConnectionsPath+"/"+conn.ID, ""), http.StatusNotFound)
}

func TestGitHostConnectionDefaults(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)
	wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/102", `{"namespace":"acme"}`), http.StatusCreated)

	first := e.connect(t, "101")
	second := e.connect(t, "102")

	// The default connection stays while another one exists.
	wantHost(t, e.send(t, http.MethodDelete, hostConnectionsPath+"/"+first, ""), http.StatusConflict)

	moved := e.send(t, http.MethodPost, hostConnectionsPath+"/"+second+"/make-default", "")
	wantHost(t, moved, http.StatusOK)

	if got := decodeHost[connectionDoc](t, moved); !got.Default || got.Version != 2 {
		t.Fatalf("make-default = %+v", got)
	}

	wantHost(t, e.send(t, http.MethodDelete, hostConnectionsPath+"/"+first, ""), http.StatusNoContent)
	wantHost(t, e.send(t, http.MethodDelete, hostConnectionsPath+"/"+second, ""), http.StatusNoContent)
}

func TestGitHostConnectionConflicts(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())

	// No registration: a conflict that the operator can fix.
	r := e.send(t, http.MethodPost, hostConnectionsPath, createBody("101"))
	wantHost(t, r, http.StatusConflict)

	if !strings.Contains(r.body, "git-host.app-not-registered") {
		t.Fatalf("answer = %s, want the code of the missing registration", r.body)
	}

	wantHost(t, e.send(t, http.MethodPut, hostAppPath, hostAppBody()), http.StatusCreated)

	// An installation that no one assigned: a conflict that the operator can fix,
	// with the same status as a check of a connection that lost its assignment.
	r = e.send(t, http.MethodPost, hostConnectionsPath, createBody("101"))
	wantHost(t, r, http.StatusConflict)

	if !strings.Contains(r.body, "git-host.installation-not-assigned") {
		t.Fatalf("answer = %s, want the code of the missing assignment", r.body)
	}

	// An installation of another namespace.
	wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/101", `{"namespace":"beta"}`), http.StatusCreated)
	wantHost(t, e.send(t, http.MethodPost, hostConnectionsPath, createBody("101")), http.StatusConflict)

	// A second connection to one installation.
	wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/101", `{"namespace":"acme"}`), http.StatusOK)
	e.connect(t, "101")
	wantHost(t, e.send(t, http.MethodPost, hostConnectionsPath, createBody("101")), http.StatusConflict)
}

// TestGitHostIfMatchInvalidCodes proves that a bad If-Match header on each
// git-host route that reads one answers the If-Match code and its message.
// The subtests share one environment and only read refusals from it.
func TestGitHostIfMatchInvalidCodes(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	created := e.send(t, http.MethodPost, hostConnectionsPath, createBody("101"))
	wantHost(t, created, http.StatusCreated)

	conn := decodeHost[connectionDoc](t, created)

	tests := []struct {
		name string
		call hostCall
	}{
		{
			name: "app PUT",
			call: hostCall{
				headers: map[string]string{"If-Match": "abc"},
				method:  http.MethodPut, target: hostAppPath, body: hostAppBody(),
			},
		},
		{
			name: "app DELETE",
			call: hostCall{
				headers: map[string]string{"If-Match": "abc"},
				method:  http.MethodDelete, target: hostAppPath, body: "",
			},
		},
		{
			name: "installation assign",
			call: hostCall{
				headers: map[string]string{"If-Match": "abc"},
				method:  http.MethodPut, target: hostInstallationsPath + "/101", body: `{"namespace":"acme"}`,
			},
		},
		{
			name: "installation unassign",
			call: hostCall{
				headers: map[string]string{"If-Match": "abc"},
				method:  http.MethodDelete, target: hostInstallationsPath + "/101", body: "",
			},
		},
		{
			name: "connection DELETE",
			call: hostCall{
				headers: map[string]string{"If-Match": "abc"},
				method:  http.MethodDelete, target: hostConnectionsPath + "/" + conn.ID, body: "",
			},
		},
		{
			name: "connection make-default",
			call: hostCall{
				headers: map[string]string{"If-Match": `"0"`},
				method:  http.MethodPost, target: hostConnectionsPath + "/" + conn.ID + "/make-default", body: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := e.do(t, tt.call)

			wantHost(t, r, http.StatusBadRequest)
			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, []byte(r.body),
				errcodetest.Want{Code: errcode.RequestIfMatchInvalid, Args: nil, RequestBody: nil})
			wantMessage(t, []byte(r.body), ifMatchInvalidMessage)
		})
	}
}

// TestGitHostConnectionTakesNothingThatTheOperatorOwns sends each field that
// only the operator sets, straight to the create route, with a value and with
// null. A request that holds none of them makes a connection.
func TestGitHostConnectionTakesNothingThatTheOperatorOwns(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	//nolint:gosec // The strings are fields of a request that the route refuses, not credentials.
	owns := map[string]string{
		"an API address":          `"apiAddress":"https://evil.example.com"`,
		"an API address of null":  `"apiAddress":null`,
		"an app ID":               `"appID":"777"`,
		"an app ID of null":       `"appID":null`,
		"a private key":           `"privateKey":"` + hostKeyCanary + `"`,
		"a private key of null":   `"privateKey":null`,
		"an event secret":         `"eventSecret":"` + hostSecretCanary + `"`,
		"an event secret of null": `"eventSecret":null`,
		"a credential":            `"credential":"token"`,
		"a credential of null":    `"credential":null`,
		"a list of sets":          `"credentials":[]`,
		"a list of sets of null":  `"credentials":null`,
	}

	for name, field := range owns {
		body := `{"kind":"github","auth":"app","installationID":"101",` + field + `}`

		r := e.send(t, http.MethodPost, hostConnectionsPath, body)
		wantHost(t, r, http.StatusBadRequest)

		if !strings.Contains(r.body, "the operator sets them") {
			t.Fatalf("the refusal of %s = %s, want the sentence of an operator-owned field", name, r.body)
		}

		if strings.Contains(r.body, hostKeyCanary) || strings.Contains(r.body, hostSecretCanary) {
			t.Fatalf("the refusal of %s echoes the secret: %s", name, r.body)
		}
	}

	// A field that the operator does not own, and that no route takes, fails
	// with the sentence of the body.
	unknown := e.send(t, http.MethodPost, hostConnectionsPath,
		`{"kind":"github","auth":"app","installationID":"101","other":"x"}`)
	wantHost(t, unknown, http.StatusBadRequest)

	list := decodeHost[connectionsDoc](t, e.send(t, http.MethodGet, hostConnectionsPath, ""))
	if len(list.Connections) != 0 {
		t.Fatalf("list = %+v, want no connection after refused creates", list)
	}

	// The same request without a field of the operator makes a connection.
	wantHost(t, e.send(t, http.MethodPost, hostConnectionsPath, createBody("101")), http.StatusCreated)
}

func TestGitHostConnectionRefusesInputThatIsNotValid(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	for name, body := range map[string]string{ //nolint:gosec // The strings are bodies of requests, not credentials.
		"the token auth kind": `{"kind":"github","auth":"token","installationID":"101"}`,
		"another kind":        `{"kind":"gitlab","auth":"app","installationID":"101"}`,
		"no installation":     `{"kind":"github","auth":"app"}`,
		"a bad installation":  `{"kind":"github","auth":"app","installationID":"1 OR 1=1"}`,
		"no kind":             `{"auth":"app","installationID":"101"}`,
		"not JSON":            `x`,
		"an empty body":       ``,
	} {
		wantHost(t, e.send(t, http.MethodPost, hostConnectionsPath, body), http.StatusBadRequest)

		_ = name
	}
}

func TestGitHostConnectionOfAnotherNamespaceIsNotFound(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	id := e.connect(t, "101")
	other := "/api/dustpan/v1/namespaces/beta/git-host-connections/" + id

	// The test authorizer allows the namespace actions on acme only: beta is
	// refused before the connection is looked up.
	wantHost(t, e.send(t, http.MethodGet, other, ""), http.StatusForbidden)
}

func TestGitHostCheckAnswersTheAccountAndTheCapabilities(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	id := e.connect(t, "101")

	r := e.send(t, http.MethodPost, hostConnectionsPath+"/"+id+"/check", "")
	wantHost(t, r, http.StatusOK)

	doc := decodeHost[checkDoc](t, r)
	if doc.Account.ID != "99" || doc.Account.Login != "acme-org" || doc.Capabilities == nil ||
		len(doc.Account.Scopes) != 1 {
		t.Fatalf("check = %+v", doc)
	}

	for _, consumer := range forge.Consumers() {
		missing, ok := doc.Missing[string(consumer)]
		if !ok || missing == nil {
			t.Fatalf("missing = %v, want a list for %s", doc.Missing, consumer)
		}
	}

	if got := doc.Missing["self-service"]; len(got) != 1 || got[0] != "files" {
		t.Fatalf("self-service misses %v, want files", got)
	}
}

func TestGitHostCheckAnswersWhatTheGitHostSaid(t *testing.T) {
	t.Parallel()

	budget := fmt.Errorf("%w: %w", forge.ErrAppBudget, forge.Unavailable(30*time.Second, "budget"))

	for _, tc := range []struct {
		err        error
		name       string
		text       string
		retryAfter string
		status     int
		code       string
	}{
		{
			err: forge.ErrAuth, name: "a refused credential", text: "GitHub refused the credential of the connection",
			retryAfter: "", status: http.StatusUnprocessableEntity, code: "",
		},
		{
			err: forge.Unavailable(30*time.Second, "rate limit"), name: "no answer with a wait",
			text: "GitHub did not answer: try again later", retryAfter: "30", status: http.StatusServiceUnavailable,
			code: "",
		},
		{
			err: forge.ErrUnavailable, name: "no answer", text: "GitHub did not answer: try again later",
			retryAfter: "", status: http.StatusServiceUnavailable, code: "",
		},
		{
			err: budget, name: "a spent budget of the app",
			text:       "the GitHub App used its budget of signed calls: try again in 30 seconds",
			retryAfter: "30", status: http.StatusServiceUnavailable, code: errcode.GitHostAppBudgetSpent,
		},
		{
			err: forge.ErrAppBudget, name: "a spent budget with no wait",
			text:       "the GitHub App used its budget of signed calls: try again later",
			retryAfter: "", status: http.StatusServiceUnavailable, code: errcode.GitHostAppBudgetSpent,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newHostEnv(t, hostAllowAll())
			e.register(t)

			id := e.connect(t, "101")
			e.opener.setCheckErr(tc.err)

			r := e.send(t, http.MethodPost, hostConnectionsPath+"/"+id+"/check", "")
			wantHost(t, r, tc.status)

			if got := r.header.Get("Retry-After"); got != tc.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tc.retryAfter)
			}

			if !strings.Contains(r.body, `"`+tc.text+`"`) {
				t.Errorf("answer = %s, want the message %q", r.body, tc.text)
			}

			if tc.code != "" {
				errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, []byte(r.body),
					errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
			}
		})
	}
}

func TestGitHostCheckFailsClosedAfterTheOperatorMovesTheInstallation(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	id := e.connect(t, "101")
	target := hostConnectionsPath + "/" + id + "/check"

	wantHost(t, e.send(t, http.MethodPost, target, ""), http.StatusOK)
	wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/101", `{"namespace":"beta"}`), http.StatusOK)

	r := e.send(t, http.MethodPost, target, "")
	wantHost(t, r, http.StatusConflict)

	var denied int

	for _, entry := range e.auditEntries(t, "acme") {
		if entry.Outcome == audit.OutcomeDenied && entry.Action == "git-host-connection:open" {
			denied++
		}
	}

	if denied != 1 {
		t.Fatalf("%d denied open entries, want 1", denied)
	}
}

func TestGitHostCheckTakesNoIdempotencyKey(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	id := e.connect(t, "101")

	r := e.do(t, hostCall{
		headers: map[string]string{"Idempotency-Key": "k-1"}, method: http.MethodPost,
		target: hostConnectionsPath + "/" + id + "/check", body: "",
	})
	wantHost(t, r, http.StatusBadRequest)

	if !strings.Contains(r.body, "request.idempotency-key-unsupported") {
		t.Fatalf("answer = %s, want the code of an unsupported key", r.body)
	}
}

func TestGitHostWritesWriteOneAuditEntryWithNoSecret(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	id := e.connect(t, "101")
	wantHost(t, e.send(t, http.MethodPost, hostConnectionsPath+"/"+id+"/check", ""), http.StatusOK)
	wantHost(t, e.send(t, http.MethodDelete, hostConnectionsPath+"/"+id, ""), http.StatusNoContent)

	entries := e.auditEntries(t, "acme")
	actions := make([]string, 0, len(entries))

	for _, entry := range entries {
		actions = append(actions, entry.Action)

		if entry.Subject != "tester" || entry.ObjectID != id {
			t.Fatalf("entry = %+v, want the subject and the connection ID", entry)
		}
	}

	want := "git-host-connection:create,git-host-connection:check,git-host-connection:delete"
	if got := strings.Join(actions, ","); got != want {
		t.Fatalf("actions = %s, want %s", got, want)
	}

	// The console session of the writes also makes entries: its person and its start.
	serverEntries := withoutSessionEntries(e.auditEntries(t, audit.ServerScope))
	server := make([]string, 0, len(serverEntries))

	for _, entry := range serverEntries {
		server = append(server, entry.Action)
	}

	if got := strings.Join(server, ","); got != "git-host-app:put,git-host-installation:assign" {
		t.Fatalf("server actions = %s", got)
	}
}

func TestGitHostRoutesEnforceTheirActions(t *testing.T) {
	t.Parallel()

	probe := newHostEnv(t, hostAllowAll())

	var rows int

	for _, route := range probe.handler.Routes() {
		if !strings.Contains(route.Path, "git-host-") {
			continue
		}

		rows++

		if route.Scope != 2 || len(route.Actions) == 0 || route.Resolve == nil {
			t.Fatalf("route %s %s has no action or resolver", route.Method, route.Path)
		}

		deny := map[identity.Action]bool{}
		for _, a := range route.Actions {
			deny[a] = true
		}

		denied := newHostEnv(t, hostAuthorizer{deny: deny, anyNamespace: false})
		target := strings.NewReplacer(
			"{kind}", "github", "{installation_id}", "1", "{namespace}", "acme", "{id}", forge.NewConnectionID(),
		).Replace(route.Path)

		r := denied.send(t, route.Method, target, "{}")
		if r.status != http.StatusForbidden {
			t.Errorf("%s %s with its actions denied = %d, want 403", route.Method, route.Path, r.status)
		}
	}

	const want = 13

	if rows != want {
		t.Fatalf("%d Git host routes, want %d: a new route needs its row in this test", rows, want)
	}
}

func TestGitHostRoutesNeedTheirServiceToBeMounted(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t)

	for _, route := range h.Routes() {
		if strings.Contains(route.Path, "git-host-") {
			t.Fatalf("route %s %s is mounted with no Git host service", route.Method, route.Path)
		}
	}
}

// TestGitHostCheckIsRateLimited sends a second check of one connection at once
// and expects 429 with a Retry-After that is not above 60 seconds. The limit
// is the one a server has when it sets none.
func TestGitHostCheckIsRateLimited(t *testing.T) {
	t.Parallel()

	limits := forge.DefaultCheckLimits()
	e := newHostEnvLimited(t, hostAllowAll(), withKeys, &limits)
	e.register(t)

	id := e.connect(t, "101")
	target := hostConnectionsPath + "/" + id + "/check"

	wantHost(t, e.send(t, http.MethodPost, target, ""), http.StatusOK)

	limited := e.send(t, http.MethodPost, target, "")
	wantHost(t, limited, http.StatusTooManyRequests)

	seconds, err := strconv.Atoi(limited.header.Get("Retry-After"))
	if err != nil || seconds < 1 || seconds > 60 {
		t.Fatalf("Retry-After = %q, want 1 to 60 seconds", limited.header.Get("Retry-After"))
	}
}

// TestGitHostRetryAfterIsCapped sends a Git host that asks for a wait of one
// hour and expects the cap.
func TestGitHostRetryAfterIsCapped(t *testing.T) {
	t.Parallel()

	e := newHostEnv(t, hostAllowAll())
	e.register(t)

	id := e.connect(t, "101")
	e.opener.setCheckErr(forge.Unavailable(time.Hour, "rate limit"))

	limited := e.send(t, http.MethodPost, hostConnectionsPath+"/"+id+"/check", "")
	wantHost(t, limited, http.StatusServiceUnavailable)

	if got := limited.header.Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want the cap 60", got)
	}
}
