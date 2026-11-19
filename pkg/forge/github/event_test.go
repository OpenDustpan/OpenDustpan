package github_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

const (
	headerSignature = "X-Hub-Signature-256"
	headerLegacy    = "X-Hub-Signature"
	headerEvent     = "X-Github-Event"
	headerDelivery  = "X-Github-Delivery"

	deliveryID = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

	// The secrets are 32 bytes long, which is the least that the verifier takes.
	secretCurrent = "current-secret-0123456789abcdefg"
	secretOld     = "old-secret-0123456789abcdef01234"
	secretOther   = "other-secret-0123456789abcdef012"
	// secretShort has 31 bytes.
	secretShort = "short-secret-0123456789abcdef01"
)

// mac returns the header value of the HMAC-SHA256 of body under secret.
func mac(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write(body)

	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

const reviewBody = `{"action":"opened","number":7,"pull_request":{"number":7},` +
	`"repository":{"full_name":"acme/infra"},"installation":{"id":9001},"sender":{"id":5,"login":"octo"}}`

const pingBody = `{"zen":"Keep it logically awesome.","hook_id":12,"sender":{"id":5,"login":"octo"}}`

// eventRequest returns a delivery of the event name with body, signed with
// secret, and with the header of GitHub.
func eventRequest(secret, name, body string) forge.EventRequest {
	return forge.EventRequest{
		Header: map[string][]string{
			headerSignature: {mac(secret, []byte(body))},
			headerEvent:     {name},
			headerDelivery:  {deliveryID},
		},
		Body: []byte(body),
	}
}

func review(body string) forge.EventRequest {
	return eventRequest(secretCurrent, "pull_request", body)
}

// withHeader returns r with the header name set to values. No value deletes
// the header.
func withHeader(r forge.EventRequest, name string, values ...string) forge.EventRequest {
	h := make(map[string][]string, len(r.Header))
	for k, v := range r.Header {
		h[k] = slices.Clone(v)
	}

	if len(values) == 0 {
		delete(h, name)
	} else {
		h[name] = values
	}

	return forge.EventRequest{Header: h, Body: r.Body}
}

func secrets(list ...string) [][]byte {
	out := make([][]byte, 0, len(list))
	for _, s := range list {
		out = append(out, []byte(s))
	}

	return out
}

// replace returns reviewBody with the first old text replaced by new.
func replace(old, replacement string) string {
	return strings.Replace(reviewBody, old, replacement, 1)
}

// TestEventSecretsOfTheTests checks the lengths that the table of
// TestParseEventVerifier needs: three secrets of the least length and one of a
// byte less.
func TestEventSecretsOfTheTests(t *testing.T) {
	t.Parallel()

	for _, s := range []string{secretCurrent, secretOld, secretOther} {
		if len(s) != forge.MinEventSecretBytes {
			t.Fatalf("the test secret %q has %d bytes, want %d", s, len(s), forge.MinEventSecretBytes)
		}
	}

	if len(secretShort) != forge.MinEventSecretBytes-1 {
		t.Fatalf("the short test secret has %d bytes, want %d", len(secretShort), forge.MinEventSecretBytes-1)
	}
}

// wantReviewEvent is the event that the good review request gives.
func wantReviewEvent() forge.Event {
	return forge.Event{
		DeliveryID: deliveryID, Kind: forge.EventReview, Repo: "acme/infra", ReviewID: "7", InstallationID: "9001",
		Sender: forge.Account{ID: "5", Login: "octo", ExpiresAt: time.Time{}, NoExpiry: false, Scopes: nil},
	}
}

// sigOf returns the signature header of r.
func sigOf(r forge.EventRequest) string { return http.Header(r.Header).Get(headerSignature) }

// signatureCases are the hostile requests for the signature header.
func signatureCases() []verifierCase {
	good := review(reviewBody)
	current := secrets(secretCurrent)
	wr := wantReviewEvent()

	return []verifierCase{
		// The signature header.
		{"valid review event", good, current, nil, &wr},
		{"no signature header", withHeader(good, headerSignature), current, forge.ErrSignature, nil},
		{"empty signature header", withHeader(good, headerSignature, ""), current, forge.ErrSignature, nil},
		{
			"two signature header values",
			withHeader(good, headerSignature, sigOf(good), sigOf(good)), current, forge.ErrSignature, nil,
		},
		{
			"only the legacy header, in the sha256 form",
			withHeader(withHeader(good, headerSignature), headerLegacy, sigOf(good)), current, forge.ErrSignature, nil,
		},
		{
			"a good legacy header beside a bad header",
			withHeader(withHeader(good, headerSignature, "sha256=00"), headerLegacy, sigOf(good)),
			current, forge.ErrSignature, nil,
		},
		{
			"sha1 form",
			withHeader(good, headerSignature, "sha1="+strings.TrimPrefix(sigOf(good), "sha256=")[:40]),
			current, forge.ErrSignature, nil,
		},
		{
			"another prefix",
			withHeader(good, headerSignature, "sha512="+strings.TrimPrefix(sigOf(good), "sha256=")),
			current, forge.ErrSignature, nil,
		},
		{
			"no prefix", withHeader(good, headerSignature, strings.TrimPrefix(sigOf(good), "sha256=")),
			current, forge.ErrSignature, nil,
		},
		{
			"upper case prefix",
			withHeader(good, headerSignature, "SHA256="+strings.TrimPrefix(sigOf(good), "sha256=")),
			current,
			forge.ErrSignature,
			nil,
		},
		{
			"upper case hex",
			withHeader(good, headerSignature, "sha256="+strings.ToUpper(strings.TrimPrefix(sigOf(good), "sha256="))),
			current,
			forge.ErrSignature,
			nil,
		},
		{
			"63 digits", withHeader(good, headerSignature, sigOf(good)[:len(sigOf(good))-1]),
			current, forge.ErrSignature, nil,
		},
		{
			"65 digits", withHeader(good, headerSignature, sigOf(good)+"0"),
			current, forge.ErrSignature, nil,
		},
		{
			"64 characters that are not hex",
			withHeader(good, headerSignature, "sha256="+strings.Repeat("z", 64)),
			current, forge.ErrSignature, nil,
		},
		{
			"signature with white space around it", withHeader(good, headerSignature, " "+sigOf(good)),
			current, forge.ErrSignature, nil,
		},
		// The body and the secrets.
		{
			"one byte of the body changed",
			forge.EventRequest{
				Header: good.Header,
				Body:   []byte(strings.Replace(reviewBody, `"number":7`, `"number":8`, 1)),
			},
			current,
			forge.ErrSignature,
			nil,
		},
		{
			"signature of the empty body",
			forge.EventRequest{
				Header: withHeader(good, headerSignature, mac(secretCurrent, nil)).Header,
				Body:   good.Body,
			},
			current,
			forge.ErrSignature,
			nil,
		},
	}
}

// secretListCases are the hostile lists of secrets.
func secretListCases() []verifierCase {
	good := review(reviewBody)
	wr := wantReviewEvent()

	return []verifierCase{
		{"secret that is not in the list", good, secrets(secretOther), forge.ErrSignature, nil},
		{
			"secret of 31 bytes that signs",
			eventRequest(secretShort, "pull_request", reviewBody), secrets(secretShort), forge.ErrSignature, nil,
		},
		{
			"empty secret that signs", eventRequest("", "pull_request", reviewBody), secrets(""),
			forge.ErrSignature, nil,
		},
		{"nil list", good, nil, forge.ErrSignature, nil},
		{"empty list", good, [][]byte{}, forge.ErrSignature, nil},
		{"a nil secret only", good, [][]byte{nil}, forge.ErrSignature, nil},
		{
			"old secret second in the list signs",
			eventRequest(secretOld, "pull_request", reviewBody),
			secrets(secretCurrent, secretOld),
			nil,
			&wr,
		},
		{"current secret first in the list signs", good, secrets(secretCurrent, secretOld), nil, &wr},
		{"short secret first, then the secret that signs", good, secrets(secretShort, secretCurrent), nil, &wr},
		{"empty secret first, then the secret that signs", good, secrets("", secretCurrent), nil, &wr},
	}
}

// deliveryCases are the requests with a bad delivery ID, and the order of the checks.
func deliveryCases() []verifierCase {
	good := review(reviewBody)
	current := secrets(secretCurrent)

	return []verifierCase{
		// The event name and the delivery ID are read after a match only.
		{
			"bad signature and no delivery ID",
			withHeader(withHeader(good, headerSignature, "sha256=00"), headerDelivery),
			current,
			forge.ErrSignature,
			nil,
		},
		{
			"bad signature and no event name", withHeader(withHeader(good, headerSignature, "sha256=00"), headerEvent),
			current, forge.ErrSignature, nil,
		},
		{
			"bad signature and an event outside the port",
			withHeader(withHeader(good, headerSignature, "sha256=00"), headerEvent, "push"),
			current, forge.ErrSignature, nil,
		},
		{
			"bad signature and a body that does not parse",
			forge.EventRequest{Header: withHeader(good, headerSignature, "sha256=00").Header, Body: []byte("{")},
			current, forge.ErrSignature, nil,
		},
		{
			"bad signature and a delivery ID with CR LF",
			withHeader(withHeader(good, headerSignature, "sha256=00"), headerDelivery, "a\r\nb"),
			current, forge.ErrSignature, nil,
		},
		// The delivery ID.
		{"no delivery ID", withHeader(good, headerDelivery), current, forge.ErrInvalid, nil},
		{"empty delivery ID", withHeader(good, headerDelivery, ""), current, forge.ErrInvalid, nil},
		{"two delivery IDs", withHeader(good, headerDelivery, deliveryID, deliveryID), current, forge.ErrInvalid, nil},
		{
			"delivery ID that is not a UUID",
			withHeader(good, headerDelivery, "not-a-uuid"),
			current,
			forge.ErrInvalid,
			nil,
		},
		{
			"delivery ID of 35 characters",
			withHeader(good, headerDelivery, deliveryID[:35]),
			current,
			forge.ErrInvalid,
			nil,
		},
		{
			"delivery ID of 37 characters",
			withHeader(good, headerDelivery, deliveryID+"0"),
			current,
			forge.ErrInvalid,
			nil,
		},
		{
			"delivery ID with CR LF", withHeader(good, headerDelivery, deliveryID[:30]+"\r\nX-Evil: 1"),
			current, forge.ErrInvalid, nil,
		},
		{
			"delivery ID with a character that is not hex",
			withHeader(good, headerDelivery, strings.Replace(deliveryID, "a", "g", 1)),
			current,
			forge.ErrInvalid,
			nil,
		},
	}
}

// eventCases are the requests with a bad event name or body.
func eventCases() []verifierCase {
	good := review(reviewBody)
	current := secrets(secretCurrent)
	wr := wantReviewEvent()

	return []verifierCase{
		// The event name.
		{"no event name", withHeader(good, headerEvent), current, forge.ErrInvalid, nil},
		{
			"two event names",
			withHeader(good, headerEvent, "pull_request", "pull_request"),
			current,
			forge.ErrInvalid,
			nil,
		},
		{"event push", eventRequest(secretCurrent, "push", reviewBody), current, forge.ErrUnsupported, nil},
		{"event issues", eventRequest(secretCurrent, "issues", reviewBody), current, forge.ErrUnsupported, nil},
		{
			"event with a body that does not parse and a kind outside the port",
			eventRequest(secretCurrent, "push", "{"),
			current,
			forge.ErrUnsupported,
			nil,
		},
		{
			"pull request labeled", review(replace(`"opened"`, `"labeled"`)), current, forge.ErrUnsupported, nil,
		},
		// The body.
		{"body that is not JSON", review("not json"), current, forge.ErrInvalid, nil},
		{"empty body", review(""), current, forge.ErrInvalid, nil},
		{"JSON array", review("[]"), current, forge.ErrInvalid, nil},
		{"JSON null", review("null"), current, forge.ErrInvalid, nil},
		{"body with text after the object", review(reviewBody + " {}"), current, forge.ErrInvalid, nil},
		{"no action", review(replace(`"action":"opened",`, "")), current, forge.ErrInvalid, nil},
		{"action that is a number", review(replace(`"opened"`, `1`)), current, forge.ErrInvalid, nil},
		{"no installation", review(replace(`"installation":{"id":9001},`, "")), current, forge.ErrInvalid, nil},
		{"installation ID as a string", review(replace(`"id":9001`, `"id":"9001"`)), current, forge.ErrInvalid, nil},
		{"installation ID 0", review(replace(`"id":9001`, `"id":0`)), current, forge.ErrInvalid, nil},
		{"installation ID below 0", review(replace(`"id":9001`, `"id":-4`)), current, forge.ErrInvalid, nil},
		{
			"no repository",
			review(replace(`"repository":{"full_name":"acme/infra"},`, "")),
			current,
			forge.ErrInvalid,
			nil,
		},
		{
			"pull request number as a string",
			review(replace(`"number":7,`, `"number":"7",`)),
			current,
			forge.ErrInvalid,
			nil,
		},
		{"pull request number 0", review(replace(`"number":7,`, `"number":0,`)), current, forge.ErrInvalid, nil},
		{"pull request number below 0", review(replace(`"number":7,`, `"number":-1,`)), current, forge.ErrInvalid, nil},
		{
			"pull request number with a fraction",
			review(replace(`"number":7,`, `"number":7.5,`)),
			current,
			forge.ErrInvalid,
			nil,
		},
		{"no pull request number", review(replace(`"number":7,`, "")), current, forge.ErrInvalid, nil},
		{"no sender", review(replace(`,"sender":{"id":5,"login":"octo"}`, "")), current, forge.ErrInvalid, nil},
		{"sender with no login", review(replace(`,"login":"octo"`, "")), current, forge.ErrInvalid, nil},
		{"sender ID 0", review(replace(`"id":5`, `"id":0`)), current, forge.ErrInvalid, nil},
		// The repository name, with the rule of the other methods.
		{
			"repository with a part of two dots",
			review(replace("acme/infra", "acme/..")),
			current,
			forge.ErrInvalid,
			nil,
		},
		{
			"repository with an owner of two dots",
			review(replace("acme/infra", "../infra")),
			current,
			forge.ErrInvalid,
			nil,
		},
		{
			"repository with a leading slash",
			review(replace("acme/infra", "/acme/infra")),
			current,
			forge.ErrInvalid,
			nil,
		},
		{"repository with a backslash", review(replace("acme/infra", `acme\\infra`)), current, forge.ErrInvalid, nil},
		{"repository with three parts", review(replace("acme/infra", "a/b/c")), current, forge.ErrInvalid, nil},
		{"repository with no slash", review(replace("acme/infra", "acme")), current, forge.ErrInvalid, nil},
		{"repository that is empty", review(replace("acme/infra", "")), current, forge.ErrInvalid, nil},
		{"repository with CR LF", review(replace("acme/infra", `acme/in\r\nfra`)), current, forge.ErrInvalid, nil},
		// The ping.
		{
			"ping", eventRequest(secretCurrent, "ping", pingBody), current, nil,
			&forge.Event{
				DeliveryID: deliveryID, Kind: forge.EventPing, Repo: "", ReviewID: "", InstallationID: "",
				Sender: wr.Sender,
			},
		},
		{"ping that is not JSON", eventRequest(secretCurrent, "ping", "zen"), current, forge.ErrInvalid, nil},
		{
			"ping with a bad signature",
			withHeader(eventRequest(secretCurrent, "ping", pingBody), headerSignature, "sha256=00"),
			current,
			forge.ErrSignature,
			nil,
		},
	}
}

// TestParseEventVerifier sends hostile requests to the verifier. It runs each
// request through the verifier of the Opener and through the ParseEvent
// method of an opened Forge: both must give the same answer.
func TestParseEventVerifier(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(signatureCases(), secretListCases(), deliveryCases(), eventCases())

	o := openHost(t, forge.AuthApp)
	opener := newHostOpener(t, o.host)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, "opener", opener)
			tc.check(t, "forge", o.fg)
		})
	}
}

// verifierCase is one request for the verifier with the wanted answer.
type verifierCase struct {
	name    string
	req     forge.EventRequest
	secrets [][]byte
	wantErr error
	want    *forge.Event
}

// check parses the request with events and compares the answer with the wanted
// error and event. An error comes with no event and with no part of a secret.
func (c verifierCase) check(t *testing.T, path string, events forge.Events) {
	t.Helper()

	got, err := events.ParseEvent(t.Context(), c.req, c.secrets)

	if c.wantErr == nil && err != nil || c.wantErr != nil && !errors.Is(err, c.wantErr) {
		t.Fatalf("%s: ParseEvent error = %v, want %v", path, err, c.wantErr)
	}

	if c.want != nil && !reflect.DeepEqual(got, *c.want) {
		t.Fatalf("%s: ParseEvent = %+v, want %+v", path, got, *c.want)
	}

	if err == nil {
		return
	}

	var none forge.Event
	if !reflect.DeepEqual(got, none) {
		t.Fatalf("%s: ParseEvent gave the event %+v with the error %v", path, got, err)
	}

	for _, s := range []string{secretCurrent, secretOld, secretOther, secretShort} {
		if strings.Contains(err.Error(), s) {
			t.Fatalf("%s: the error holds a secret: %v", path, err)
		}
	}
}

// TestParseEventReadsTheHeaderOfNetHTTP checks the header names of net/http:
// the canonical form is the form that the verifier reads.
func TestParseEventReadsTheHeaderOfNetHTTP(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)
	r := review(reviewBody)

	if _, err := o.fg.ParseEvent(t.Context(), r, secrets(secretCurrent)); err != nil {
		t.Fatalf("ParseEvent = %v", err)
	}
}

// TestCapabilitiesListEvents checks the capabilities of the verifier and of the
// token for the git protocol, for each auth kind.
func TestCapabilitiesListEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		auth          forge.AuthKind
		wantRepoToken bool
	}{
		{"app", forge.AuthApp, true},
		{"token", forge.AuthToken, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			caps := openHost(t, tc.auth).fg.Capabilities()

			if !caps.Has(forge.CapEvents) {
				t.Errorf("Capabilities = %v, want events", caps)
			}

			if !caps.Has(forge.CapGitAccess) {
				t.Errorf("Capabilities = %v, want git-access", caps)
			}

			if caps.Has(forge.CapRepoToken) != tc.wantRepoToken {
				t.Errorf(
					"Capabilities = %v, repo-token listed = %v, want %v",
					caps,
					!tc.wantRepoToken,
					tc.wantRepoToken,
				)
			}
		})
	}
}

// TestOpenerEventsMakesNoCall checks that the verifier of the Opener needs no
// connection and makes no call.
func TestOpenerEventsMakesNoCall(t *testing.T) {
	t.Parallel()

	o := openHost(t, forge.AuthApp)
	before := o.host.Calls()

	got, err := newHostOpener(t, o.host).ParseEvent(
		t.Context(), review(reviewBody), secrets(secretCurrent),
	)
	if err != nil || got.Kind != forge.EventReview {
		t.Fatalf("ParseEvent = %+v, %v", got, err)
	}

	if after := o.host.Calls(); after != before {
		t.Fatalf("ParseEvent made %d calls, want none", after-before)
	}
}
