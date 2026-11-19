package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Names of the headers that GitHub sends with a delivery.
const (
	headerSignature = "X-Hub-Signature-256"
	headerEvent     = "X-GitHub-Event"
	headerDelivery  = "X-GitHub-Delivery"
)

// signaturePrefix starts the value of the signature header.
const signaturePrefix = "sha256="

// digestBytes is the length of an HMAC-SHA256 digest.
const digestBytes = sha256.Size

// Names of the events and of the actions that the port names.
const (
	eventNamePing  = "ping"
	eventNamePull  = "pull_request"
	actionOpened   = "opened"
	actionReopened = "reopened"
	actionSync     = "synchronize"
	actionClosed   = "closed"
)

// eventVerifier is the forge.Events of GitHub. It holds no state and makes no
// call to GitHub. Opener.Events returns it, and the ParseEvent method of an
// opened Forge runs the same function, so the two cannot differ.
type eventVerifier struct{}

var _ forge.Events = eventVerifier{}

// ParseEvent checks the signature of r against each secret and returns the
// event. See parseEvent.
func (eventVerifier) ParseEvent(_ context.Context, r forge.EventRequest, secrets [][]byte) (forge.Event, error) {
	return parseEvent(r, secrets, hmac.Equal)
}

// ParseEvent checks the signature of r against each secret and returns the
// event. It makes no call to GitHub. See parseEvent.
func (*gitHub) ParseEvent(_ context.Context, r forge.EventRequest, secrets [][]byte) (forge.Event, error) {
	return parseEvent(r, secrets, hmac.Equal)
}

// parseEvent is the one check of an event of GitHub. It accepts an event only
// when the header X-Hub-Signature-256 holds "sha256=" and the 64 lower case
// hex digits of the HMAC-SHA256 of the body under one of the secrets. It reads
// no other signature header: the SHA-1 header X-Hub-Signature is never read.
//
// equal compares two digests. The production value is hmac.Equal, which takes
// the same time for any content. A test gives a counting function.
//
// It ignores a secret shorter than forge.MinEventSecretBytes, as it ignores an
// empty one. It computes and compares the digest of each usable secret before
// it decides, so that the time does not tell which secret matched. The
// event name and the delivery ID are read only after a match: an input
// without a valid signature gets ErrSignature whatever else it holds.
func parseEvent(
	r forge.EventRequest, secrets [][]byte, equal func(a, b []byte) bool,
) (forge.Event, error) {
	sent, ok := signatureOf(r.Header)
	if !ok {
		return forge.Event{}, signatureError()
	}

	matched := false

	for _, secret := range secrets {
		// Defense in depth: forge.Registration.Validate refuses a short event
		// secret when a person saves it. This check stays so that a stored
		// secret of another version, or a caller that skips the registration,
		// can never make an event valid with a weak key. It does not rely on
		// the service or on the registration.
		if len(secret) < forge.MinEventSecretBytes {
			continue
		}

		m := hmac.New(sha256.New, secret)
		_, _ = m.Write(r.Body)

		// Each usable secret is compared, also after a match, so that the time
		// does not tell which secret matched.
		if equal(m.Sum(nil), sent) {
			matched = true
		}
	}

	if !matched {
		return forge.Event{}, signatureError()
	}

	return eventOf(r)
}

func signatureError() error {
	return fmt.Errorf("%w: no event secret matches the signature", forge.ErrSignature)
}

// single returns the one value of the header name. It reports false for a
// header that is missing, empty or has more than one value.
func single(h map[string][]string, name string) (string, bool) {
	values := http.Header(h).Values(name)
	if len(values) != 1 || values[0] == "" {
		return "", false
	}

	return values[0], true
}

// signatureOf returns the digest of the header X-Hub-Signature-256. The header
// needs one value of the form "sha256=" and 64 lower case hex digits.
func signatureOf(h map[string][]string) ([]byte, bool) {
	value, ok := single(h, headerSignature)
	if !ok || len(value) != len(signaturePrefix)+2*digestBytes || value[:len(signaturePrefix)] != signaturePrefix {
		return nil, false
	}

	digits := value[len(signaturePrefix):]
	for i := range len(digits) {
		c := digits[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil, false
		}
	}

	digest, err := hex.DecodeString(digits)
	if err != nil || len(digest) != digestBytes {
		return nil, false
	}

	return digest, true
}

// uuidGroups returns the number of hex digits in each group of a UUID.
func uuidGroups() [5]int { return [5]int{8, 4, 4, 4, 12} }

// validDeliveryID reports whether id has the form of a UUID: groups of 8, 4, 4,
// 4 and 12 hex digits, with a dash between two groups.
func validDeliveryID(id string) bool {
	groups := strings.Split(id, "-")
	sizes := uuidGroups()

	if len(groups) != len(sizes) {
		return false
	}

	for i, group := range groups {
		if len(group) != sizes[i] {
			return false
		}

		for j := range len(group) {
			c := group[j]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}

	return true
}

// Fields of the body of an event that the adapter reads.
const (
	wireAction       = "action"
	wireNumber       = "number"
	wireRepository   = "repository"
	wireFullName     = "full_name"
	wireInstallation = "installation"
	wireSender       = "sender"
	wireID           = "id"
	wireLogin        = "login"
)

// object decodes raw as a JSON object. The text null, an array and a value of
// another type are not an object.
func object(raw []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}

	return obj, true
}

// positive decodes raw as a whole number above zero and returns it as text. A
// string, a fraction, null and a missing field are refused.
func positive(raw json.RawMessage) (string, bool) {
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil || n <= 0 {
		return "", false
	}

	return strconv.FormatInt(n, 10), true
}

// text decodes raw as a string that is not empty.
func text(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || s == "" {
		return "", false
	}

	return s, true
}

// eventOf reads the headers and the body of a request whose signature matched.
func eventOf(r forge.EventRequest) (forge.Event, error) {
	id, ok := single(r.Header, headerDelivery)
	if !ok || !validDeliveryID(id) {
		return forge.Event{}, fmt.Errorf("%w: the delivery ID is missing or is not a UUID", forge.ErrInvalid)
	}

	name, ok := single(r.Header, headerEvent)
	if !ok {
		return forge.Event{}, fmt.Errorf("%w: the event name is missing", forge.ErrInvalid)
	}

	if name != eventNamePing && name != eventNamePull {
		return forge.Event{}, fmt.Errorf("%w: GitHub events of this kind are not in the port", forge.ErrUnsupported)
	}

	body, ok := object(r.Body)
	if !ok {
		return forge.Event{}, fmt.Errorf("%w: the event body is not a JSON object", forge.ErrInvalid)
	}

	if name == eventNamePull {
		return reviewEventOf(id, body)
	}

	// A ping carries no installation and no repository for an app. The sender
	// is read when it is there.
	var sender forge.Account

	if raw, found := body[wireSender]; found {
		var err error

		if sender, err = senderOf(raw); err != nil {
			return forge.Event{}, err
		}
	}

	return forge.Event{
		DeliveryID: id, Kind: forge.EventPing, Repo: "", ReviewID: "", InstallationID: "", Sender: sender,
	}, nil
}

// senderOf returns the account that caused the event.
func senderOf(raw json.RawMessage) (forge.Account, error) {
	invalid := fmt.Errorf("%w: the sender of the event has no ID or no login", forge.ErrInvalid)

	obj, ok := object(raw)
	if !ok {
		return forge.Account{}, invalid
	}

	id, idOK := positive(obj[wireID])
	login, loginOK := text(obj[wireLogin])

	if !idOK || !loginOK {
		return forge.Account{}, invalid
	}

	return forge.Account{ID: id, Login: login, ExpiresAt: time.Time{}, NoExpiry: false, Scopes: nil}, nil
}

// supportedAction returns nil for an action of a pull request that the port
// names: ErrInvalid for a missing action, ErrUnsupported for another one.
func supportedAction(raw json.RawMessage) error {
	action, ok := text(raw)
	if !ok {
		return fmt.Errorf("%w: the event has no action", forge.ErrInvalid)
	}

	switch action {
	case actionOpened, actionReopened, actionSync, actionClosed:
		return nil
	default:
		return fmt.Errorf("%w: this action of a pull request is not in the port", forge.ErrUnsupported)
	}
}

// repositoryOf returns the full name of the repository of the event, checked
// with the rule of the other methods: a later step uses the name in a path.
func repositoryOf(raw json.RawMessage) (string, error) {
	obj, ok := object(raw)
	if !ok {
		return "", fmt.Errorf("%w: the event has no repository", forge.ErrInvalid)
	}

	name, ok := text(obj[wireFullName])
	if !ok {
		return "", fmt.Errorf("%w: the event has no repository name", forge.ErrInvalid)
	}

	if _, err := splitRepo(name); err != nil {
		return "", err
	}

	return name, nil
}

// reviewEventOf returns the review event of the body of a pull request event.
func reviewEventOf(id string, body map[string]json.RawMessage) (forge.Event, error) {
	if err := supportedAction(body[wireAction]); err != nil {
		return forge.Event{}, err
	}

	installation, ok := object(body[wireInstallation])
	if !ok {
		return forge.Event{}, fmt.Errorf("%w: the event has no installation", forge.ErrInvalid)
	}

	installationID, ok := positive(installation[wireID])
	if !ok {
		return forge.Event{}, fmt.Errorf("%w: the event has no installation ID", forge.ErrInvalid)
	}

	number, ok := positive(body[wireNumber])
	if !ok {
		return forge.Event{}, fmt.Errorf("%w: the event has no pull request number", forge.ErrInvalid)
	}

	repo, err := repositoryOf(body[wireRepository])
	if err != nil {
		return forge.Event{}, err
	}

	sender, err := senderOf(body[wireSender])
	if err != nil {
		return forge.Event{}, err
	}

	return forge.Event{
		DeliveryID: id, Kind: forge.EventReview, Repo: repo, ReviewID: number, InstallationID: installationID,
		Sender: sender,
	}, nil
}
