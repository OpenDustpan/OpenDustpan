package signinsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// refusal describes a refused sign-in: the reason for the audit entry, the
// answer for the adapter, the subject at the identity provider when it
// passed the checks of the Assertion and is safe for the entry (else empty),
// and the error that caused the refusal, or nil.
type refusal struct {
	cause      error
	reason     string
	idpSubject string
	answer     Answer
}

// entryOf returns the audit entry of a sign-in of the person subject: the
// entry of the caller with the subject and the subject at the identity
// provider.
func entryOf(base *audit.Entry, subject, idpSubject string) audit.Entry {
	e := *base
	e.Subject = subject
	e.IDPSubject = idpSubject
	e.Action = session.ActionSignIn

	return e
}

// refuse writes the audit entry of the refused sign-in r and returns the
// *RefusedError.
func (s *Service) refuse(ctx context.Context, req *Request, r *refusal) error {
	s.record(ctx, req, r)

	return &RefusedError{Reason: r.reason, Answer: r.answer, cause: r.cause}
}

// record writes the audit entry of the refused sign-in r. The entry holds the
// reason, the provider ID and the kind of the method. A failed write is
// logged and counted, and never lets the sign-in through.
func (s *Service) record(ctx context.Context, req *Request, r *refusal) {
	subject := "sign-in:" + req.Provider.ID
	if r.idpSubject != "" && req.Provider.ID != session.LocalProviderID {
		subject = session.SubjectPrefix(req.Provider.ID) + r.idpSubject
	}

	e := entryOf(&req.Entry, subject, r.idpSubject)
	e.At = s.cfg.Sessions.Now()
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeFailure

	if r.answer == AnswerRefused {
		// The server refuses the person for a reason about the principal: the
		// sign-in is denied, not failed.
		e.Outcome = audit.OutcomeDenied
	}

	e.Object = "sign-in-provider:" + req.Provider.ID
	e.Changes = []audit.Change{
		change("reason", r.reason), change("provider", req.Provider.ID), change("method", string(req.Provider.Kind)),
	}

	if _, err := s.cfg.Audit.AppendServer(ctx, e); err != nil {
		s.cfg.Logger.ErrorContext(ctx, "the audit entry of a refused sign-in was not written",
			slog.String("provider", req.Provider.ID), slog.String("error", err.Error()))

		if s.cfg.OnAuditFailure != nil {
			s.cfg.OnAuditFailure()
		}
	}
}

// methodFailed handles the error of a method: it writes the audit entry with
// the reason of the error class, and for ErrSubjectDisabled it disables the
// principal first. It always returns a *RefusedError, or the error of the
// store when the disable failed.
func (s *Service) methodFailed(ctx context.Context, req *Request) error {
	err := req.MethodErr

	s.cfg.Logger.WarnContext(ctx, "sign-in refused", slog.String("provider", req.Provider.ID),
		slog.String("method", string(req.Provider.Kind)), slog.String("error", err.Error()))

	if errors.Is(err, session.ErrSubjectDisabled) {
		return s.subjectDisabled(ctx, req)
	}

	return s.refuse(ctx, req, &refusal{
		reason: methodReason(req.Provider.Kind, err), answer: AnswerFailed, idpSubject: "", cause: err,
	})
}

// methodReason returns the audit reason for the error err of a method of the
// kind kind. The most specific class comes first. An error of no known class
// has its own reason, so a new error never reads as another one.
func methodReason(kind session.MethodKind, err error) string {
	switch {
	case errors.Is(err, session.ErrReplay):
		return ReasonReplay
	case errors.Is(err, session.ErrRequestBinding):
		return ReasonBinding
	case errors.Is(err, session.ErrProviderDisabled):
		return ReasonProviderOff
	case errors.Is(err, session.ErrMethodUnavailable):
		return ReasonUnavailable
	case errors.Is(err, session.ErrMethodRejected):
		if kind == session.MethodLDAP {
			return ReasonDirectoryRejected
		}

		return ReasonProviderError
	default:
		return answerReason(kind, err)
	}
}

// answerReason is methodReason for the classes about the answer of the
// provider and the input of the method.
func answerReason(kind session.MethodKind, err error) string {
	switch {
	case errors.Is(err, session.ErrMethodAnswer):
		// The ID token exists in OpenID Connect only. A plain OAuth 2.0
		// provider has none, so its refusal never says that an ID token was
		// refused.
		if kind == session.MethodOIDC && errors.Is(err, oauth.ErrIDToken) {
			return ReasonOpenIDAnswer
		}

		return ReasonAnswer
	case errors.Is(err, session.ErrInvalidAssertion):
		return ReasonAssertion
	case errors.Is(err, session.ErrMethodInput):
		return ReasonInput
	case errors.Is(err, session.ErrUntrustedSource):
		return ReasonUntrustedSource
	case errors.Is(err, session.ErrNoAssertion):
		return ReasonNoAssertion
	case errors.Is(err, session.ErrGroupsMissing):
		return ReasonGroupsMissing
	default:
		return ReasonMethodFailed
	}
}

// checkDisabledAssertion returns the reason for which the Assertion of
// ErrSubjectDisabled cannot name a person to disable, or "". Defense in
// depth: the method returns only the provider ID, the subject and the kind.
// These checks stay so that a method of one provider can never disable a
// person of another provider, and so that a value that fails the limits
// never reaches the store.
func checkDisabledAssertion(req *Request) string {
	a := &req.Assertion

	switch {
	case a.ProviderID != req.Provider.ID:
		return ReasonProviderMismatch
	case a.Method != req.Provider.Kind:
		return ReasonMethodMismatch
	case a.PrincipalID != "" || !validText(a.Subject, 1, session.MaxAssertionSubject):
		return ReasonAssertion
	case len(session.SubjectPrefix(req.Provider.ID)+a.Subject) > session.MaxAssertionSubject:
		return ReasonSubjectLength
	default:
		return ""
	}
}

// subjectDisabled does the steps of a method that reports the person as
// disabled. It never makes a principal. It never disables a principal that
// the identity provider cannot manage, such as a break-glass account. It
// always refuses the sign-in.
func (s *Service) subjectDisabled(ctx context.Context, req *Request) error {
	cause := req.MethodErr
	a := &req.Assertion

	if reason := checkDisabledAssertion(req); reason != "" {
		return s.refuse(ctx, req, &refusal{reason: reason, answer: AnswerFailed, idpSubject: "", cause: cause})
	}

	subject := session.SubjectPrefix(req.Provider.ID) + a.Subject
	source, _ := SourceOf(a.Method)
	e := entryOf(&req.Entry, subject, a.Subject)

	// Create is false: the service never makes a principal only to disable it.
	p, _, err := s.cfg.Principals.SignInPrincipal(ctx, e, principal.SignInInput{
		Subject: subject, Source: source, Names: nil, Create: false,
	})
	if err != nil {
		return s.disableRefusal(ctx, req, err)
	}

	// Defense in depth: SignInPrincipal refuses a break-glass account, a
	// principal of a namespace, a principal that is not a person and a
	// temporary administrator, and the store refuses the last administrator.
	// This check does not rely on any of them: the raw store disables any
	// principal that it gets, so a faulty lookup must never reach it with a
	// principal of another subject or kind, or with a temporary administrator.
	target, targetErr := s.isTarget(ctx, &p, subject)
	if targetErr != nil {
		s.cfg.Logger.WarnContext(ctx, "disable refused: the read of the temporary administrators failed",
			slog.String("provider", req.Provider.ID), slog.String("error", targetErr.Error()))

		return s.refuse(ctx, req, &refusal{
			reason: ReasonTargetLookup, answer: AnswerFailed, idpSubject: a.Subject,
			cause: errors.Join(cause, targetErr),
		})
	}

	if !target {
		return s.refuse(ctx, req, &refusal{
			reason: ReasonDisableNotTarget, answer: AnswerFailed, idpSubject: a.Subject, cause: cause,
		})
	}

	if disableErr := s.disable(ctx, e, &p); disableErr != nil {
		if errors.Is(disableErr, principal.ErrLastBreakGlass) ||
			errors.Is(disableErr, access.ErrLastSiteAdministrator) {
			// The server keeps the last administrator. The sign-in is refused
			// as every other refusal is.
			return s.refuse(ctx, req, &refusal{
				reason: ReasonDisableLastAdmin, answer: AnswerFailed, idpSubject: a.Subject, cause: cause,
			})
		}

		s.record(ctx, req, &refusal{
			reason: ReasonDisableFailed, answer: AnswerFailed, idpSubject: a.Subject, cause: cause,
		})

		return disableErr
	}

	return s.refuse(ctx, req, &refusal{
		reason: ReasonSubjectDisabled, answer: AnswerFailed, idpSubject: a.Subject, cause: cause,
	})
}

// disableRefusal turns the answer err of the principal service, for a person
// that a provider reports as disabled, into the refusal. Nothing is disabled.
func (s *Service) disableRefusal(ctx context.Context, req *Request, err error) error {
	var reason string

	switch {
	case errors.Is(err, principal.ErrNotFound):
		reason = ReasonDisableNoPrincipal
	case errors.Is(err, principal.ErrNotSignInTarget), errors.Is(err, principal.ErrInvalid),
		errors.Is(err, principal.ErrReservedSubject):
		// Never disable: a break-glass account, a temporary administrator or a
		// principal of another kind is not for an identity provider to
		// disable.
		reason = ReasonDisableNotTarget
	case errors.Is(err, principal.ErrDisabled):
		reason = ReasonSubjectDisabled
	default:
		return fmt.Errorf("find the principal to disable: %w", err)
	}

	return s.refuse(ctx, req, &refusal{
		reason: reason, answer: AnswerFailed, idpSubject: req.Assertion.Subject, cause: req.MethodErr,
	})
}

// disable disables the principal p and bumps its session generation, so that
// each session of the person ends. Each write has its own audit entry.
func (s *Service) disable(ctx context.Context, e audit.Entry, p *principal.Principal) error {
	e.At = s.cfg.Sessions.Now()
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Object = principal.Ref(p.ID)
	e.Changes = []audit.Change{change("reason", ReasonSubjectDisabled)}

	if err := s.cfg.Disabler.DisablePrincipal(ctx, p.ID, e); err != nil {
		return fmt.Errorf("disable principal %q: %w", p.ID, err)
	}

	if _, err := s.cfg.Generations.BumpGeneration(ctx, p.ID, session.BumpDirectory, 0, e); err != nil {
		return fmt.Errorf("end the sessions of principal %q: %w", p.ID, err)
	}

	return nil
}
