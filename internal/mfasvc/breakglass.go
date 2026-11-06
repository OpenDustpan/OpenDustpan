package mfasvc

import (
	"context"
	"errors"
	"log/slog"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/principal"
)

// PrincipalReader reads a principal record. The record store meets it.
type PrincipalReader interface {
	// GetPrincipal returns the principal id, or principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}

// breakGlassWatch counts the failed assertions for the passkeys of
// break-glass accounts. It raises an alert and locks nothing.
type breakGlassWatch struct {
	principals PrincipalReader
	counter    authgate.NameLockCounter
}

// WatchBreakGlass makes the service count each failed assertion for the
// passkey of a break-glass account. At each authgate.BreakGlassAlertLimit
// failures the counter appends an alert to the site administrators, at most
// once for each account in each hour, in the transaction of the count. The
// count is an alert and never a lock: no sign-in reads it, so a failed
// assertion for a credential cannot lock out the owner. Without a watch, the
// service counts nothing.
func (s *Service) WatchBreakGlass(principals PrincipalReader, counter authgate.NameLockCounter) {
	s.watch = &breakGlassWatch{principals: principals, counter: counter}
}

// noteFailure counts a failed assertion for the passkey key when the person of
// the key is a break-glass account. A failure of the count is logged and never
// changes the answer of the sign-in, which is refused already.
func (s *Service) noteFailure(ctx context.Context, base audit.Entry, key *mfa.Passkey) {
	if s.watch == nil {
		return
	}

	p, err := s.watch.principals.GetPrincipal(ctx, key.PersonID)
	if err != nil {
		s.log.WarnContext(ctx, "the person of a failed assertion cannot be read", slog.String("error", err.Error()))

		return
	}

	if !p.BreakGlass {
		return
	}

	object := principal.Ref(p.ID)

	_, err = s.watch.counter.AddNameFailure(ctx, authgate.NameFailure{
		Events: s.breakGlassEvents(base, object), Scope: authgate.ScopeBreakGlass, Name: p.ID, Sub: "",
		Rule: authgate.BreakGlassRule(),
	})
	if err != nil && !errors.Is(err, authgate.ErrNameLocked) {
		s.log.ErrorContext(ctx, "the failed assertion for a break-glass account was not counted",
			slog.String("error", err.Error()))
	}
}

// breakGlassEvents returns the records of the count of one account: the entry
// of the alert, the entry of its end, and the alert to the administrators.
func (s *Service) breakGlassEvents(base audit.Entry, object string) *authgate.LockEvents {
	at := s.now()

	entry := func(action string) audit.Entry {
		e := s.entry(base, action, object, audit.KindSecurity, audit.OutcomeDenied)
		e.Reason = "failed passkey assertions for a break-glass account"

		return e
	}

	return &authgate.LockEvents{
		Locked: entry(authgate.ActionNameLocked),
		Ended:  entry(authgate.ActionNameLockEnded),
		Alert: notify.Notification{
			ID: ident.NewID(notify.IDPrefix), Type: notify.SignInLocked, Scope: audit.ServerScope,
			Object: notify.NotificationsObject(audit.ServerScope), ObjectID: "", Subject: history.ServerSubject,
			Agent: "", Severity: notify.SeverityWarning, Summary: "", Links: nil, Attributes: nil,
			Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}},
			DedupeKey:  "", At: at, Sequence: 0,
		},
	}
}
