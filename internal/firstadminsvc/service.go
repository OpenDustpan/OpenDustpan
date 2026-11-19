// Package firstadminsvc is the service of the first administrator: the offer
// of a one-time code by the bootstrap administrator, the redeem of the code
// by the named person, and the read of the marker.
//
// The service checks each input from outside the process, makes the code
// and its digest, builds the audit entry of each write, and writes a failure
// entry for each refusal. It never puts a code or a digest in an audit
// entry, a log record or an error. The rules of the redeem that need the
// stored rows live in package firstadmin and run in the transaction of the
// store.
package firstadminsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// Sentinel errors of the service, besides the errors of package firstadmin.
var (
	// ErrNotBootstrap reports an offer from a caller that is not the static
	// bootstrap administrator.
	ErrNotBootstrap = errors.New("the caller is not the bootstrap administrator")
	// ErrUnavailable reports a port that failed in a way that has no domain
	// error. The request is refused and nothing changed.
	ErrUnavailable = errors.New("the first administrator service is unavailable")
)

// The failed rules, as the audit entry of a refusal names them. Each is a
// fixed text. None holds a code, a digest or text from the request.
const (
	RuleCallerNotBootstrap  = "caller-not-bootstrap"
	RulePrincipalForm       = "principal-id-form"
	RulePrincipalNotAllowed = "principal-not-allowed"
	RuleReasonInvalid       = "reason-invalid"
	RuleCodeForm            = "code-form"
	RuleCodeInvalid         = "code-invalid"
	RuleAlreadyMade         = "already-made"
	RuleGrantExists         = "grant-exists"
	RuleStaleSignIn         = "stale-sign-in"
	RuleSessionInvalid      = "session-invalid"
	RuleSessionInput        = "session-input"
	RuleSubjectMismatch     = "subject-mismatch"
	RuleRandomSource        = "random-source"
	RuleStoreError          = "store-error"
)

const (
	maxPrincipalIDLength = 64
	maxGrantIDLength     = 64
	maxSessionIDLength   = 256
)

// Entry keys of the changes of an audit entry.
const (
	changeRule     = "rule"
	changeReplaced = "replaced"
	changeGrant    = "grant"
	changeCreator  = firstadmin.ChangeCreatedBy
	changeSession  = "session"
	changeRole     = "role"
)

// Principals reads the live principal with an ID.
type Principals interface {
	// GetPrincipal returns the principal, or principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}

// Config holds the dependencies of a Service. Each field is required.
type Config struct {
	// Store is the port of the first administrator.
	Store firstadmin.Store
	// Principals reads the principal of an offer and of a redeem.
	Principals Principals
	// TempAdmins reads the records of the temporary administrators.
	TempAdmins tempadmin.Reader
	// Audit writes the failure entry of a refusal.
	Audit audit.ServerStore
	// Now is the clock.
	Now func() time.Time
	// Random is the random source of a code.
	Random io.Reader
	// GrantID makes the ID of the grant of a redeem. Production code passes
	// ident.NewID with access.GrantIDPrefix.
	GrantID func() string
	// Window returns the freshness window of the session policy.
	Window func(ctx context.Context) time.Duration
	// Logger receives the log records of the service.
	Logger *slog.Logger
}

// Request holds the facts of one request that an audit entry records.
type Request struct {
	SourceIP  string
	UserAgent string
	RequestID string
	Channel   audit.Channel
}

// Service is the service of the first administrator.
type Service struct {
	cfg Config
}

// New returns a Service. It returns firstadmin.ErrMissingDependency when a
// field of cfg is nil.
func New(cfg Config) (*Service, error) {
	for _, dep := range []struct {
		name    string
		missing bool
	}{
		{name: "store", missing: cfg.Store == nil},
		{name: "principals", missing: cfg.Principals == nil},
		{name: "temporary administrators", missing: cfg.TempAdmins == nil},
		{name: "audit", missing: cfg.Audit == nil},
		{name: "clock", missing: cfg.Now == nil},
		{name: "random source", missing: cfg.Random == nil},
		{name: "grant ID source", missing: cfg.GrantID == nil},
		{name: "freshness window source", missing: cfg.Window == nil},
		{name: "logger", missing: cfg.Logger == nil},
	} {
		if dep.missing {
			return nil, fmt.Errorf("%w: %s", firstadmin.ErrMissingDependency, dep.name)
		}
	}

	return &Service{cfg: cfg}, nil
}

// OfferInput is the input of Offer. Caller is the principal of the request.
type OfferInput struct {
	Caller      identity.Principal
	PrincipalID string
	Reason      string
	Request     Request
}

// OfferResult is the answer of Offer. Code is shown once, here only.
type OfferResult struct {
	ExpiresAt   time.Time
	PrincipalID string
	Code        firstadmin.Code
	Replaced    bool
}

// IsBootstrap reports whether p is the static bootstrap administrator: the
// administrator flag, no kind, no stored token, no credential kind, no job
// and no temporary administrator. A personal token with Admin, a console
// session, a job token and a temporary administrator all fail one of these
// facts.
func IsBootstrap(p identity.Principal) bool {
	return p.Admin &&
		p.Subject != "" &&
		p.Kind == "" &&
		p.TokenID == "" &&
		p.Credential == "" &&
		p.JobID == "" &&
		!p.TemporaryAdministrator &&
		p.TemporaryExpiresAt.IsZero() &&
		p.TokenExpiresAt.IsZero()
}

// attempt holds the facts that each refusal of one call records.
type attempt struct {
	now     time.Time
	request Request
	subject string
	action  string
	object  string
}

// Offer makes a one-time code for the person principalID and returns it
// once. Every refusal writes a failure entry and returns an error; no
// refusal returns a code.
func (s *Service) Offer(ctx context.Context, in OfferInput) (OfferResult, error) {
	a := attempt{
		now: s.cfg.Now(), request: in.Request, subject: in.Caller.Subject,
		action: string(identity.ActionServerOfferFirstAdministrator), object: principal.Ref(in.PrincipalID),
	}

	if rule, err := screenOffer(in); err != nil {
		s.refuse(ctx, a, rule)

		return OfferResult{}, err
	}

	state, err := s.cfg.Store.State(ctx, a.now)
	if err != nil {
		return OfferResult{}, s.refuseStore(ctx, a, err)
	}

	// Defense in depth: the store refuses an offer after the marker too.
	// This check stays so that no code is made for a closed route.
	if state.Marker != nil {
		s.refuse(ctx, a, RuleAlreadyMade)

		return OfferResult{}, firstadmin.ErrAlreadyMade
	}

	if perr := s.checkPrincipal(ctx, in.PrincipalID); perr != nil {
		s.refuse(ctx, a, RulePrincipalNotAllowed)

		return OfferResult{}, perr
	}

	code, err := firstadmin.NewCode(s.cfg.Random)
	if err != nil {
		s.refuse(ctx, a, RuleRandomSource)

		return OfferResult{}, fmt.Errorf("%w: make the code: %w", ErrUnavailable, err)
	}

	entry := s.entry(a)
	entry.ObjectID = in.PrincipalID
	entry.Reason = in.Reason
	entry.Changes = []audit.Change{change(changeReplaced, strconv.FormatBool(state.Offer != nil))}

	res, err := s.cfg.Store.Offer(ctx, firstadmin.OfferInput{
		Now: a.now, PrincipalID: in.PrincipalID, Digest: code.Digest(), CreatedBy: in.Caller.Subject, Reason: in.Reason,
	}, entry)
	if err != nil {
		return OfferResult{}, s.refuseStore(ctx, a, err)
	}

	return OfferResult{ExpiresAt: res.ExpiresAt, PrincipalID: in.PrincipalID, Code: code, Replaced: res.Replaced}, nil
}

// screenOffer checks the input of an offer that needs no port. It returns the
// failed rule and the error, or no rule and no error.
//
// Defense in depth: each check here stays so that a bad input never reaches
// a port, and none relies on the route, the handler or the store. The route
// requires Admin, the handler requires the bootstrap principal and decodes
// the body, and the store checks the live row.
func screenOffer(in OfferInput) (string, error) {
	switch {
	case !IsBootstrap(in.Caller):
		return RuleCallerNotBootstrap, ErrNotBootstrap
	case !validPrincipalID(in.PrincipalID):
		return RulePrincipalForm, fmt.Errorf("%w: the principal ID is not an ID", firstadmin.ErrPrincipalNotPerson)
	default:
		if err := firstadmin.ValidateReason(in.Reason); err != nil {
			return RuleReasonInvalid, err
		}

		return "", nil
	}
}

func change(key, value string) audit.Change {
	return audit.Change{Key: key, Old: nil, New: value, Layer: "", TakesEffect: ""}
}

// RedeemInput is the input of Redeem. The principal ID, the session ID and
// the sign-in time come from the console session of the caller, never from
// the body of the request. Code is the only value of the body.
type RedeemInput struct {
	AuthTime    time.Time
	Caller      identity.Principal
	PrincipalID string
	SessionID   string
	Code        firstadmin.Code
	Request     Request
}

// Redeem spends the code and makes the first administrator in one
// transaction of the store. Every refusal writes a failure entry.
func (s *Service) Redeem(ctx context.Context, in RedeemInput) (firstadmin.Result, error) {
	a := attempt{
		now: s.cfg.Now(), request: in.Request, subject: in.Caller.Subject,
		action: string(identity.ActionCallerRedeemFirstAdministrator), object: identity.ServerObject().String(),
	}
	window := s.cfg.Window(ctx)

	if rule, err := screenRedeem(in, a.now, window); err != nil {
		s.refuse(ctx, a, rule)

		return firstadmin.Result{}, err
	}

	offer, err := s.openOffer(ctx, a, in.PrincipalID)
	if err != nil {
		return firstadmin.Result{}, err
	}

	if cerr := s.checkSession(ctx, a, in); cerr != nil {
		return firstadmin.Result{}, cerr
	}

	grantID := s.cfg.GrantID()
	if !validGrantID(grantID) {
		s.refuse(ctx, a, RuleStoreError)

		return firstadmin.Result{}, fmt.Errorf("%w: the grant ID is not an ID", ErrUnavailable)
	}

	entry := s.entry(a)
	entry.PrincipalKind = audit.PrincipalKindPerson
	entry.TokenID = in.Caller.TokenID
	entry.Reason = offer.Reason
	entry.Changes = []audit.Change{
		change(changeCreator, offer.CreatedBy),
		change(changeGrant, grantID),
		change(changeRole, firstadmin.Role),
		change(changeSession, in.SessionID),
	}

	res, err := s.cfg.Store.Redeem(ctx, firstadmin.RedeemInput{
		Now: a.now, AuthTime: in.AuthTime, PrincipalID: in.PrincipalID, SessionID: in.SessionID,
		Digest: in.Code.Digest(), GrantID: grantID, Window: window,
	}, entry)
	if err != nil {
		return firstadmin.Result{}, s.refuseStore(ctx, a, err)
	}

	return res, nil
}

// State returns the marker and the open offer. It never returns a code or a
// digest.
func (s *Service) State(ctx context.Context) (firstadmin.State, error) {
	st, err := s.cfg.Store.State(ctx, s.cfg.Now())
	if err != nil {
		return firstadmin.State{}, fmt.Errorf("%w: read the state: %w", ErrUnavailable, err)
	}

	return st, nil
}

// screenRedeem checks the input of a redeem that needs no port. It returns
// the failed rule and the error, or no rule and no error.
//
// Defense in depth: each check here stays so that a bad input never reaches
// a port, and none relies on the handler or the store. The handler reads the
// session, checks the form of the code and checks the fresh sign-in, and the
// store transaction compares the principal of the code with the session and
// checks the sign-in time again. A bad form gets the one answer of a wrong
// code, so the answer never says which part failed.
func screenRedeem(in RedeemInput, now time.Time, window time.Duration) (string, error) {
	age := now.Sub(in.AuthTime)

	switch {
	case !validPrincipalID(in.PrincipalID), in.SessionID == "", len(in.SessionID) > maxSessionIDLength,
		in.AuthTime.IsZero(), in.Caller.Subject == "":
		return RuleSessionInput, firstadmin.ErrCodeInvalid
	case !in.Code.ValidForm():
		return RuleCodeForm, firstadmin.ErrCodeInvalid
	case window <= 0, age < 0, age > window:
		return RuleStaleSignIn, firstadmin.ErrStaleSignIn
	default:
		return "", nil
	}
}

// openOffer reads the state and returns the open offer for the person. An
// offer for another person gets the one answer of a wrong code.
//
// Defense in depth: the store transaction reads the code row again, and it
// sets the reason and the creator of the entry from that row. The reason and
// the creator that this read gives are a first value only: an offer can
// replace the code between this read and the transaction, so the service never
// relies on them. The read stays so that a request with no open offer for the
// person never reaches the store.
func (s *Service) openOffer(ctx context.Context, a attempt, principalID string) (firstadmin.Offer, error) {
	state, err := s.cfg.Store.State(ctx, a.now)
	if err != nil {
		return firstadmin.Offer{}, s.refuseStore(ctx, a, err)
	}

	// Defense in depth: the store transaction reads the marker too. This
	// check stays so that a closed route never reaches the store.
	if state.Marker != nil {
		s.refuse(ctx, a, RuleAlreadyMade)

		return firstadmin.Offer{}, firstadmin.ErrAlreadyMade
	}

	if state.Offer == nil || state.Offer.PrincipalID != principalID {
		s.refuse(ctx, a, RuleCodeInvalid)

		return firstadmin.Offer{}, firstadmin.ErrCodeInvalid
	}

	return *state.Offer, nil
}

// checkSession reads the principal of the session and checks it.
//
// Defense in depth: the store transaction runs the same principal checks
// and compares the principal of the code with the session. These checks stay
// so that a mismatch of a session and a row, or a principal that cannot be
// the first administrator, never reaches the store.
func (s *Service) checkSession(ctx context.Context, a attempt, in RedeemInput) error {
	person, err := s.cfg.Principals.GetPrincipal(ctx, in.PrincipalID)
	if err != nil {
		if errors.Is(err, principal.ErrNotFound) {
			s.refuse(ctx, a, RulePrincipalNotAllowed)

			return fmt.Errorf("%w: no such principal", firstadmin.ErrPrincipalNotLive)
		}

		s.refuse(ctx, a, RuleStoreError)

		return fmt.Errorf("%w: read the principal: %w", ErrUnavailable, err)
	}

	if person.Subject != in.Caller.Subject {
		s.refuse(ctx, a, RuleSubjectMismatch)

		return fmt.Errorf("%w: the session and the principal differ", firstadmin.ErrPrincipalNotLive)
	}

	if err := s.checkPrincipal(ctx, in.PrincipalID); err != nil {
		s.refuse(ctx, a, RulePrincipalNotAllowed)

		return err
	}

	return nil
}

// checkPrincipal reads the live principal and its temporary administrator
// record and runs firstadmin.CheckPrincipal. Every error of a read that is
// not "not found" refuses the request.
//
// Defense in depth: the store runs the same check in its transaction. This
// check stays so that a request for a principal that cannot be the first
// administrator is refused before a code is made or a store is asked.
func (s *Service) checkPrincipal(ctx context.Context, id string) error {
	p, err := s.cfg.Principals.GetPrincipal(ctx, id)
	if err != nil {
		if errors.Is(err, principal.ErrNotFound) {
			return fmt.Errorf("%w: no such principal", firstadmin.ErrPrincipalNotLive)
		}

		return fmt.Errorf("%w: read the principal: %w", ErrUnavailable, err)
	}

	temp := false

	if _, err := s.cfg.TempAdmins.GetTempAdminBySubject(ctx, p.Subject); err == nil {
		temp = true
	} else if !errors.Is(err, tempadmin.ErrNotFound) {
		return fmt.Errorf("%w: read the temporary administrator: %w", ErrUnavailable, err)
	}

	return firstadmin.CheckPrincipal(p, temp)
}

// validPrincipalID reports whether id has the form of the ID of a principal.
func validPrincipalID(id string) bool {
	return ident.Valid(id, maxPrincipalIDLength) && len(id) > len(principal.IDPrefix) &&
		id[:len(principal.IDPrefix)] == principal.IDPrefix
}

// validGrantID reports whether id has the form of the ID of a grant.
func validGrantID(id string) bool {
	return ident.Valid(id, maxGrantIDLength) && len(id) > len(access.GrantIDPrefix) &&
		id[:len(access.GrantIDPrefix)] == access.GrantIDPrefix
}

// entry returns the fixed part of the audit entry of an attempt.
func (s *Service) entry(a attempt) audit.Entry {
	channel := a.request.Channel
	if channel == "" {
		channel = audit.ChannelAPI
	}

	return audit.Entry{
		At: a.now, Namespace: audit.ServerScope, Subject: a.subject, Action: a.action, Object: a.object,
		SourceIP: a.request.SourceIP, UserAgent: audit.TruncateUserAgent(a.request.UserAgent), Channel: channel,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: a.request.RequestID,
		IDPSubject: "", TokenID: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

// refuse writes the failure entry of a refusal. The write is best effort:
// the request is refused already, and a failed write is logged.
func (s *Service) refuse(ctx context.Context, a attempt, rule string) {
	if a.subject == "" {
		a.subject = "unknown"
	}

	e := s.entry(a)
	e.Outcome = audit.OutcomeFailure
	e.Changes = []audit.Change{change(changeRule, rule)}

	// A refusal must be recorded even when the request context ended.
	if _, err := s.cfg.Audit.AppendServer(context.WithoutCancel(ctx), e); err != nil {
		s.cfg.Logger.WarnContext(ctx, "first administrator: the failure entry was not written",
			slog.String("action", a.action), slog.String("rule", rule), slog.String("error", err.Error()))
	}
}

// refuseStore writes the failure entry for an error of the store and maps
// the error fail closed: a domain error passes, and every other error is
// ErrUnavailable.
func (s *Service) refuseStore(ctx context.Context, a attempt, err error) error {
	for _, known := range []struct {
		err  error
		rule string
	}{
		{err: firstadmin.ErrAlreadyMade, rule: RuleAlreadyMade},
		{err: firstadmin.ErrCodeInvalid, rule: RuleCodeInvalid},
		{err: firstadmin.ErrPrincipalInvalid, rule: RulePrincipalNotAllowed},
		{err: firstadmin.ErrGrantExists, rule: RuleGrantExists},
		{err: firstadmin.ErrStaleSignIn, rule: RuleStaleSignIn},
		{err: firstadmin.ErrSessionInvalid, rule: RuleSessionInvalid},
		{err: firstadmin.ErrReasonInvalid, rule: RuleReasonInvalid},
	} {
		if errors.Is(err, known.err) {
			s.refuse(ctx, a, known.rule)

			// The error keeps the sentinel of the failed rule of the principal,
			// so that the API names the rule. The text of each sentinel is fixed.
			if principalRule := principalRuleOf(err); principalRule != nil {
				return fmt.Errorf("%w", principalRule)
			}

			return fmt.Errorf("%w", known.err)
		}
	}

	s.refuse(ctx, a, RuleStoreError)

	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// principalRuleOf returns the sentinel of the rule of CheckPrincipal that err
// wraps, or nil when err wraps none of them.
func principalRuleOf(err error) error {
	for _, rule := range []error{
		firstadmin.ErrPrincipalNotPerson, firstadmin.ErrPrincipalDisabled, firstadmin.ErrPrincipalBreakGlass,
		firstadmin.ErrPrincipalTempAdmin, firstadmin.ErrPrincipalDevSubject, firstadmin.ErrPrincipalBootstrapSubject,
		firstadmin.ErrPrincipalNotLive,
	} {
		if errors.Is(err, rule) {
			return rule
		}
	}

	return nil
}
