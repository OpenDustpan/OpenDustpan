package githubfake

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Header names of a delivery.
const (
	HeaderEvent     = "X-Github-Event"
	HeaderDelivery  = "X-Github-Delivery"
	HeaderSignature = "X-Hub-Signature-256"
	// signaturePrefix starts the value of HeaderSignature.
	signaturePrefix = "sha256="
)

// webhookTimeout is how long GitHub waits for the answer to a delivery.
const webhookTimeout = 10 * time.Second

// maxRecordedAnswer is the most bytes of the answer to a delivery that the
// fake keeps.
const maxRecordedAnswer = 4 << 10

// Hook is the webhook of the app: the secret that signs each delivery and
// the ID that the headers name. GitHub has one hook for all installations of
// an app.
type Hook struct {
	// ID is the ID of the hook.
	ID int64
	// Secret signs the body. An empty secret sends no signature, as GitHub
	// does for a hook with no secret.
	Secret []byte
}

// Event is an event with its name and its JSON body.
type Event struct {
	Name string
	Body []byte
}

// Delivery is one request that GitHub sends: the header and the body.
type Delivery struct {
	// ID is the delivery ID, the value of X-GitHub-Delivery.
	ID     string
	Header http.Header
	Body   []byte
}

// Sent is a delivery that the fake sent, with the answer of the receiver.
type Sent struct {
	Delivery Delivery
	// URL is the address that the fake sent the delivery to.
	URL string
	// Status is the status of the answer, or zero when the call failed.
	Status int
	// Answer is the start of the body of the answer.
	Answer []byte
}

// Sign returns the HMAC-SHA256 of body with secret, in the form of the header
// X-Hub-Signature-256: "sha256=" and the hex digest.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)

	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// deliveryID returns the next delivery ID, in the form of a UUID. The caller
// holds no lock.
func (f *Fake) deliveryID() string {
	f.mu.Lock()
	n := f.next()
	f.mu.Unlock()

	sum := sha256.Sum256([]byte("delivery-" + strconv.FormatUint(n, 10)))
	h := hex.EncodeToString(sum[:16])

	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// NewDelivery returns the request that GitHub sends for ev to hook, with a
// new delivery ID. The fake sends nothing: Send does.
func (f *Fake) NewDelivery(hook Hook, ev Event) Delivery {
	return f.sign(hook, ev, f.deliveryID())
}

// Redeliver returns d again, as GitHub sends a retry: the same body and the
// same delivery ID, signed again.
func (f *Fake) Redeliver(hook Hook, d Delivery) Delivery {
	return f.sign(hook, Event{Name: d.Header.Get(HeaderEvent), Body: d.Body}, d.ID)
}

func (f *Fake) sign(hook Hook, ev Event, id string) Delivery {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "GitHub-Hookshot/fake")
	h.Set(HeaderEvent, ev.Name)
	h.Set(HeaderDelivery, id)
	h.Set("X-Github-Hook-Id", strconv.FormatInt(hook.ID, 10))
	h.Set("X-Github-Hook-Installation-Target-Id", f.cfg.AppID)
	h.Set("X-Github-Hook-Installation-Target-Type", "integration")

	if len(hook.Secret) > 0 {
		h.Set(HeaderSignature, Sign(hook.Secret, ev.Body))
	}

	return Delivery{ID: id, Header: h, Body: slices.Clone(ev.Body)}
}

// Send posts d to target, as GitHub does, and records the answer. A status
// that is not a success is a result, not an error. It returns an error when
// the call does not reach the receiver.
func (f *Fake) Send(ctx context.Context, target string, d Delivery) (Sent, error) {
	sent := Sent{Delivery: d, URL: target, Status: 0, Answer: nil}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(d.Body))
	if err != nil {
		return sent, fmt.Errorf("build the delivery: %w", err)
	}

	req.Header = d.Header.Clone()

	resp, err := f.webhookClient().Do(req)
	if err != nil {
		return sent, fmt.Errorf("send the delivery: %w", err)
	}

	sent.Status = resp.StatusCode

	answer, readErr := io.ReadAll(io.LimitReader(resp.Body, maxRecordedAnswer))
	closeErr := resp.Body.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return sent, fmt.Errorf("read the answer to the delivery: %w", err)
	}

	sent.Answer = answer

	f.mu.Lock()
	f.deliveries = append(f.deliveries, sent)
	f.mu.Unlock()

	return sent, nil
}

// Deliveries returns the deliveries that the fake sent, in order.
func (f *Fake) Deliveries() []Sent {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.deliveries)
}

func (f *Fake) webhookClient() *http.Client {
	if f.cfg.WebhookClient != nil {
		return f.cfg.WebhookClient
	}

	return &http.Client{
		Timeout:       webhookTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Sender is the account that caused an event.
type Sender struct {
	ID    int64
	Login string
}

// Ping returns the event that GitHub sends when a person sets up the hook.
func (f *Fake) Ping(hook Hook, sender Sender) Event {
	return event("ping", map[string]any{
		"zen":     "Keep it logically awesome.",
		"hook_id": hook.ID,
		"hook": map[string]any{
			"type": "App", "id": hook.ID, "active": true, "app_id": json.Number(f.cfg.AppID),
		},
		"sender": wireAccount(sender),
	})
}

// ReviewAction is the action of a pull request event.
type ReviewAction string

// The actions that the fake sends.
const (
	ReviewOpened      ReviewAction = "opened"
	ReviewSynchronize ReviewAction = "synchronize"
	ReviewClosed      ReviewAction = "closed"
)

// PullRequest describes a pull request in an event.
type PullRequest struct {
	Number int
	Head   Commit
	// HeadBranch and BaseBranch name the branches.
	HeadBranch string
	BaseBranch string
	// Merged is true for a pull request that closed with a merge.
	Merged bool
}

// refs are the branches of a pull request in an event.
type branchRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha,omitempty"`
}

// PullRequestEvent returns the event "pull_request" of an app installation for
// the pull request pr of the repository fullName.
func (f *Fake) PullRequestEvent(action ReviewAction, fullName string, pr PullRequest, sender Sender) Event {
	state := "open"
	if action == ReviewClosed {
		state = "closed"
	}

	owner, name, _ := strings.Cut(fullName, "/")

	return event("pull_request", map[string]any{
		"action": string(action),
		"number": pr.Number,
		"pull_request": map[string]any{
			"number": pr.Number, "state": state, "merged": pr.Merged,
			"head": branchRef{Ref: pr.HeadBranch, SHA: pr.Head.ID},
			"base": branchRef{Ref: pr.BaseBranch, SHA: ""},
		},
		"repository": map[string]any{
			"full_name": fullName, "name": name, "owner": map[string]string{"login": owner},
		},
		"installation": map[string]json.Number{"id": json.Number(f.cfg.InstallationID)},
		"sender":       wireAccount(sender),
	})
}

// RawEvent returns an event of any name and body, for example a name that
// the receiver does not know, or a body that does not parse.
func RawEvent(name string, body []byte) Event {
	return Event{Name: name, Body: slices.Clone(body)}
}

func event(name string, payload any) Event {
	body, err := json.Marshal(payload)
	if err != nil {
		// The payloads hold only strings, numbers and booleans.
		panic("githubfake: " + err.Error())
	}

	return Event{Name: name, Body: body}
}
