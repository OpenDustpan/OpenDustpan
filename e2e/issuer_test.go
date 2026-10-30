//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// issuerAgent is the User-Agent of the issuer requests. The route coverage
// check counts requests that do not carry the harness agent.
const issuerAgent = "opendustpan-e2e-issuer"

// issuerBase is the issuer of the e2e namespace.
const issuerBase = contextNamespace + "/issuer"

// issuerState holds what the scenario learns from the answers: the path of the
// protocol routes of the namespace, and the ETag of the settings.
type issuerState struct {
	protocol string
	etag     string
}

// issuerCall is one issuer request of the scenario.
type issuerCall struct {
	check  func(string) error
	name   string
	method string
	path   string
	body   string
	want   int
}

// issuerStep runs one issuer request with the token. An empty token sends the
// request of a caller with no sign-in.
func issuerStep(t *testing.T, env *e2e.Env, token string, c issuerCall) {
	t.Helper()

	env.APIStep(t.Context(), t, token, e2e.APIStepSpec{
		UserAgent: issuerAgent,
		Name:      c.name,
		Method:    c.method,
		Path:      c.path,
		Body:      c.body,
		Want:      c.want,
		Check:     c.check,
		Scheme:    "token",
	})
}

// mustHold checks that an answer holds every fragment and no key material.
func mustHold(fragments ...string) func(string) error {
	return func(body string) error {
		for _, banned := range []string{`"sealed"`, "PRIVATE KEY", `"d":`, `"dp"`, `"qi"`} {
			if strings.Contains(body, banned) {
				return fmt.Errorf("%w: the answer holds %q: %s", errAnswerLacksFragment, banned, body)
			}
		}

		for _, f := range fragments {
			if !strings.Contains(body, f) {
				return fmt.Errorf("%w: %q is not in %s", errAnswerLacksFragment, f, body)
			}
		}

		return nil
	}
}

// learnProtocol reads the path of the protocol routes from the effective
// issuer URL of the answer: the stable ID of the namespace is in it.
func (s *issuerState) learnProtocol(body string) error {
	var got struct {
		URL string `json:"effectiveIssuerURL"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		return fmt.Errorf("decode %s: %w", body, err)
	}

	_, path, ok := strings.Cut(strings.TrimPrefix(got.URL, "https://"), "/oidc/")
	if !ok || !strings.HasPrefix(path, "ns-") {
		return fmt.Errorf("%w: effectiveIssuerURL %q has no namespace ID", errAnswerLacksFragment, got.URL)
	}

	s.protocol = "/oidc/" + path

	return mustHold(`"issuerHostConfigured":true`, `"keys":[]`)(body)
}

// issuerScenario drives the issuer routes over the API as the platform
// engineer, principal A: the keys, the protocol routes, the settings, the
// rotation and the export. The protocol routes need no sign-in.
func issuerScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	var st issuerState

	token := env.TokenA()
	get, post := http.MethodGet, http.MethodPost

	issuerStep(t, env, token, issuerCall{
		name: "read the issuer of a namespace with no key", method: get, path: issuerBase, body: "",
		want: http.StatusOK, check: st.learnProtocol,
	})
	issuerStep(t, env, "", issuerCall{
		name:   "the discovery document needs no sign-in",
		method: get,
		path:   "/oidc/ns-absent/.well-known/openid-configuration",
		body:   "",
		want:   http.StatusNotFound,
		check:  mustHold(`namespace.not-found`),
	})
	issuerStep(t, env, "", issuerCall{
		name:   "the discovery document of the namespace",
		method: get,
		path:   st.protocol + "/.well-known/openid-configuration",
		body:   "",
		want:   http.StatusOK,
		check: mustHold(
			`"jwks_uri"`,
			`"id_token_signing_alg_values_supported":["RS256"]`,
			`"response_types_supported":["id_token"]`,
		),
	})
	issuerStep(t, env, "", issuerCall{
		name: "a namespace with no key has an empty JWKS", method: get, path: st.protocol + "/jwks", body: "",
		want: http.StatusOK, check: mustHold(`"keys":[]`),
	})
	issuerStep(t, env, token, issuerCall{
		name: "make the first keys", method: post, path: issuerBase + "/keys", body: "", want: http.StatusCreated,
		check: mustHold(`"state":"active"`, `"state":"next"`, `"algorithm":"RS256"`),
	})
	issuerStep(t, env, token, issuerCall{
		name: "a second make is a conflict", method: post, path: issuerBase + "/keys", body: "",
		want: http.StatusConflict, check: mustHold(`issuer.conflict`),
	})
	issuerStep(t, env, "", issuerCall{
		name: "the JWKS holds the two public keys", method: get, path: st.protocol + "/jwks", body: "",
		want: http.StatusOK, check: mustHold(`"kty":"RSA"`, `"alg":"RS256"`, `"use":"sig"`, `"kid":"oik-`),
	})

	issuerSettings(t, env, token, &st)
	issuerRotation(t, env, token)
}

// patchCall is one merge patch of the settings.
type patchCall struct {
	check func(string) error
	name  string
	body  string
	tag   string
	want  int
}

// issuerSettings changes the settings with a merge patch, and shows that a
// change that reduces security needs a fresh sign-in.
func issuerSettings(t *testing.T, env *e2e.Env, token string, st *issuerState) {
	t.Helper()

	remember := func(body string) error {
		var got struct {
			ETag string `json:"etag"`
		}

		if err := json.Unmarshal([]byte(body), &got); err != nil {
			return fmt.Errorf("decode %s: %w", body, err)
		}

		st.etag = got.ETag

		return nil
	}

	patchStep := func(c patchCall) {
		t.Helper()

		spec := e2e.APIStepSpec{
			UserAgent: issuerAgent,
			Name:      c.name,
			Method:    http.MethodPatch,
			Path:      issuerBase,
			Body:      c.body,
			Want:      c.want,
			Check:     c.check,
			Scheme:    "token",
		}
		env.APIStepWithHeaders(t.Context(), t, spec, map[string]string{
			"If-Match": c.tag, "Content-Type": "application/merge-patch+json",
		})
	}

	patchStep(patchCall{
		name: "a patch with no matching tag is refused", body: `{"allowBlockSubjects":false}`, tag: `"issuer.9"`,
		want: http.StatusPreconditionFailed, check: nil,
	})
	patchStep(patchCall{
		name: "a patch with a stronger setting needs no fresh sign-in", body: `{"allowBlockSubjects":false}`,
		tag: `"issuer.0"`, want: http.StatusOK, check: remember,
	})
	patchStep(patchCall{
		name: "a new issuerURL with an access token needs a fresh sign-in",
		body: `{"issuerURL":"https://files.example.com/acme"}`, tag: st.etag, want: http.StatusForbidden,
		check: mustHold(`mfa.fresh-step-required`),
	})

	issuerStep(t, env, token, issuerCall{
		name: "the export of a namespace with no issuerURL is refused", method: http.MethodGet,
		path: issuerBase + "/export", body: "", want: http.StatusBadRequest, check: mustHold(`issuer.invalid`),
	})
}

// issuerRotation rotates the keys.
func issuerRotation(t *testing.T, env *e2e.Env, token string) {
	t.Helper()

	rotate := issuerBase + "/keys/rotate"
	post := http.MethodPost

	issuerStep(t, env, token, issuerCall{
		name: "a normal rotation waits for the next key to age", method: post, path: rotate, body: "",
		want: http.StatusConflict, check: mustHold(`issuer.conflict`),
	})
	issuerStep(t, env, token, issuerCall{
		name: "an emergency rotation needs a reason", method: post, path: rotate, body: `{"emergency":true}`,
		want: http.StatusBadRequest, check: mustHold(`issuer.invalid`),
	})
	issuerStep(t, env, token, issuerCall{
		name: "an emergency rotation deletes every earlier key", method: post, path: rotate,
		body: `{"emergency":true,"reason":"e2e"}`, want: http.StatusOK, check: mustHold(`"deleted":["oik-`),
	})
	issuerStep(t, env, token, issuerCall{
		name: "a copy check needs an issuerURL", method: post, path: issuerBase + "/keys/check-copy", body: "",
		want: http.StatusBadRequest, check: mustHold(`issuer.invalid`),
	})
}

// TestIssuerAPI drives the issuer routes on a server of each store. The routes
// belong to no tool, so this test needs no CLI binary. The env with a sign-in
// has a public URL, which the issuer host needs.
func TestIssuerAPI(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			env := e2e.NewEnvWithSignIn(t.Context(), t, e2e.NoCLI(), store)

			issuerScenario(t, env)
		})
	}
}
