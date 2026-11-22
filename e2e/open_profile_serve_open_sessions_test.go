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

// sessionsBase is the session collection of the e2e namespace.
const sessionsBase = contextNamespace + "/profile-sessions"

// profileOpenFlow opens one with the API consumer, replays the key (409 with
// the session ID and no value), reads the session, reads one path, and reads
// as another principal (404). It runs on the profile that profileScenario
// created, before the writes replace its values.
func profileOpenFlow(t *testing.T, env *e2e.Env, one, tool string) {
	t.Helper()

	key := "e2e-open-" + tool
	openBody, err := json.Marshal(map[string]string{"consumer": "api"})
	if err != nil {
		t.Fatalf("encode open body: %v", err)
	}

	var session string

	profileStep(t, env, profileRequest("open the profile ("+tool+")",
		http.MethodPost, one+"/open", http.StatusCreated).withBody(string(openBody)).withKey(key).
		checking(func(answer string) error {
			var opened struct {
				SessionID string `json:"sessionId"` //nolint:tagliatelle // The apidesc camelCase rule spells an ID as Id; sessionId is the wire name.
			}

			if err := json.Unmarshal([]byte(answer), &opened); err != nil {
				return fmt.Errorf("decode %s: %w", answer, err)
			}

			if opened.SessionID == "" {
				return fmt.Errorf("%w: the open holds no session: %s", errAnswerLacksFragment, answer)
			}

			session = opened.SessionID

			return profileMasked(`"expiresAt"`)(answer)
		}))

	if session == "" {
		t.Fatal("the open holds no session")
	}

	profileStep(t, env, profileRequest("the same open with the same key names the session ("+tool+")",
		http.MethodPost, one+"/open", http.StatusConflict).withBody(string(openBody)).withKey(key).
		checking(profileMasked(session, `request.idempotency-key-replayed-secret`)))

	profileStep(t, env, profileRequest("read the session ("+tool+")",
		http.MethodGet, sessionsBase+"/"+session, http.StatusOK).
		checking(func(answer string) error {
			for _, fragment := range []string{
				`"region"`, `"value":"eu-west-1"`, `"db.password"`, `"value":"` + profileSecret + `"`,
				`"secret":true`,
			} {
				if !strings.Contains(answer, fragment) {
					return fmt.Errorf("%w: %q is not in %s", errAnswerLacksFragment, fragment, answer)
				}
			}

			return nil
		}))
	profileStep(t, env, profileRequest("read one path of the session ("+tool+")",
		http.MethodGet, sessionsBase+"/"+session+"?path=region", http.StatusOK).
		checking(func(answer string) error {
			if !strings.Contains(answer, `"value":"eu-west-1"`) {
				return fmt.Errorf("%w: the path read holds no value: %s", errAnswerLacksFragment, answer)
			}

			if strings.Contains(answer, "db.password") {
				return fmt.Errorf("%w: the path read holds another path: %s", errAnswerLacksFragment, answer)
			}

			return nil
		}))

	env.APIStepB(t.Context(), t, e2e.APIStepSpec{
		UserAgent: profileAgent,
		Name:      "another principal does not read the session (" + tool + ")",
		Method:    http.MethodGet,
		Path:      sessionsBase + "/" + session,
		Body:      "",
		Want:      http.StatusNotFound,
		Check:     profileMasked(`profile.not-found`),
		Scheme:    "token",
	})
}
