//go:build e2e

package e2e_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/e2e/githubfake"
	"opendustpan.org/opendustpan/pkg/audit"
)

const (
	e2eRotatedSecret = "e2e-event-secret-rotated-0123456789abcdef01"
	e2eWrongSecret   = "e2e-event-secret-wrong-0123456789abcdef0123"
)

// TestGitHostWebhookIntake drives the intake of the webhook with the real
// server, on each store, and the fake GitHub as the sender. The steps are the
// steps of the operator: register the app and read the hook ID, assign an
// installation, send a ping and a pull request event, send the pull request
// event again, send a delivery with a wrong signature and a delivery to an
// unknown hook, rotate the event secret, send with the old secret, confirm the
// rotation, and send with the old secret again. Every delivery gets the status
// 204 and no body, so the steps read the reason in the counter and in the
// audit log, as the operator does.
func TestGitHostWebhookIntake(t *testing.T) {
	t.Parallel()

	if e2e.UseBinaryServer() {
		t.Skip("the Git host steps run against the in-process server")
	}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()

			if db == e2e.StorePostgres && e2e.PostgresURL() == "" {
				t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
			}

			runGitHostWebhookSteps(t, db)
		})
	}
}

// webhookScenario holds what the steps share.
type webhookScenario struct {
	host hostScenario

	hookID string
	sender githubfake.Sender
}

func runGitHostWebhookSteps(t *testing.T, store e2e.Store) {
	t.Helper()

	ctx := t.Context()
	fake := e2e.NewGitHostFake(t)
	env := e2e.NewEnvWithGitHost(ctx, t, store, fake)

	checkReady(ctx, t, env)

	s := &webhookScenario{
		host: hostScenario{
			env: env, fake: fake, answers: nil, app: "/api/dustpan/v1/git-host-apps/github",
			assign: "/api/dustpan/v1/git-host-installations/" + e2e.GitHostInstallationID,
			conns:  "", create: "",
		},
		hookID: "",
		sender: githubfake.Sender{ID: 7, Login: "octo"},
	}

	s.operatorRegisters(t)
	s.operatorAssigns(t)
	s.githubDelivers(t)
	s.strangerDelivers(t)
	s.operatorRotates(t)
	s.operatorConfirms(t)

	for _, a := range s.host.answers {
		for _, secret := range []string{e2eEventSecret, e2eRotatedSecret, "PRIVATE KEY"} {
			if strings.Contains(a, secret) {
				t.Fatalf("an answer holds a secret: %s", a)
			}
		}
	}
}

// putBody returns the body of the registration with the event secret.
func (s *webhookScenario) putBody(t *testing.T, eventSecret string) string {
	t.Helper()

	key, err := json.Marshal(s.host.fake.PrivateKeyPEM())
	if err != nil {
		t.Fatalf("encode the key: %v", err)
	}

	return `{"appID":"` + e2e.GitHostAppID + `","apiAddress":"` + s.host.fake.URL() + `","privateKey":` + string(key) +
		`,"eventSecret":"` + eventSecret + `"}`
}

// registration is the answer of the registration route.
type webhookRegistration struct {
	HookID          string `json:"hookID"`
	OldSecretEndsAt *int64 `json:"oldSecretEndsAt"`
	Version         int64  `json:"version"`
}

func (s *webhookScenario) readRegistration(t *testing.T) webhookRegistration {
	t.Helper()

	var reg webhookRegistration

	if err := json.Unmarshal([]byte(s.host.do(t, http.MethodGet, s.host.app, "", http.StatusOK)), &reg); err != nil {
		t.Fatalf("decode the registration: %v", err)
	}

	return reg
}

// operatorRegisters registers the app and reads the hook ID.
func (s *webhookScenario) operatorRegisters(t *testing.T) {
	t.Helper()

	created := s.host.do(t, http.MethodPut, s.host.app, s.putBody(t, e2eEventSecret), http.StatusCreated)

	var reg webhookRegistration
	if err := json.Unmarshal([]byte(created), &reg); err != nil {
		t.Fatalf("decode the registration: %v", err)
	}

	if len(reg.HookID) != 32 || reg.OldSecretEndsAt != nil {
		t.Fatalf("registration = %+v, want a hook ID and no window", reg)
	}

	s.hookID = reg.HookID
}

func (s *webhookScenario) operatorAssigns(t *testing.T) {
	t.Helper()

	s.host.do(t, http.MethodPut, s.host.assign, `{"namespace":"`+e2e.Namespace+`"}`, http.StatusCreated)
}

// hook returns the hook of the fake with secret.
func (s *webhookScenario) hook(secret string) githubfake.Hook {
	return githubfake.Hook{ID: 1, Secret: []byte(secret)}
}

// deliver sends d to the hook address and checks the one answer: 204, no body.
func (s *webhookScenario) deliver(t *testing.T, hookID string, d githubfake.Delivery) {
	t.Helper()

	sent := s.host.env.DeliverWebhook(t.Context(), t, s.host.fake, "/webhooks/"+hookID, d)
	if sent.Status != http.StatusNoContent || len(sent.Answer) != 0 {
		t.Fatalf("delivery %s answered %d with %q, want 204 and no body", d.ID, sent.Status, sent.Answer)
	}
}

// noCommit is the head of a pull request that the steps do not read.
var noCommit githubfake.Commit //nolint:gochecknoglobals // A zero value for the events of the steps.

// pullRequest returns the event of a pull request that opened.
func (s *webhookScenario) pullRequest(number int) githubfake.Event {
	return s.host.fake.Events().PullRequestEvent(
		githubfake.ReviewOpened, "acme-org/web",
		githubfake.PullRequest{
			Number: number, Head: noCommit, HeadBranch: "feature", BaseBranch: "main", Merged: false,
		},
		s.sender,
	)
}

// githubDelivers sends a ping, a pull request event, and the same event again.
// The first two are accepted. The second send is a duplicate: the receipt of the
// first one stands, and the server drops the retry.
func (s *webhookScenario) githubDelivers(t *testing.T) {
	t.Helper()

	events := s.host.fake.Events()
	hook := s.hook(e2eEventSecret)

	s.deliver(t, s.hookID, events.NewDelivery(hook, events.Ping(hook, s.sender)))

	review := events.NewDelivery(hook, s.pullRequest(1))
	s.deliver(t, s.hookID, review)

	if got := s.rejects(t); len(got) != 0 {
		t.Fatalf("rejects after two good deliveries = %v, want none", got)
	}

	// GitHub sends the same delivery ID again.
	s.deliver(t, s.hookID, events.Redeliver(hook, review))

	if got := s.rejects(t); got["duplicate"] != 1 || len(got) != 1 {
		t.Fatalf("rejects after a redelivery = %v, want one duplicate", got)
	}
}

// strangerDelivers sends a wrong signature, a hook ID that the server does not
// know, an event the port does not name, and a body that is not JSON. Each gets
// the same answer, and each is counted under its own reason.
func (s *webhookScenario) strangerDelivers(t *testing.T) {
	t.Helper()

	events := s.host.fake.Events()
	good := s.hook(e2eEventSecret)

	s.deliver(t, s.hookID, events.NewDelivery(s.hook(e2eWrongSecret), s.pullRequest(2)))
	s.deliver(t, strings.Repeat("0", 32), events.NewDelivery(good, s.pullRequest(3)))
	s.deliver(t, s.hookID, events.NewDelivery(good, githubfake.RawEvent("push", []byte(`{"ref":"main"}`))))
	s.deliver(t, s.hookID, events.NewDelivery(good, githubfake.RawEvent("pull_request", []byte(`not json`))))

	got := s.rejects(t)
	for reason, want := range map[string]int{
		"signature": 1, "unknown-hook": 1, "unsupported": 1, "malformed": 1, "duplicate": 1,
	} {
		if got[reason] != want {
			t.Fatalf("rejects = %v, want %d of %s", got, want, reason)
		}
	}

	if n := s.rejectEntries(t, "signature"); n != 1 {
		t.Fatalf("%d audit entries for a wrong signature, want 1", n)
	}
}

// operatorRotates puts a new event secret. Both secrets sign for the window,
// and each delivery that the old secret signs leaves an entry.
func (s *webhookScenario) operatorRotates(t *testing.T) {
	t.Helper()

	s.host.do(t, http.MethodPut, s.host.app, s.putBody(t, e2eRotatedSecret), http.StatusOK)

	reg := s.readRegistration(t)
	if reg.OldSecretEndsAt == nil || *reg.OldSecretEndsAt <= 0 {
		t.Fatalf("registration = %+v, want the end of the window", reg)
	}

	events := s.host.fake.Events()

	s.deliver(t, s.hookID, events.NewDelivery(s.hook(e2eRotatedSecret), s.pullRequest(4)))

	if n := s.oldSecretEntries(t); n != 0 {
		t.Fatalf("%d old-secret entries after a delivery with the new secret, want 0", n)
	}

	before := s.rejects(t)["signature"]

	s.deliver(t, s.hookID, events.NewDelivery(s.hook(e2eEventSecret), s.pullRequest(5)))

	if n := s.oldSecretEntries(t); n != 1 {
		t.Fatalf("%d old-secret entries after a delivery with the old secret, want 1", n)
	}

	if got := s.rejects(t)["signature"]; got != before {
		t.Fatalf("signature rejects = %d, want %d: the window accepts the old secret", got, before)
	}
}

// operatorConfirms ends the window. The old secret signs no more.
func (s *webhookScenario) operatorConfirms(t *testing.T) {
	t.Helper()

	confirm := s.host.app + "/confirm-rotation"

	s.host.do(t, http.MethodPost, confirm, "", http.StatusOK)

	if reg := s.readRegistration(t); reg.OldSecretEndsAt != nil {
		t.Fatalf("registration = %+v, want no window", reg)
	}

	// A second confirm has no window.
	s.host.do(t, http.MethodPost, confirm, "", http.StatusConflict)

	events := s.host.fake.Events()
	before := s.rejects(t)["signature"]

	s.deliver(t, s.hookID, events.NewDelivery(s.hook(e2eEventSecret), s.pullRequest(6)))

	if got := s.rejects(t)["signature"]; got != before+1 {
		t.Fatalf("signature rejects = %d, want %d after the old secret signed", got, before+1)
	}

	if n := s.oldSecretEntries(t); n != 1 {
		t.Fatalf("%d old-secret entries, want still 1", n)
	}

	s.deliver(t, s.hookID, events.NewDelivery(s.hook(e2eRotatedSecret), s.pullRequest(7)))
}

var rejectedLine = regexp.MustCompile(`(?m)^dustpan_git_host_event_rejected_total\{reason="([a-z-]+)"\} (\d+)$`)

// rejects reads the counter of the rejects by reason.
func (s *webhookScenario) rejects(t *testing.T) map[string]int {
	t.Helper()

	status, body, err := s.host.env.ServerAPIStatus(t.Context(), http.MethodGet, "/api/server/metrics", "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("read the metrics = %d, %v", status, err)
	}

	out := map[string]int{}

	for _, m := range rejectedLine.FindAllStringSubmatch(body, -1) {
		n, convErr := strconv.Atoi(m[2])
		if convErr != nil {
			t.Fatalf("metric value %q: %v", m[2], convErr)
		}

		out[m[1]] = n
	}

	return out
}

// rejectEntries counts the audit entries of the rejects with reason.
func (s *webhookScenario) rejectEntries(t *testing.T, reason string) int {
	t.Helper()

	entries, _ := s.host.env.ServerRecords(t.Context(), t)
	n := 0

	for i := range entries {
		e := &entries[i]
		if e.Action == "git-host-event:receive" && e.Outcome == audit.OutcomeDenied &&
			strings.Contains(e.Reason, reason) {
			n++
		}
	}

	return n
}

// oldSecretEntries counts the entries of the events that the old secret signed.
func (s *webhookScenario) oldSecretEntries(t *testing.T) int {
	t.Helper()

	entries, _ := s.host.env.ServerRecords(t.Context(), t)
	n := 0

	for i := range entries {
		if entries[i].Action == "git-host-app:event-accepted-old-secret" {
			n++
		}
	}

	return n
}
