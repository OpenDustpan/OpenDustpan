package signinsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// Reasons of a refused passkey sign-in, in the audit entry. No reason holds a
// challenge, a key, a response or the text of a break-glass reason.
const (
	ReasonPasskeyPerson      = "assertion names another person than the passkey"
	ReasonPasskeyNotTarget   = "principal is not a local user or a break-glass account"
	ReasonPasskeyNoState     = "sign-in service has no reader of the principal state"
	ReasonPasskeyNoReason    = "break-glass sign-in has no reason"
	ReasonPasskeyReasonText  = "break-glass reason is not valid text"
	ReasonPasskeyNotVerified = "assertion holds no user verification"
)

// MaxBreakGlassReason is the most bytes of the reason that a person types at
// a break-glass sign-in.
const MaxBreakGlassReason = 500

// ErrReasonRequired is the cause of the refusal of a break-glass sign-in that
// gives no valid reason. The HTTP adapter shows it as its own answer, so that
// the person starts the sign-in again with a reason.
var ErrReasonRequired = errors.New("a break-glass sign-in needs a reason")

// States reads the state of a principal. The record store of the sessions
// meets it.
type States interface {
	// PrincipalState is session.PrincipalStateStore.PrincipalState.
	PrincipalState(ctx context.Context, id string) (session.PrincipalState, error)
}

// PasskeyRequest is a sign-in that a discoverable passkey completed. The
// Assertion comes from the records of the passkey and its person, never from
// the request of the browser.
type PasskeyRequest struct {
	// Entry is as in Request.
	Entry audit.Entry
	// PrincipalID is the person of the passkey that the service of the
	// passkeys verified. The Assertion must name it.
	PrincipalID string
	// Assertion is the Assertion of the check of the passkey.
	Assertion session.Assertion
	// Reason is the text that a person types at a break-glass sign-in. A
	// sign-in of a local user ignores it.
	Reason string
	// UserVerified is true when the check of the passkey saw user
	// verification. A passkey sign-in without it is refused.
	UserVerified bool
}

// CompletePasskey finishes a sign-in of a discoverable passkey. The passkey
// does not use the method port: it makes its Assertion from its own records.
// The service checks the Assertion, reads the state of the person, and
// refuses a person who is not a local user or a break-glass account, who is
// disabled, or who is not a person. It starts the session with the session
// generation of that same read. For a break-glass account it needs a reason
// and writes the alert of the use in the transaction of the session.
//
// It returns a *RefusedError for a sign-in that it refuses, after it wrote
// the audit entry.
func (s *Service) CompletePasskey(ctx context.Context, req *PasskeyRequest) (Result, error) {
	var none Result

	a := &req.Assertion
	ref := &Request{
		MethodErr: nil,
		Entry:     req.Entry,
		Assertion: req.Assertion,
		Provider: Provider{
			ID: session.LocalProviderID, Kind: session.MethodPasskey, AutoCreate: false, Stored: false,
		},
	}

	// Defense in depth: the HTTP adapter builds the Assertion from the passkey
	// that the service of the passkeys verified. This check stays so that a
	// change of the adapter cannot start a session from an Assertion that
	// fails the limits of a sign-in.
	if err := session.CheckAssertion(*a, s.cfg.Sessions.Now()); err != nil {
		return none, s.refuse(
			ctx, ref, &refusal{reason: ReasonAssertion, answer: AnswerFailed, idpSubject: "", cause: err},
		)
	}

	if reason := checkPasskey(req); reason != "" {
		return none, s.refuse(ctx, ref, &refusal{reason: reason, answer: AnswerFailed, idpSubject: "", cause: nil})
	}

	if s.cfg.States == nil {
		return none, s.refuse(ctx, ref, &refusal{
			reason: ReasonPasskeyNoState, answer: AnswerFailed, idpSubject: "", cause: nil,
		})
	}

	state, err := s.cfg.States.PrincipalState(ctx, req.PrincipalID)

	switch {
	case errors.Is(err, session.ErrPrincipalNotFound):
		return none, s.refuse(ctx, ref, &refusal{
			reason: ReasonLocalPrincipalGone, answer: AnswerRefused, idpSubject: "", cause: nil,
		})
	case err != nil:
		return none, fmt.Errorf("read the state of the person of a passkey: %w", err)
	}

	if reason := passkeyTargetRefusal(&state.Owner, req.PrincipalID); reason != "" {
		return none, s.refuse(ctx, ref, &refusal{reason: reason, answer: AnswerRefused, idpSubject: "", cause: nil})
	}

	e := entryOf(&req.Entry, state.Owner.Subject, "")
	e.Changes = startChanges(a)

	start := &session.Start{
		AuthTime: a.AuthTime, PrincipalID: req.PrincipalID, Provider: session.LocalProviderID,
		SourceIP: e.SourceIP, UserAgent: e.UserAgent, Generation: state.Generation, Stored: false, Alert: nil,
	}

	if state.Owner.BreakGlass {
		if refused := s.breakGlassStart(ctx, ref, req, start, &e); refused != nil {
			return none, refused
		}
	}

	out := s.startSession(ctx, e, start)

	if errors.Is(out.err, session.ErrGenerationChanged) {
		// A reset, a change of the credentials or a disable after the read of
		// the state changed the generation.
		return none, s.refuse(ctx, ref, &refusal{
			reason: ReasonGenerationChanged, answer: AnswerFailed, idpSubject: "", cause: out.err,
		})
	}

	res, err := s.started(ctx, ref, "", &out)
	res.BreakGlass = err == nil && state.Owner.BreakGlass

	return res, err
}

// breakGlassStart checks the reason of a break-glass sign-in and puts the
// alert of the use on start. It adds the reason and the kind to the audit
// entry e. It returns the refusal when the reason is missing or is not
// valid text.
func (s *Service) breakGlassStart(
	ctx context.Context, ref *Request, req *PasskeyRequest, start *session.Start, e *audit.Entry,
) error {
	reason := strings.TrimSpace(req.Reason)

	switch {
	case reason == "":
		return s.refuse(ctx, ref, &refusal{
			reason: ReasonPasskeyNoReason, answer: AnswerRefused, idpSubject: "", cause: ErrReasonRequired,
		})
	case !validText(reason, 1, MaxBreakGlassReason) || strings.ContainsFunc(reason, audit.IsHiddenRune):
		// The reason goes to the audit entry and to the alert that the
		// administrators read. A control, format, line separator or paragraph
		// separator character (for example U+202E or U+200B) could show text
		// that differs from the stored text, so the set of the audit log
		// refuses them.
		return s.refuse(ctx, ref, &refusal{
			reason: ReasonPasskeyReasonText, answer: AnswerRefused, idpSubject: "", cause: ErrReasonRequired,
		})
	}

	at := s.cfg.Sessions.Now()
	e.Reason = reason
	e.Changes = append(e.Changes, change("breakGlass", "true"))

	start.Alert = &notify.Notification{
		ID: ident.NewID(notify.IDPrefix), Type: notify.BreakGlassUsed, Scope: audit.ServerScope,
		Object: notify.NotificationsObject(audit.ServerScope), ObjectID: "", Subject: history.ServerSubject, Agent: "",
		Severity: notify.SeverityCritical,
		Summary:  "A break-glass account signed in with a passkey: " + reason,
		Links:    nil, Attributes: map[string]string{"method": string(session.MethodPasskey)},
		Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}},
		DedupeKey:  "", At: at, Sequence: 0,
	}

	return nil
}

// checkPasskey returns the reason for which the Assertion of req is not the
// Assertion of a passkey sign-in of the person of the passkey, or "".
func checkPasskey(req *PasskeyRequest) string {
	a := &req.Assertion

	switch {
	case a.ProviderID != session.LocalProviderID:
		return ReasonProviderMismatch
	case a.Method != session.MethodPasskey:
		return ReasonMethodMismatch
	case req.PrincipalID == "" || a.PrincipalID != req.PrincipalID || a.Subject != req.PrincipalID:
		// The Assertion must name the person of the passkey.
		return ReasonPasskeyPerson
	case !req.UserVerified:
		// Defense in depth: the service of the passkeys refuses a sign-in with
		// no user verification. This check stays so that a passkey that can
		// sign in alone always had it, and a sign-in counts as two factors.
		return ReasonPasskeyNotVerified
	default:
		return ""
	}
}

// passkeyTargetRefusal returns the reason for which the owner o of a session
// cannot sign in with a passkey, or "". The owner is a person that is not
// disabled, and is a local user or a break-glass account. Defense in depth:
// the service of the passkeys reads the passkey by its credential, and the
// state read checks that the principal is a person. These checks do not rely
// on either of them.
func passkeyTargetRefusal(o *session.Owner, id string) string {
	switch {
	case o.ID != id || o.Kind != principal.KindPerson:
		return ReasonPasskeyNotTarget
	case !o.DisabledAt.IsZero():
		return ReasonDisabled
	case o.BreakGlass:
		return ""
	case !strings.HasPrefix(o.Subject, principal.LocalSubjectPrefix):
		return ReasonPasskeyNotTarget
	default:
		return ""
	}
}
