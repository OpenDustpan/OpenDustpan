// Package chatops holds the actions that a person can take from a chat
// message: acknowledge, open, and approve. The list is short on purpose. An
// action from chat never does more than the same person can do in the
// console. An approval from chat needs a fresh authentication in a console
// session of the person: chat alone never completes it.
//
// A chat identity maps to a principal only through a link that the person
// accepts from a console session, with a one-time link request. The
// functions of this package are pure. The Links, LinkRequests and Intents
// ports keep links, link requests and pending approvals. The Verifier port
// checks that a request came from the chat tool.
package chatops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors returned by this package and by the ports.
var (
	// ErrInvalid reports a request, a link, a link request, an intent or a
	// fence that fails validation.
	ErrInvalid = errors.New("invalid chat action")
	// ErrUnsupported reports an action that chat does not offer, or an
	// action on a target that it does not fit.
	ErrUnsupported = errors.New("action not available from chat")
	// ErrWorkspace reports a workspace of a chat tool that is not on the
	// allowlist of the installation.
	ErrWorkspace = errors.New("chat workspace is not allowed")
	// ErrNotLinked reports a chat user with no link to a principal, or an
	// intent whose chat user is no longer linked to its principal.
	ErrNotLinked = errors.New("chat user is not linked")
	// ErrNotFound reports an unknown link, link request or intent.
	ErrNotFound = errors.New("chat data not found")
	// ErrConflict reports a write that collides with a stored row: an
	// intent or a link request with an ID that exists, or a link that
	// another link of the same chat user replaced.
	ErrConflict = errors.New("chat data conflict")
	// ErrDenied reports an intent that another principal, an agent or a
	// break-glass session tries to use.
	ErrDenied = errors.New("chat action denied")
	// ErrNeedsSession reports an intent that a request with an access
	// token, or with no console session, tries to complete.
	ErrNeedsSession = errors.New("chat approval needs a console session")
	// ErrGateChanged reports an intent for a job that now waits on another
	// phase or another gate than the one that the chat message showed.
	ErrGateChanged = errors.New("the job waits on another gate now")
	// ErrExpired reports an intent or a link request after its expiry.
	ErrExpired = errors.New("chat action expired")
	// ErrUsed reports an intent or a link request that was used once
	// already.
	ErrUsed = errors.New("chat action already used")
	// ErrAuthStale reports an approval with no fresh authentication.
	ErrAuthStale = errors.New("approval needs a fresh authentication")
	// ErrSignature reports a request that the chat tool did not sign, or
	// signed at a time outside the bounds.
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
	// the same number as for a confirmation in the console.
	Freshness = job.ConfirmFreshness
	// SignatureMaxAge is the maximum age of the signed timestamp of a
	// request from a chat tool.
	SignatureMaxAge = 5 * time.Minute
	// SignatureMaxSkew is how far in the future the signed timestamp of a
	// request can be, for the difference of the clocks.
	SignatureMaxSkew = time.Minute
	// MaxIDLen caps the IDs of a platform, a workspace, a user, a target
	// and a delivery.
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
	// after a fresh authentication in a console session.
	ActionApprove Action = "approve"
)

// TargetKind names what an action acts on.
type TargetKind string

// Kinds of a target.
const (
	TargetJob          TargetKind = "job"
	TargetIncident     TargetKind = "incident"
	TargetReview       TargetKind = "break-glass-review"
	TargetNotification TargetKind = "notification"
)

// Target is what an action acts on. For an approval, Phase is the index
// of the phase of the job, Gate is the type of the gate, and Plan is the
// binding of the plans that the chat message showed: the approval fails
// when the job waits on another phase or gate, or when the plans changed
// since. They are zero for other targets.
type Target struct {
	Kind  TargetKind
	ID    string
	Phase int
	Gate  job.GateType
	Plan  job.PlanBinding
}

// Request is one action from a chat tool, after the Verifier checked it.
// Platform is the chat tool of the Verifier that checked the request,
// never a value of the body. DeliveryID is the ID that the chat tool
// gives the request. A repeated request is safe: an acknowledge changes
// nothing the second time, and an approve makes a new intent that only a
// new fresh authentication of the linked person completes.
type Request struct {
	Platform   string
	Workspace  string
	User       string
	DeliveryID string
	Action     Action
	Target     Target
	At         time.Time
}

// Validate returns ErrInvalid for a request with a missing or long field.
func (r Request) Validate() error {
	if !validIDs(r.Platform, r.Workspace, r.User, r.DeliveryID, r.Target.ID) {
		return fmt.Errorf("%w: a request names a platform, a workspace, a user, a delivery and a target", ErrInvalid)
	}

	if r.At.IsZero() {
		return fmt.Errorf("%w: a request needs a time", ErrInvalid)
	}

	if r.Target.Phase < 0 {
		return fmt.Errorf("%w: the phase of a target is not negative", ErrInvalid)
	}

	return nil
}

// validIDs reports whether each of ids is not empty and at most MaxIDLen
// long.
func validIDs(ids ...string) bool {
	for _, v := range ids {
		if v == "" || len(v) > MaxIDLen {
			return false
		}
	}

	return true
}

// Workspaces is the allowlist of the installation: the workspace IDs that
// it accepts for each chat tool, by platform. An empty allowlist accepts
// no workspace.
type Workspaces map[string][]string

// Allows reports whether the allowlist has workspace for platform.
func (w Workspaces) Allows(platform, workspace string) bool {
	return platform != "" && workspace != "" && slices.Contains(w[platform], workspace)
}

// ChatUser names one user of a chat tool: the platform, the workspace of
// the tool and the user ID of the tool. Each ID is exact: two IDs that
// differ only in case are two users.
type ChatUser struct {
	Platform  string
	Workspace string
	User      string
}

// Link maps a user of a chat tool to a principal. The person accepts it
// from a console session, so the chat tool alone can never claim a
// principal.
type Link struct {
	Platform  string
	Workspace string
	User      string
	Principal string
	LinkedAt  time.Time
}

// ChatUser returns the chat user of l.
func (l Link) ChatUser() ChatUser {
	return ChatUser{Platform: l.Platform, Workspace: l.Workspace, User: l.User}
}

// Validate returns ErrInvalid for a link with an empty or long field, or
// with no time.
func (l Link) Validate() error {
	if !validIDs(l.Platform, l.Workspace, l.User, l.Principal) || l.LinkedAt.IsZero() {
		return fmt.Errorf("%w: a link names a platform, a workspace, a user, a principal and a time", ErrInvalid)
	}

	return nil
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

// Decide returns what to do with req for the chat user of link, with the
// allowlist allow. link is nil when the user has no link. It returns
// ErrInvalid for a request that fails Validate, ErrWorkspace for a
// workspace that allow does not have, ErrUnsupported for an action that
// chat does not offer or for a target that the action does not fit, and
// ErrNotLinked for an action that changes something from a user with no
// link. The chat user of link must be the user of req, with an exact
// match of each ID. The caller checks Decision.Action with the authorizer
// before it applies or stores anything, so a person never learns more
// from chat than from the API.
func Decide(req Request, link *Link, allow Workspaces) (Decision, error) {
	if err := req.Validate(); err != nil {
		return Decision{}, err
	}

	if !allow.Allows(req.Platform, req.Workspace) {
		return Decision{}, fmt.Errorf("%w: %s %s", ErrWorkspace, req.Platform, req.Workspace)
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
// returns ErrUnsupported for a target that the action does not fit. An
// approval fits only an approval gate whose plans the message showed.
func decideLinked(req Request, principal string) (Decision, error) {
	t := req.Target

	switch {
	case req.Action == ActionAcknowledge && t.Kind == TargetIncident:
		return Decision{Outcome: OutcomeApply, Action: "namespace:respond-incidents", Principal: principal}, nil
	case req.Action == ActionApprove && t.Kind == TargetJob && t.Gate == job.GateApproval && t.Plan.Plan != "":
		return Decision{Outcome: OutcomeStepUp, Action: "job:approve", Principal: principal}, nil
	default:
		return Decision{}, fmt.Errorf("%w: %s of a %s", ErrUnsupported, req.Action, t.Kind)
	}
}

// sameUser reports whether l is the link of the chat user of r. The IDs
// of a chat tool are exact: two IDs that differ only in case are two
// users.
func sameUser(l Link, r Request) bool {
	return l.Platform == r.Platform && l.Workspace == r.Workspace && l.User == r.User
}

// Intent is an approval that a person started from chat and must complete
// with a fresh authentication in a console session. It is good for one
// use, until ExpiresAt. Namespace is the namespace of the job.
type Intent struct {
	ID        string
	Namespace namespace.Name
	Principal string
	Request   Request
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
}

// NewIntent returns the intent of an approval of req by principal at now,
// for a job in the namespace ns.
func NewIntent(id string, ns namespace.Name, principal string, req Request, now time.Time) Intent {
	return Intent{
		ID: id, Namespace: ns, Principal: principal, Request: req, CreatedAt: now,
		ExpiresAt: now.Add(IntentLifetime), UsedAt: time.Time{},
	}
}

// Validate returns ErrInvalid for an intent with an ID that does not
// start with IntentIDPrefix, a namespace that is not valid, no principal,
// a request that fails Validate or that is not an approval, or times that
// do not match IntentLifetime.
func (in Intent) Validate() error {
	_, err := namespace.ParseName(string(in.Namespace))

	switch {
	case !validIDs(in.ID, in.Principal) || len(in.ID) <= len(IntentIDPrefix) || in.ID[:len(IntentIDPrefix)] !=
		IntentIDPrefix:
		return fmt.Errorf("%w: an intent has an ID with the prefix %s and a principal", ErrInvalid, IntentIDPrefix)
	case err != nil:
		return fmt.Errorf("%w: the namespace of an intent: %w", ErrInvalid, err)
	case in.Request.Action != ActionApprove:
		return fmt.Errorf("%w: an intent is for an approval", ErrInvalid)
	case in.CreatedAt.IsZero() || !in.ExpiresAt.Equal(in.CreatedAt.Add(IntentLifetime)):
		return fmt.Errorf("%w: an intent expires %s after its creation", ErrInvalid, IntentLifetime)
	}

	return in.Request.Validate()
}

// Auth is the authentication of the browser request that completes an
// intent. The server fills each field from the console session of the
// request and from the access token, if any. No client gives them.
// Session is the ID of the console session, and empty for a request with
// no session. TokenID is the ID of the access token of the request, and
// empty for a request of a console session. BreakGlass is true for an
// elevated break-glass session. Kind and At are the kind and the time of
// the last fresh authentication in the session.
type Auth struct {
	Principal  string
	Agent      string
	Session    string
	TokenID    string
	BreakGlass bool
	Kind       job.ConfirmAuth
	At         time.Time
}

// linkHolds reports whether link is the link that in was made with: the
// link of the chat user of the request of in, to the principal of in, and
// made at or before in. A link made after in is a new link, also for the
// same principal.
func linkHolds(in Intent, link *Link) bool {
	return link != nil && sameUser(*link, in.Request) && link.Principal == in.Principal &&
		!link.LinkedAt.After(in.CreatedAt)
}

// CheckIntent returns nil when auth can complete in at now, for the job
// whose current state is current: the kind and the ID of the job, the
// index of the phase that waits, the type of the gate that waits, and the
// binding of the stored plans of that phase. link is the current link of
// the chat user of the request of in (Links.LinkOf), or nil when that
// chat user has no link. It returns, in this order:
//   - ErrUsed for an intent with a use,
//   - ErrExpired after its expiry,
//   - ErrNeedsSession for a request with an access token or with no
//     console session,
//   - ErrDenied for another principal, an agent, or a break-glass session,
//   - ErrNotLinked when link is nil, is the link of another chat user,
//     names another principal than in, or was made after in, so an intent
//     stops when its link is deleted or replaced, also when the same
//     principal links the same chat user again,
//   - ErrGateChanged when the job waits on another phase or gate than in,
//   - job.ErrPlanChanged when the plans changed since the message,
//   - ErrAuthStale for an authentication that is missing, of an unknown
//     kind, older than the intent, in the future, or older than Freshness.
//
// An authentication from before the button never counts: the person
// authenticates for this approval.
func CheckIntent(in Intent, link *Link, auth Auth, current Target, now time.Time) error {
	switch {
	case !in.UsedAt.IsZero():
		return ErrUsed
	case !now.Before(in.ExpiresAt):
		return ErrExpired
	}

	if err := checkCaller(in, auth); err != nil {
		return err
	}

	if !linkHolds(in, link) {
		return fmt.Errorf("%w: the chat user of the intent is no longer linked to %s", ErrNotLinked, in.Principal)
	}

	if err := checkTarget(in.Request.Target, current); err != nil {
		return err
	}

	if !auth.Kind.Valid() || auth.At.Before(in.CreatedAt) || auth.At.After(now) || now.Sub(auth.At) > Freshness {
		return fmt.Errorf("%w: authenticate again within %s", ErrAuthStale, Freshness)
	}

	return nil
}

// checkCaller returns ErrNeedsSession for a request with an access token
// or with no console session, and ErrDenied for another principal than
// the one of in, an agent, or a break-glass session.
func checkCaller(in Intent, auth Auth) error {
	switch {
	case auth.Session == "" || auth.TokenID != "":
		return fmt.Errorf("%w: open the link in the console", ErrNeedsSession)
	case auth.Principal != in.Principal || auth.Agent != "" || auth.BreakGlass:
		return ErrDenied
	default:
		return nil
	}
}

// checkTarget returns ErrGateChanged when current is another job, phase
// or gate than t, and job.ErrPlanChanged when the plans changed.
func checkTarget(t, current Target) error {
	switch {
	case current.Kind != t.Kind || current.ID != t.ID || current.Phase != t.Phase || current.Gate != t.Gate:
		return fmt.Errorf("%w: open the job in the console", ErrGateChanged)
	case current.Plan != t.Plan:
		return fmt.Errorf("%w: open the job in the console", job.ErrPlanChanged)
	default:
		return nil
	}
}

// CheckTimestamp returns ErrSignature when the signed time at of a
// request is older than SignatureMaxAge at now, or later than now plus
// SignatureMaxSkew. A Verifier calls it.
func CheckTimestamp(at, now time.Time) error {
	if now.Sub(at) > SignatureMaxAge || at.Sub(now) > SignatureMaxSkew {
		return fmt.Errorf("%w: the request time is outside %s", ErrSignature, SignatureMaxAge)
	}

	return nil
}

// Verifier checks that a request came from the chat tool, with the signing
// secret of the installation for that tool, and parses it. An adapter for
// each tool implements it and runs the suite chatopstest.RunVerifier. The
// adapter compares the signature in constant time (hmac.Equal), signs the
// timestamp with the body, and checks the timestamp with CheckTimestamp.
// The Request that it returns has At set to the signed time, Platform set
// to the chat tool of the adapter, and DeliveryID set to the ID that the
// tool gives the request. A platform in the body never counts, so an
// adapter of one tool can never act for the link of a user of another
// tool. It returns ErrSignature for a request with a missing header, a
// signature that does not match the body, or a timestamp outside the
// bounds, also when the body does not parse, and ErrInvalid for a signed
// body that it cannot parse or that fails Request.Validate. It checks the
// signature before it parses the body.
type Verifier interface {
	Verify(ctx context.Context, headers map[string][]string, body []byte, now time.Time) (Request, error)
}
