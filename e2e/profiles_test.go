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

// profileAgent is the User-Agent of the profile requests. The route coverage
// check counts requests that do not carry the harness agent.
const profileAgent = "opendustpan-e2e-profiles"

// profilesBase is the profile list of the e2e namespace.
const profilesBase = contextNamespace + "/profiles"

// profileSecret is the plain text of the secret that the scenario stores. No
// answer of a read without a reveal may hold it.
//
//nolint:gosec // A value for a test, not a credential.
const profileSecret = "e2e-profile-secret-9f3a"

// profileCall is one profile request of a scenario step.
type profileCall struct {
	check   func(string) error
	name    string
	method  string
	path    string
	body    string
	ifMatch string
	want    int
}

// profileRequest returns a request with no body, no check and no tag.
func profileRequest(name, method, path string, want int) profileCall {
	return profileCall{check: nil, name: name, method: method, path: path, body: "", ifMatch: "", want: want}
}

// withBody returns c with a body.
func (c profileCall) withBody(body string) profileCall {
	c.body = body

	return c
}

// withTag returns c with the If-Match tag.
func (c profileCall) withTag(tag string) profileCall {
	c.ifMatch = tag

	return c
}

// checking returns c with a check of the answer.
func (c profileCall) checking(check func(string) error) profileCall {
	c.check = check

	return c
}

// profileStep runs one profile request as principal A.
func profileStep(t *testing.T, env *e2e.Env, c profileCall) {
	t.Helper()

	var headers map[string]string
	if c.ifMatch != "" {
		headers = map[string]string{"If-Match": c.ifMatch}
	}

	env.APIStepWithHeaders(t.Context(), t, e2e.APIStepSpec{
		UserAgent: profileAgent,
		Name:      c.name,
		Method:    c.method,
		Path:      c.path,
		Body:      c.body,
		Want:      c.want,
		Check:     c.check,
		Scheme:    "token",
	}, headers)
}

// profileMasked checks that an answer holds every fragment and holds no
// plain text of the secret.
func profileMasked(fragments ...string) func(string) error {
	return func(body string) error {
		if strings.Contains(body, profileSecret) {
			return fmt.Errorf("%w: the answer holds the plain text of the secret: %s", errAnswerLacksFragment, body)
		}

		for _, f := range fragments {
			if !strings.Contains(body, f) {
				return fmt.Errorf("%w: %q is not in %s", errAnswerLacksFragment, f, body)
			}
		}

		return nil
	}
}

// profileRevealed checks that an answer holds the plain text of the secret.
func profileRevealed(body string) error {
	if !strings.Contains(body, `"value":"`+profileSecret+`"`) {
		return fmt.Errorf("%w: the reveal holds no value: %s", errAnswerLacksFragment, body)
	}

	return nil
}

// profileTag holds the ID and the ETag of the profile of a scenario, and
// updates them from each answer that carries them.
type profileTag struct {
	id   string
	etag string
}

// remember reads the ID and the ETag from the answer of a write, and checks
// that the answer holds no plain text.
func (p *profileTag) remember(body string) error {
	var got struct {
		ID   string `json:"id"`
		ETag string `json:"etag"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		return fmt.Errorf("decode %s: %w", body, err)
	}

	p.id, p.etag = got.ID, got.ETag

	return profileMasked(`"revision"`)(body)
}

// profileScenario drives the profile routes of the e2e namespace over the
// API as the platform engineer, principal A. It creates a profile with a
// secret entry, reads it back masked, edits an entry, saves a revision and
// deletes the profile. tool names the scenario, so the scenarios of the two
// tools use two names.
func profileScenario(t *testing.T, env *e2e.Env, tool string) {
	t.Helper()

	var tag profileTag

	doc := "schemaVersion: 1\nvalues:\n  region: eu-west-1\n  db:\n    password: { $secret: " + profileSecret + " }\n"

	create, err := json.Marshal(map[string]string{"name": "e2e-" + tool, "document": doc})
	if err != nil {
		t.Fatalf("encode profile body: %v", err)
	}

	profileStep(t, env, profileRequest("create a profile with a secret entry ("+tool+")",
		http.MethodPost, profilesBase, http.StatusCreated).withBody(string(create)).checking(tag.remember))

	if tag.id == "" {
		t.Fatal("the create answer holds no profile ID")
	}

	one := profilesBase + "/" + tag.id

	profileReads(t, env, one, tool)
	profileWrites(t, env, one, &tag)

	profileStep(t, env, profileRequest("a write with a stale tag is refused",
		http.MethodDelete, one+"/entries/region", http.StatusPreconditionFailed).withTag(`"`+tag.id+`.1"`))
	profileStep(t, env, profileRequest("a profile that does not exist is not found",
		http.MethodGet, profilesBase+"/absent", http.StatusNotFound))
	profileStep(t, env, profileRequest("delete the profile",
		http.MethodDelete, one, http.StatusNoContent).withTag(tag.etag))
	profileStep(t, env, profileRequest("the deleted profile is not found", http.MethodGet, one, http.StatusNotFound))
}

// profileReads reads the profile in each way, and checks that no read holds
// the plain text of the secret, except the reveal.
func profileReads(t *testing.T, env *e2e.Env, one, tool string) {
	t.Helper()

	profileStep(t, env, profileRequest("read the profile", http.MethodGet, one, http.StatusOK).
		checking(profileMasked(`"name":"e2e-`+tool+`"`)))
	profileStep(t, env, profileRequest("list the profiles", http.MethodGet, profilesBase, http.StatusOK).
		checking(profileMasked(`"e2e-`+tool+`"`)))
	profileStep(t, env, profileRequest("read the entries masked", http.MethodGet, one+"/entries", http.StatusOK).
		checking(profileMasked(`"path":"db.password","secret":true`, `"path":"region"`)))
	profileStep(t, env, profileRequest("read the secret entry masked",
		http.MethodGet, one+"/entries/db.password", http.StatusOK).checking(profileMasked(`"secret":true`)))
	profileStep(t, env, profileRequest("the stored revision holds the secret sealed",
		http.MethodGet, one+"/revisions/1", http.StatusOK).checking(profileMasked(`$sealed`)))
	profileStep(t, env, profileRequest("a reveal by an administrator answers the plain text",
		http.MethodGet, one+"/entries/db.password?showSecrets=true", http.StatusOK).checking(profileRevealed))
}

// profileWrites edits an entry, saves a revision and removes an entry, each
// on the tag of the step before.
func profileWrites(t *testing.T, env *e2e.Env, one string, tag *profileTag) {
	t.Helper()

	profileStep(t, env, profileRequest("set a plain entry",
		http.MethodPut, one+"/entries/tags.team", http.StatusOK).
		withBody(`{"value":"payments","description":"owner"}`).withTag(tag.etag).checking(tag.remember))
	profileStep(t, env, profileRequest("mark the secret entry never store",
		http.MethodPut, one+"/entries/db.password", http.StatusOK).
		withBody(`{"secret":"`+profileSecret+`","neverStore":true}`).withTag(tag.etag).checking(tag.remember))
	profileStep(t, env, profileRequest("save a revision",
		http.MethodPost, one+"/revisions", http.StatusCreated).
		withBody(`{"document":"schemaVersion: 1\nvalues:\n  region: eu-central-1\n","reason":"e2e"}`).
		withTag(tag.etag).checking(tag.remember))
	profileStep(
		t,
		env,
		profileRequest("the revisions list newest first", http.MethodGet, one+"/revisions", http.StatusOK).
			checking(profileMasked(`"revision":4`, `"reason":"e2e"`)),
	)
	profileStep(t, env, profileRequest("add a throwaway entry",
		http.MethodPut, one+"/entries/extra", http.StatusOK).
		withBody(`{"value":1}`).withTag(tag.etag).checking(tag.remember))
	profileStep(t, env, profileRequest("delete the entry",
		http.MethodDelete, one+"/entries/extra", http.StatusOK).withTag(tag.etag).checking(tag.remember))
}
