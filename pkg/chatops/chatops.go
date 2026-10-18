// Package chatops holds the actions that a person can take from a chat
// message: acknowledge, open, and approve. The list is short on purpose. An
// action from chat never does more than the same person can do in the
// console, and an approval from chat needs the same fresh authentication
// as an approval in the console: chat alone never completes it.
//
// A chat identity maps to a principal only through a link that the person
// makes from a signed-in session. The functions of this package are pure.
// The Links and Intents ports keep links and pending approvals. The
// Verifier port checks that a request came from the chat tool.
package chatops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sentinel errors returned by this package and by the ports.
var (
	// ErrInvalid reports a request, a link or an intent that fails
	// validation.
	ErrInvalid = errors.New("invalid chat action")
	// ErrUnsupported reports an action that chat does not offer, or an
	// action on a target that it does not fit.
	ErrUnsupported = errors.New("action not available from chat")
	// ErrNotLinked reports a chat user with no link to a principal.
	ErrNotLinked = errors.New("chat user is not linked")
	// ErrNotFound reports an unknown link or intent.
	ErrNotFound = errors.New("chat data not found")
	// ErrDenied reports an intent that another principal tries to use.
	ErrDenied = errors.New("chat action denied")
	// ErrIntentExpired reports an intent after its expiry.
	ErrIntentExpired = errors.New("chat action expired")
	// ErrIntentUsed reports an intent that was used once already.
	ErrIntentUsed = errors.New("chat action already used")
	// ErrAuthStale reports an approval with no fresh authentication.
	ErrAuthStale = errors.New("approval needs a fresh authentication")
	// ErrSignature reports a request that the chat tool did not sign.
	ErrSignature = errors.New("chat request signature does not match")
)

// Bounds of chat actions.
const (
	// IntentIDPrefix starts every intent ID.
	IntentIDPrefix = "cin-"
	// IntentLifetime is how long a person has to authenticate after an
	// approve button.
	IntentLifetime = 5 * time.Minute
	// Freshness is the maximum age of the authentication of an approval,
	// the same number as in the console.
	Freshness = 15 * time.Minute
	// MaxIDLen caps the IDs of a platform, a workspace and a user.
	MaxIDLen = 200
)

// Action is an action that chat offers.
type Action string

// The actions of chat. No other action exists.
const (
	// ActionAcknowledge records that a person takes an incident. It stops
	// the escalation of its page.
	ActionAcknowledge Action = "acknowledge"
	// ActionOpen answers the link of the target in the console. It changes
	// nothing.
	ActionOpen Action = "open"
	// ActionApprove starts the approval of a job gate. It completes only
	// after a fresh authentication in the browser.
	ActionApprove Action = "approve"
)

// TargetKind names what an action acts on.
type TargetKind string

// Kinds of a target.
const (
	TargetJob      TargetKind = "job"
	TargetIncident TargetKind = "incident"
	TargetReview   TargetKind = "break-glass-review"
	TargetEvent    TargetKind = "event"
)

// Target is what an action acts on. Plan is the binding of the plans that
// the chat message showed, for an approval: the approval fails when the
// plans changed since.
type Target struct {
	Kind TargetKind
	ID   string
	Plan string
}

// Request is one action from a chat tool, after the Verifier checked it.
type Request struct {
	Platform  string
	Workspace string
	User      string
	Action    Action
	Target    Target
	At        time.Time
}

// Validate returns ErrInvalid for a request with a missing or long field.
func (r Request) Validate() error {
	for _, v := range []string{r.Platform, r.Workspace, r.User, r.Target.ID} {
		if v == "" || len(v) > MaxIDLen {
			return fmt.Errorf("%w: a request names a platform, a workspace, a user and a target", ErrInvalid)
		}
	}

	if r.At.IsZero() {
		return fmt.Errorf("%w: a request needs a time", ErrInvalid)
	}

	return nil
}

// Link maps a user of a chat tool to a principal. The person makes it
// from a signed-in session, so the chat tool alone can never claim a
// principal.
type Link struct {
	Platform  string
	Workspace string
	User      string
	Principal string
	LinkedAt  time.Time
}

// Outcome tells the caller what to do after Decide.
type Outcome string

// Outcomes of a decision.
const (
	// OutcomeApply applies the action now, as the linked principal, after
	// the check of Decision.Action on the object of the target.
	OutcomeApply Outcome = "apply"
	// OutcomeOpen answers the console link of the target and changes
	// nothing. The console checks the access of the person.
	OutcomeOpen Outcome = "open"
	// OutcomeStepUp stores an intent and answers a link, visible to the
	// person only, where the person authenticates again. The action
	// applies after that, through CheckIntent.
	OutcomeStepUp Outcome = "step-up"
)

// Decision is what Decide answers. Action is the authorization action
// that the principal needs on the object of the target, and empty for
// OutcomeOpen. Principal is the linked principal, and empty when the
// outcome needs no link.
type Decision struct {
	Outcome   Outcome
	Action    string
	Principal string
}

// Decide returns what to do with req for the chat user of link. link is
// nil when the user has no link. It returns ErrInvalid for a request that
// fails Validate, ErrUnsupported for an action that chat does not offer
// or for a target that the action does not fit, and ErrNotLinked for an
// action that changes something from a user with no link. The caller
// checks Decision.Action with the authorizer before it applies or stores
// anything, so a person never learns more from chat than from the API.
func Decide(req Request, link *Link) (Decision, error) {
	if err := req.Validate(); err != nil {
		return Decision{}, err
	}

	switch req.Action {
	case ActionOpen:
		return Decision{Outcome: OutcomeOpen, Action: "", Principal: ""}, nil
	case ActionAcknowledge, ActionApprove:
	default:
		return Decision{}, fmt.Errorf("%w: %q", ErrUnsupported, req.Action)
	}

	if link == nil || link.Principal == "" || !sameUser(*link, req) {
		return Decision{}, fmt.Errorf("%w: link the account in the console first", ErrNotLinked)
	}

	return decideLinked(req, link.Principal)
}

// decideLinked returns what to do with req for the linked principal. It
// returns ErrUnsupported for a target that the action does not fit.
func decideLinked(req Request, principal string) (Decision, error) {
	switch {
	case req.Action == ActionAcknowledge && req.Target.Kind == TargetIncident:
		return Decision{Outcome: OutcomeApply, Action: "incident:respond", Principal: principal}, nil
	case req.Action == ActionApprove && req.Target.Kind == TargetJob && req.Target.Plan != "":
		return Decision{Outcome: OutcomeStepUp, Action: "job:approve", Principal: principal}, nil
	default:
		return Decision{}, fmt.Errorf("%w: %s of a %s", ErrUnsupported, req.Action, req.Target.Kind)
	}
}

func sameUser(l Link, r Request) bool {
	return l.Platform == r.Platform && l.Workspace == r.Workspace && strings.EqualFold(l.User, r.User)
}

// AuthKind names the fresh authentication of an approval.
type AuthKind string

// Kinds of fresh authentication, as for the confirmation of a job.
const (
	AuthMFA AuthKind = "mfa"
	AuthSSO AuthKind = "sso"
)

// Intent is an approval that a person started from chat and must complete
// with a fresh authentication. It is good for one use, until ExpiresAt.
type Intent struct {
	ID        string
	Principal string
	Request   Request
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}

// NewIntent returns the intent of an approval of req by principal at now.
func NewIntent(id, principal string, req Request, now time.Time) Intent {
	return Intent{
		ID: id, Principal: principal, Request: req, CreatedAt: now, ExpiresAt: now.Add(IntentLifetime),
		UsedAt: time.Time{},
	}
}

// Auth is the authentication of the browser request that completes an
// intent.
type Auth struct {
	Principal string
	Agent     string
	Kind      AuthKind
	At        time.Time
}

// CheckIntent returns nil when auth can complete in at now. It returns
// ErrIntentUsed for an intent with a use, ErrIntentExpired after its
// expiry, ErrDenied for another principal or for an agent, and
// ErrAuthStale for an authentication that is missing, of an unknown kind,
// older than the intent, in the future, or older than Freshness. An
// authentication from before the button never counts: the person
// authenticates for this approval.
func CheckIntent(in Intent, auth Auth, now time.Time) error {
	switch {
	case !in.UsedAt.IsZero():
		return ErrIntentUsed
	case !now.Before(in.ExpiresAt):
		return ErrIntentExpired
	case auth.Principal != in.Principal || auth.Agent != "":
		return ErrDenied
	case auth.Kind != AuthMFA && auth.Kind != AuthSSO,
		auth.At.Before(in.CreatedAt),
		auth.At.After(now),
		now.Sub(auth.At) > Freshness:
		return fmt.Errorf("%w: authenticate again within %s", ErrAuthStale, Freshness)
	default:
		return nil
	}
}

// Verifier checks that a request came from the chat tool, with the signing
// secret of the installation for that tool, and parses it. An adapter for
// each tool implements it. It returns ErrSignature for a request with no
// valid signature or a timestamp outside the tolerance of the tool, and
// ErrInvalid for a body that it cannot parse.
type Verifier interface {
	Verify(ctx context.Context, headers map[string][]string, body []byte, now time.Time) (Request, error)
}
