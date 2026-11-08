// Package signinsvc is the sign-in service. After a sign-in method returns an
// Assertion, or an error, the service does the same steps in the same order
// for every method, so that no HTTP adapter has its own copy of them:
//
//  1. It checks the Assertion.
//  2. It checks that the provider of the route is the provider of the
//     Assertion, and that the kind of the method is the kind of the provider.
//  3. It finds the principal. The source of a new principal comes from the
//     kind of the method, never from a second value.
//  4. For a method that reports the person as disabled, it disables the
//     principal that exists, and refuses the sign-in.
//  5. It applies the group claims, when a group applier exists.
//  6. It starts the session with the kind of the method, the factors and the
//     time.
//
// An error at a step refuses the sign-in and changes nothing of the later
// steps. Each refusal writes one audit entry with the real reason, the
// provider ID and the kind of the method. The entry never holds a password,
// a ticket, a code or an assertion.
//
// The local password reads its own records and has its own entry,
// CompleteLocal. It checks the Assertion and starts the session with the
// generation that the check of the password read.
package signinsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("sign-in service needs a dependency")

// ErrRemovalAlertUnrecorded reports that the removal alert and the audit
// entry of its failure could not be written, so the sign-in was refused.
var ErrRemovalAlertUnrecorded = errors.New("the removal alert and its audit entry could not be written")

// Reasons of a refused sign-in, in the audit entry. No reason holds a
// password, a ticket, a code, a token, a state or a cookie value.
const (
	ReasonAssertion          = "sign-in assertion is not valid"
	ReasonProviderMismatch   = "assertion names another provider than the route"
	ReasonMethodMismatch     = "assertion method is not the method of the provider"
	ReasonNoSource           = "sign-in method has no principal source"
	ReasonProviderKind       = "sign-in provider has no method kind that the service accepts"
	ReasonNoPrincipal        = "no principal"
	ReasonDisabled           = "principal is disabled"
	ReasonNotTarget          = "principal cannot sign in through an identity provider"
	ReasonTargetLookup       = "temporary administrator read failed"
	ReasonSubject            = "subject is not valid"
	ReasonSubjectLength      = "subject over 255 bytes"
	ReasonOpenIDAnswer       = "ID token was refused"
	ReasonAnswer             = "identity provider answer failed a check"
	ReasonProviderError      = "identity provider answered an error"
	ReasonDirectoryRejected  = "directory refused the sign-in"
	ReasonUnavailable        = "identity provider is not available"
	ReasonReplay             = "sign-in value was used before"
	ReasonBinding            = "callback does not match the sign-in request"
	ReasonInput              = "sign-in input is not valid"
	ReasonUntrustedSource    = "request does not come from a trusted proxy"
	ReasonNoAssertion        = "request carries no sign-in assertion"
	ReasonGroupsMissing      = "sign-in answer has no groups"
	ReasonProviderOff        = "sign-in provider is off"
	ReasonMethodFailed       = "sign-in method failed for a reason that the service does not know"
	ReasonSubjectDisabled    = "identity provider reports the person as disabled"
	ReasonDisableNoPrincipal = "identity provider reports a person with no principal as disabled"
	ReasonDisableNotTarget   = "identity provider reports a principal as disabled that it cannot manage"
	ReasonDisableFailed      = "identity provider reports the person as disabled, and the disable failed"
	ReasonLocalPrincipalGone = "principal of the local user was not found"
	ReasonDisableLastAdmin   = "identity provider reports the last administrator as disabled, and the server keeps the person"
	ReasonGenerationChanged  = "session generation changed during the sign-in"
)

// Answer tells an HTTP adapter which answer a refusal gets.
type Answer int

const (
	// AnswerFailed is the answer of a method or an Assertion that failed:
	// the sign-in failed and the person starts again.
	AnswerFailed Answer = iota + 1
	// AnswerRefused is the answer of a sign-in that the server refuses for
	// a reason about the principal. The reason is safe to show.
	AnswerRefused
)

// RefusedError is the error of a refused sign-in. The service wrote the audit
// entry before it returned the RefusedError. Reason is safe to show: it holds no
// secret.
type RefusedError struct {
	// Reason is the real reason, as the audit entry records it.
	Reason string
	// Answer is the answer that the adapter gives.
	Answer Answer
	cause  error
}

// Error implements error.
func (r *RefusedError) Error() string {
	if r.cause == nil {
		return "sign-in refused: " + r.Reason
	}

	return "sign-in refused: " + r.Reason + ": " + r.cause.Error()
}

// Unwrap returns the error of the method, or nil.
func (r *RefusedError) Unwrap() error { return r.cause }

// Provider is what the service needs to know about the provider of a route.
type Provider struct {
	// ID is the provider ID of the route.
	ID string
	// Kind is the kind of the method of the provider. The Assertion must
	// carry the same kind.
	Kind session.MethodKind
	// AutoCreate makes a principal at the first sign-in of a person.
	AutoCreate bool
	// Stored reports a provider that the API keeps. Its session is created
	// in the same write as a check that the provider is still enabled.
	Stored bool
}

// Request is a sign-in that a method completed. The adapter copies the
// fields. It never passes the HTTP request.
type Request struct {
	// MethodErr is the error of the method, or nil.
	MethodErr error
	// Entry carries the source address, the user agent, the request ID and
	// the channel of the request. The service fills the other fields.
	Entry audit.Entry
	// Provider is the provider of the route.
	Provider Provider
	// Assertion is the Assertion of the method. For ErrSubjectDisabled it
	// holds only the provider ID, the subject and the method.
	Assertion session.Assertion
}

// LocalRequest is a sign-in that the local password completed. The
// Assertion comes from the records of the local user. Generation and
// ChangeRequired come from the same read of the record.
type LocalRequest struct {
	// Entry is as in Request.
	Entry audit.Entry
	// PrincipalID is the principal of the record of the local user. The
	// Assertion must name it.
	PrincipalID string
	// UserID is the ID of the local user. The Assertion must name it as its
	// subject.
	UserID string
	// Assertion is the Assertion of the check of the password.
	Assertion session.Assertion
	// Generation is the session generation that the read of the record
	// returned. The session must carry it.
	Generation uint64
	// ChangeRequired reports that the person must change the password.
	ChangeRequired bool
}

// Result is a started session.
type Result struct {
	// Session is the new session.
	Session session.Session
	// Secret is the secret of the session cookie.
	Secret session.Secret
	// Method is the kind of the method that signed the person in.
	Method session.MethodKind
	// ChangeRequired reports that the person must change the password. Only
	// the local password sets it.
	ChangeRequired bool
	// BreakGlass reports a session of a break-glass account. The adapter
	// limits the cookie of such a session to the break-glass limit.
	BreakGlass bool
}

// Principals finds or makes the principal of a sign-in.
type Principals interface {
	// SignInPrincipal is principal.Service.SignInPrincipal.
	SignInPrincipal(ctx context.Context, e audit.Entry, in principal.SignInInput) (principal.Principal, bool, error)
}

// Disabler disables a principal. It is the record store of package
// principal.
type Disabler interface {
	// DisablePrincipal sets the disable time of the principal id and appends
	// e in the same transaction.
	DisablePrincipal(ctx context.Context, id string, e audit.Entry) error
}

// Generations bumps the session generation of a principal, so that each
// session of the person ends.
type Generations interface {
	// BumpGeneration is session.PrincipalStateStore.BumpGeneration.
	BumpGeneration(
		ctx context.Context, id string, cause session.BumpCause, expected uint64, e audit.Entry,
	) (uint64, error)
}

// Sessions starts sessions and reads the clock.
type Sessions interface {
	// Start is session.Sessions.Start.
	Start(ctx context.Context, e audit.Entry, in session.Start) (session.Session, session.Secret, error)
	// Now returns the time of the clock of the service.
	Now() time.Time
}

// Audit writes the entries of the installation.
type Audit interface {
	// AppendServer writes e in the scope of the installation.
	AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error)
}

// Removals records the members that a group claim sync removed, for the
// removal alert. Nil records nothing.
type Removals interface {
	// Record stores one removal of each group in groupIDs for source, and
	// alerts the administrators when the removals reach a limit. An error
	// does not refuse the sign-in: the service records the failed alert and
	// goes on. It refuses only when that record fails too.
	Record(ctx context.Context, source principal.RemovalSource, groupIDs []string) error
}

// Config holds the ports of a Service. Groups may be nil: the service then
// applies no group claim, and the audit entry of the session says so.
type Config struct {
	Principals  Principals
	Disabler    Disabler
	Generations Generations
	Sessions    Sessions
	Audit       Audit
	// TempAdmins tells the temporary administrators among the principals. The
	// service refuses a sign-in and a disable for one.
	TempAdmins principal.TempAdmins
	// Groups applies the group claims of an Assertion. Nil means that no
	// mapping exists, so no sign-in joins a group.
	Groups principal.GroupClaimApplier
	// Removals records the removals of a group claim sync for the removal
	// alert. Nil records nothing.
	Removals Removals
	// States reads the state of a person for the passkey sign-in. Nil means
	// that the passkey sign-in refuses every request.
	States States
	Logger *slog.Logger
	// OnAuditFailure is called when an audit entry cannot be written. Nil
	// means no call.
	OnAuditFailure func()
}

// Service is the sign-in service.
type Service struct {
	cfg Config
}

// New returns a Service. It returns ErrMissingDependency when cfg lacks a
// required port.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Principals == nil:
		return nil, fmt.Errorf("%w: principals", ErrMissingDependency)
	case cfg.Disabler == nil:
		return nil, fmt.Errorf("%w: disabler", ErrMissingDependency)
	case cfg.Generations == nil:
		return nil, fmt.Errorf("%w: generations", ErrMissingDependency)
	case cfg.Sessions == nil:
		return nil, fmt.Errorf("%w: sessions", ErrMissingDependency)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit log", ErrMissingDependency)
	case cfg.TempAdmins == nil:
		return nil, fmt.Errorf("%w: temporary administrators", ErrMissingDependency)
	case cfg.Logger == nil:
		return nil, fmt.Errorf("%w: logger", ErrMissingDependency)
	}

	return &Service{cfg: cfg}, nil
}

// SourceOf returns the principal source of a person that the first sign-in
// of the method kind makes. It is the only place that derives a source.
// A kind with no source, an empty kind and an unknown kind return false:
// the sign-in is refused. It never defaults to a source.
func SourceOf(kind session.MethodKind) (principal.Source, bool) {
	if kind == session.MethodOIDC {
		return principal.SourceOIDC, true
	}

	if kind == session.MethodOAuth {
		return principal.SourceOAuth, true
	}

	if kind == session.MethodLDAP {
		return principal.SourceLDAP, true
	}

	// SAML, CAS and the trusted header get their source with the first
	// adapter of the method. The local methods make no principal here. An
	// empty or unknown kind has no source.
	return "", false
}

// IsRefusal reports whether err is a *RefusedError.
func IsRefusal(err error) bool {
	var refused *RefusedError

	return errors.As(err, &refused)
}

// Complete finishes a sign-in of a method port: it does the steps of the
// package comment and starts the session. It returns a *RefusedError for a
// sign-in that it refuses, after it wrote the audit entry. It returns an
// error that wraps session.ErrProviderDisabled or session.ErrProviderNotFound
// when a stored provider went off between the lookup and the start, and
// another error when a store fails.
func (s *Service) Complete(ctx context.Context, req *Request) (Result, error) {
	var none Result

	if req.MethodErr != nil && checkProviderKind(&req.Provider) == "" {
		return none, s.methodFailed(ctx, req)
	}

	// Steps 1 and 2.
	source, r := s.admit(ctx, req)
	if r != nil {
		return none, s.refuse(ctx, req, r)
	}

	a := &req.Assertion
	subject := session.SubjectPrefix(req.Provider.ID) + a.Subject

	// Step 3.
	e := entryOf(&req.Entry, subject, a.Subject)

	p, _, err := s.cfg.Principals.SignInPrincipal(ctx, e, principal.SignInInput{
		Subject: subject, Source: source, Names: a.Names, Create: req.Provider.AutoCreate,
	})
	if reason := principalRefusal(err); reason != "" {
		return none, s.refuse(
			ctx,
			req,
			&refusal{reason: reason, answer: AnswerRefused, idpSubject: a.Subject, cause: nil},
		)
	}

	if err != nil {
		return none, fmt.Errorf("find the principal of a sign-in: %w", err)
	}

	// Defense in depth: SignInPrincipal returns only a person of this subject,
	// and refuses a temporary administrator. This check does not rely on it, so
	// that a faulty lookup can never start a session for another person, for a
	// principal of another kind or for a temporary administrator.
	target, targetErr := s.isTarget(ctx, &p, subject)
	if targetErr != nil {
		s.cfg.Logger.WarnContext(ctx, "sign-in refused: the read of the temporary administrators failed",
			slog.String("provider", req.Provider.ID), slog.String("error", targetErr.Error()))

		return none, s.refuse(ctx, req, &refusal{
			reason: ReasonTargetLookup, answer: AnswerFailed, idpSubject: a.Subject, cause: targetErr,
		})
	}

	if !target {
		return none, s.refuse(ctx, req, &refusal{
			reason: ReasonNotTarget, answer: AnswerRefused, idpSubject: a.Subject, cause: nil,
		})
	}

	// Step 5.
	note, err := s.applyGroups(ctx, e, &req.Provider, a, p.ID)
	if err != nil {
		return none, err
	}

	// Step 6.
	e.Changes = startChanges(a)
	if note != "" {
		e.Changes = append(e.Changes, change("groups", note))
	}

	out := s.startSession(ctx, e, &session.Start{
		AuthTime: authTime(a, s.cfg.Sessions.Now()), PrincipalID: p.ID, Provider: req.Provider.ID,
		SourceIP: e.SourceIP, UserAgent: e.UserAgent, Generation: 0, Stored: req.Provider.Stored, Alert: nil,
	})

	return s.started(ctx, req, a.Subject, &out)
}

// CompleteLocal finishes a sign-in of the local password. The Assertion
// comes from the records of the local user, not from a method port. The
// service checks it, checks that it names the person of the record, and
// starts the session with the generation of the record. It returns a
// *RefusedError for a sign-in that it refuses, after it wrote the audit
// entry.
func (s *Service) CompleteLocal(ctx context.Context, req *LocalRequest) (Result, error) {
	var none Result

	a := &req.Assertion
	ref := &Request{
		MethodErr: nil,
		Entry:     req.Entry,
		Assertion: req.Assertion,
		Provider: Provider{
			ID:         session.LocalProviderID,
			Kind:       session.MethodLocalPassword,
			AutoCreate: false,
			Stored:     false,
		},
	}

	// Defense in depth: the service of the local users builds the Assertion
	// from the record of the check. This check stays so that a change of that
	// service cannot start a session from an Assertion that fails the limits
	// of a sign-in.
	if err := session.CheckAssertion(*a, s.cfg.Sessions.Now()); err != nil {
		return none, s.refuse(
			ctx,
			ref,
			&refusal{reason: ReasonAssertion, answer: AnswerFailed, idpSubject: "", cause: err},
		)
	}

	if reason := checkLocal(req); reason != "" {
		return none, s.refuse(ctx, ref, &refusal{reason: reason, answer: AnswerFailed, idpSubject: "", cause: nil})
	}

	e := entryOf(&req.Entry, principal.LocalSubjectPrefix+req.UserID, "")
	e.Changes = startChanges(a)

	if req.ChangeRequired {
		e.Changes = append(e.Changes, change("changeRequired", "true"))
	}

	out := s.startSession(ctx, e, &session.Start{
		AuthTime: a.AuthTime, PrincipalID: a.PrincipalID, Provider: session.LocalProviderID,
		SourceIP: e.SourceIP, UserAgent: e.UserAgent, Generation: req.Generation, Stored: false, Alert: nil,
	})

	if errors.Is(out.err, session.ErrGenerationChanged) {
		// A reset, a password change or a disable after the check of the
		// password changed the generation.
		return none, s.refuse(ctx, ref, &refusal{
			reason: ReasonGenerationChanged, answer: AnswerFailed, idpSubject: "", cause: out.err,
		})
	}

	res, err := s.started(ctx, ref, "", &out)
	res.ChangeRequired = err == nil && req.ChangeRequired

	return res, err
}

// admit does the first two steps of a sign-in of a method port: it checks the
// provider of the route, the Assertion, and the route against the Assertion.
// It returns the principal source of the method kind, or the refusal.
func (s *Service) admit(ctx context.Context, req *Request) (principal.Source, *refusal) {
	if reason := checkProviderKind(&req.Provider); reason != "" {
		return "", &refusal{reason: reason, answer: AnswerFailed, idpSubject: "", cause: nil}
	}

	a := &req.Assertion

	// Step 1.
	if err := session.CheckAssertion(*a, s.cfg.Sessions.Now()); err != nil {
		s.cfg.Logger.WarnContext(ctx, "sign-in refused", slog.String("provider", req.Provider.ID),
			slog.String("error", err.Error()))

		return "", &refusal{reason: ReasonAssertion, answer: AnswerFailed, idpSubject: "", cause: err}
	}

	// Step 2.
	if reason := checkRoute(&req.Provider, a); reason != "" {
		return "", &refusal{reason: reason, answer: AnswerFailed, idpSubject: "", cause: nil}
	}

	source, ok := SourceOf(a.Method)
	if !ok {
		return "", &refusal{reason: ReasonNoSource, answer: AnswerFailed, idpSubject: "", cause: nil}
	}

	// Refuse before the subject reaches the audit entry: the entry keeps the
	// composed subject. The refusal names the provider only.
	if len(session.SubjectPrefix(req.Provider.ID)+a.Subject) > session.MaxAssertionSubject {
		return "", &refusal{reason: ReasonSubjectLength, answer: AnswerRefused, idpSubject: "", cause: nil}
	}

	return source, nil
}

// isTarget reports whether the principal p is the person of the subject that
// an identity provider may sign in or disable: a person of exactly this
// subject, in no namespace, not a break-glass account and not a temporary
// administrator. A failed read of the temporary administrators returns
// false and the error, and the caller fails closed.
func (s *Service) isTarget(ctx context.Context, p *principal.Principal, subject string) (bool, error) {
	if p.Subject != subject || p.Kind != principal.KindPerson || p.Namespace != "" || p.BreakGlass {
		return false, nil
	}

	// Defense in depth: SignInPrincipal refuses a temporary administrator too.
	// This check does not rely on it, so that a faulty lookup can never start
	// a session for, or disable, a temporary administrator.
	temp, err := s.cfg.TempAdmins.IsTempAdmin(ctx, p.ID)
	if err != nil {
		// The read failed: fail closed. The caller refuses, and the refusal
		// names the failed read.
		return false, err
	}

	return !temp, nil
}

// checkLocal returns the reason for which the Assertion of req is not the
// Assertion of the local password of the person of the record, or "".
func checkLocal(req *LocalRequest) string {
	a := &req.Assertion

	switch {
	case a.ProviderID != session.LocalProviderID:
		return ReasonProviderMismatch
	case a.Method != session.MethodLocalPassword:
		return ReasonMethodMismatch
	case req.PrincipalID == "" || a.PrincipalID != req.PrincipalID || a.Subject != req.UserID:
		// The Assertion must name the person of the record.
		return ReasonAssertion
	default:
		return ""
	}
}

// startResult is what the store answered to the start of a session.
type startResult struct {
	err    error
	sess   session.Session
	secret session.Secret
}

// startSession starts the session in with the audit entry e.
func (s *Service) startSession(ctx context.Context, e audit.Entry, in *session.Start) startResult {
	sess, secret, err := s.cfg.Sessions.Start(ctx, e, *in)

	return startResult{err: err, sess: sess, secret: secret}
}

// started turns the result of the start of a session into the Result or the
// refusal.
func (s *Service) started(ctx context.Context, req *Request, idpSubject string, out *startResult) (Result, error) {
	var none Result

	switch {
	case out.err == nil:
		return Result{
			Session: out.sess, Secret: out.secret, Method: req.Assertion.Method, ChangeRequired: false,
			BreakGlass: false,
		}, nil
	case errors.Is(out.err, session.ErrProviderDisabled), errors.Is(out.err, session.ErrProviderNotFound):
		// The provider was disabled or deleted after the lookup of this
		// callback. The adapter answers as for an unknown provider.
		return none, fmt.Errorf("start the session: %w", out.err)
	case errors.Is(out.err, session.ErrPrincipalDisabled):
		return none, s.refuse(ctx, req, &refusal{
			reason: ReasonDisabled, answer: AnswerRefused, idpSubject: idpSubject, cause: nil,
		})
	case errors.Is(out.err, session.ErrPrincipalNotFound):
		reason := ReasonNotTarget
		if req.Provider.ID == session.LocalProviderID {
			// The local password has no identity provider.
			reason = ReasonLocalPrincipalGone
		}

		return none, s.refuse(ctx, req, &refusal{
			reason: reason, answer: AnswerRefused, idpSubject: idpSubject, cause: nil,
		})
	default:
		return none, fmt.Errorf("start the session: %w", out.err)
	}
}

// applyGroups applies the group claims of the Assertion a to the principal
// principalID. It returns a note for the audit entry of the session. With no
// applier, or without groups in the Assertion, it changes no membership. A
// refused add is not an error.
func (s *Service) applyGroups(
	ctx context.Context, e audit.Entry, p *Provider, a *session.Assertion, principalID string,
) (string, error) {
	if !a.GroupsPresent {
		return "", nil
	}

	if s.cfg.Groups == nil {
		return noGroupMapping, nil
	}

	res, err := s.cfg.Groups.ApplyGroupClaims(ctx, e, principal.GroupClaims{
		PrincipalID: principalID, Source: principal.MembershipSourceClaim, SourceID: p.ID, Groups: a.Groups,
		Present: true,
	})
	if err != nil {
		return "", fmt.Errorf("apply the group claims of a sign-in: %w", err)
	}

	removed := slices.Concat(res.Removed, res.Suspended)

	if s.cfg.Removals != nil && len(removed) > 0 {
		source := principal.RemovalSource{Kind: principal.RemovalSourceIdentityProvider, ID: p.ID}

		if err := s.cfg.Removals.Record(ctx, source, removed); err != nil {
			if alertErr := s.removalAlertFailed(ctx, e, source, principalID, err); alertErr != nil {
				return "", alertErr
			}
		}
	}

	if res.NoMapping {
		return noGroupMapping, nil
	}

	return fmt.Sprintf("added %d, removed %d, suspended %d, refused %d, unmapped %d",
		len(res.Added), len(res.Removed), len(res.Suspended), len(res.Refused), len(res.Unmapped)), nil
}

// removalAlertFailed records an alert of the removals that could not be
// written. The sign-in goes on when the entry of its failure is written. It
// returns ErrRemovalAlertUnrecorded when that entry fails too.
func (s *Service) removalAlertFailed(
	ctx context.Context, e audit.Entry, source principal.RemovalSource, principalID string, alertErr error,
) error {
	s.cfg.Logger.ErrorContext(ctx, "the removal alert was not written",
		slog.String("code", errcode.PrincipalRemovalAlertFailed),
		slog.String("source_kind", string(source.Kind)),
		slog.String("source_id", source.ID),
		slog.String("error", alertErr.Error()))

	if err := s.recordRemovalAlertFailure(ctx, e, source, principalID); err != nil {
		s.cfg.Logger.ErrorContext(ctx, "the audit entry of the failed removal alert was not written",
			slog.String("source_kind", string(source.Kind)), slog.String("source_id", source.ID),
			slog.String("error", err.Error()))

		if s.cfg.OnAuditFailure != nil {
			s.cfg.OnAuditFailure()
		}

		return fmt.Errorf("%w: %w", ErrRemovalAlertUnrecorded, err)
	}

	return nil
}

// recordRemovalAlertFailure writes the audit entry of an alert of the
// removals that could not be written.
func (s *Service) recordRemovalAlertFailure(
	ctx context.Context, e audit.Entry, source principal.RemovalSource, principalID string,
) error {
	// Only the request context of the sign-in entry carries over. Its
	// changes and its token do not describe the failed alert.
	entry := audit.Entry{
		At: s.cfg.Sessions.Now(), Namespace: audit.ServerScope, Subject: string(source.Kind) + ":" + source.ID,
		IDPSubject: "", TokenID: "", Action: principal.AuditActionMassRemovalAlert,
		Object: principal.Ref(principalID), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: e.SourceIP, UserAgent: e.UserAgent, Channel: e.Channel,
		Kind: audit.KindSecurity, Outcome: audit.OutcomeFailure, RequestID: e.RequestID, ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "",
		Reason: errcode.PrincipalRemovalAlertFailed,
	}

	_, err := s.cfg.Audit.AppendServer(ctx, entry)

	return err
}

// noGroupMapping is the note that an entry holds for an Assertion with
// groups when no applier exists.
const noGroupMapping = "not applied: no group mapping exists"

// checkProviderKind returns the reason for which the provider of the route
// cannot complete a sign-in, or "" for a provider that can. Defense in depth:
// the adapter builds the provider from its method. This check stays so that
// an adapter that passes an empty ID or a kind that is not a method port
// cannot start a session.
func checkProviderKind(p *Provider) string {
	if !session.ValidProviderID(p.ID) || p.ID == session.LocalProviderID || p.ID == session.DevProviderID {
		return ReasonProviderKind
	}

	// The local methods are not method ports, and an empty or unknown kind is
	// not a method.
	if !slices.Contains(methodPortKinds(), p.Kind) {
		return ReasonProviderKind
	}

	return ""
}

// methodPortKinds returns the kinds of the methods that implement a method
// port.
func methodPortKinds() []session.MethodKind {
	return []session.MethodKind{
		session.MethodOIDC, session.MethodOAuth, session.MethodSAML, session.MethodCAS, session.MethodLDAP,
		session.MethodHeader,
	}
}

// checkRoute returns the reason for which the Assertion a does not belong to
// the provider p of the route, or "". The Assertion must name the provider of
// the route, and the kind of the method of the provider. Defense in depth:
// the method names its own provider ID, and CheckAssertion checks the form of
// the ID. This check stays so that a method of one provider can never sign a
// person in as the subject of another provider.
func checkRoute(p *Provider, a *session.Assertion) string {
	switch {
	case a.ProviderID != p.ID:
		return ReasonProviderMismatch
	case a.Method != p.Kind:
		return ReasonMethodMismatch
	default:
		return ""
	}
}

// authTime returns the time of the authentication of a session of the
// Assertion a. A method that proves no time of the authentication, such as
// plain OAuth 2.0, starts a session whose time of the authentication is now.
func authTime(a *session.Assertion, now time.Time) time.Time {
	if a.AuthTimeKnown {
		return a.AuthTime
	}

	return now
}

// principalRefusal returns the reason for which a sign-in cannot name a
// principal, or "" for a found principal or an error of the store.
func principalRefusal(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, principal.ErrNotFound):
		return ReasonNoPrincipal
	case errors.Is(err, principal.ErrDisabled):
		return ReasonDisabled
	case errors.Is(err, principal.ErrNotSignInTarget):
		return ReasonNotTarget
	case errors.Is(err, principal.ErrInvalid), errors.Is(err, principal.ErrReservedSubject):
		return ReasonSubject
	default:
		return ""
	}
}

// startChanges returns the changes of the audit entry of a session: the kind
// of the method and the factors that the server checked.
func startChanges(a *session.Assertion) []audit.Change {
	changes := []audit.Change{change("method", string(a.Method))}

	if len(a.Factors) > 0 {
		factors := make([]string, len(a.Factors))
		for i, f := range a.Factors {
			factors[i] = string(f)
		}

		changes = append(changes, change("factors", strings.Join(factors, ",")))
	}

	if a.Method == session.MethodPasskey {
		// A passkey sign-in needs user verification, so the passkey is the
		// possession of the key and the verification on the device.
		changes = append(changes, change("factorCount", "2"))
	}

	return changes
}

// change returns the audit change of key with the new value.
func change(key, value string) audit.Change {
	return audit.Change{Key: key, Old: nil, New: value, Layer: "", TakesEffect: ""}
}

// validText reports whether v is UTF-8 with no control character and has min
// to max bytes.
func validText(v string, minLen, maxLen int) bool {
	return len(v) >= minLen && len(v) <= maxLen && utf8.ValidString(v) && !strings.ContainsFunc(v, unicode.IsControl)
}
