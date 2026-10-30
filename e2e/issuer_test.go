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

// issuerStep runs one issuer request as principal A.
func issuerStep(
	t *testing.T, env *e2e.Env, token, name, method, path, body string, want int, check func(string) error,
) {
	t.Helper()

	env.APIStep(t.Context(), t, token, e2e.APIStepSpec{
		UserAgent: issuerAgent, Name: name, Method: method, Path: path, Body: body, Want: want, Check: check,
		Scheme: "token",
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
		URL string `json:"effectiveIssuerUrl"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		return fmt.Errorf("decode %s: %w", body, err)
	}

	_, path, ok := strings.Cut(strings.TrimPrefix(got.URL, "https://"), "/oidc/")
	if !ok || !strings.HasPrefix(path, "ns-") {
		return fmt.Errorf("%w: effectiveIssuerUrl %q has no namespace ID", errAnswerLacksFragment, got.URL)
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

	issuerStep(t, env, token, "read the issuer of a namespace with no key", http.MethodGet, issuerBase, "", http.StatusOK,
		st.learnProtocol)
	issuerStep(t, env, "", "the discovery document needs no sign-in", http.MethodGet,
		"/oidc/ns-absent/.well-known/openid-configuration", "", http.StatusNotFound, mustHold(`namespace.not-found`))
	issuerStep(t, env, "", "the discovery document of the namespace", http.MethodGet,
		st.protocol+"/.well-known/openid-configuration", "", http.StatusOK,
		mustHold(`"jwks_uri"`, `"id_token_signing_alg_values_supported":["RS256"]`, `"response_types_supported":["id_token"]`))
	issuerStep(t, env, "", "a namespace with no key has an empty JWKS", http.MethodGet, st.protocol+"/jwks", "",
		http.StatusOK, mustHold(`"keys":[]`))
	issuerStep(t, env, token, "make the first keys", http.MethodPost, issuerBase+"/keys", "", http.StatusCreated,
		mustHold(`"state":"active"`, `"state":"next"`, `"algorithm":"RS256"`))
	issuerStep(t, env, token, "a second make is a conflict", http.MethodPost, issuerBase+"/keys", "", http.StatusConflict,
		mustHold(`issuer.conflict`))
	issuerStep(t, env, "", "the JWKS holds the two public keys", http.MethodGet, st.protocol+"/jwks", "",
		http.StatusOK, mustHold(`"kty":"RSA"`, `"alg":"RS256"`, `"use":"sig"`, `"kid":"oik-`))

	issuerSettings(t, env, token, &st)
	issuerRotation(t, env, token)
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

	patchStep := func(name, body, tag string, want int, check func(string) error) {
		t.Helper()

		spec := e2e.APIStepSpec{
			UserAgent: issuerAgent, Name: name, Method: http.MethodPatch, Path: issuerBase, Body: body, Want: want,
			Check: check, Scheme: "token",
		}
		env.APIStepWithHeaders(t.Context(), t, spec, map[string]string{
			"If-Match": tag, "Content-Type": "application/merge-patch+json",
		})
	}

	patchStep("a patch with no matching tag is refused", `{"allowBlockSubjects":false}`, `"issuer.9"`,
		http.StatusPreconditionFailed, nil)
	patchStep("a patch with a stronger setting needs no fresh sign-in", `{"allowBlockSubjects":false}`, `"issuer.0"`,
		http.StatusOK, remember)
	patchStep("a new issuerUrl with an access token needs a fresh sign-in", `{"issuerUrl":"https://files.example.com/acme"}`,
		st.etag, http.StatusForbidden, mustHold(`mfa.fresh-step-required`))

	issuerStep(t, env, token, "the export of a namespace with no issuerUrl is refused", http.MethodGet, issuerBase+"/export",
		"", http.StatusBadRequest, mustHold(`issuer.invalid`))
}

// issuerRotation rotates the keys.
func issuerRotation(t *testing.T, env *e2e.Env, token string) {
	t.Helper()

	issuerStep(t, env, token, "a normal rotation makes the next key active", http.MethodPost, issuerBase+"/keys/rotate", "",
		http.StatusOK, mustHold(`"promoted":true`))
	issuerStep(t, env, token, "an emergency rotation needs a reason", http.MethodPost, issuerBase+"/keys/rotate",
		`{"emergency":true}`, http.StatusBadRequest, mustHold(`issuer.invalid`))
	issuerStep(t, env, token, "an emergency rotation deletes every earlier key", http.MethodPost, issuerBase+"/keys/rotate",
		`{"emergency":true,"reason":"e2e"}`, http.StatusOK, mustHold(`"deleted":["oik-`))
	issuerStep(t, env, token, "a copy check needs an issuerUrl", http.MethodPost, issuerBase+"/keys/check-copy", "",
		http.StatusBadRequest, mustHold(`issuer.invalid`))
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
