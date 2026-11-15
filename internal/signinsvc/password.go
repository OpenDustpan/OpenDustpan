package signinsvc

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/session"
)

// Reasons of a refused sign-in of a password method.
const (
	ReasonNameLocked       = "user name locked after failed attempts"
	ReasonSubjectLocked    = "person locked after failed attempts"
	ReasonCountFailed      = "count of the failure cannot be written"
	ReasonLockoutOff       = "password sign-in has no lockout store"
	ReasonGateSkipped      = "password method did not check the person before the bind"
	ReasonGateSubject      = "password method checked another person than the one it signed in"
	ReasonPasswordMismatch = "password method is not the method of the provider"
)

// ErrSubjectLocked is the error of the bind gate while the person is
// locked. The method wraps it, and the service refuses the sign-in with the
// answer of a wrong password.
var ErrSubjectLocked = errors.New("person locked after failed attempts")

// errShortKey is the error of a key of the counts with the wrong length.
var errShortKey = errors.New("the key of the counts has the wrong length")

// errGateTwice is the error of a second call of the bind gate with another
// subject in one sign-in.
var errGateTwice = errors.New("bind gate called again with another subject")

// Kinds of the key of a count of a password method.
const (
	keyKindName    = "name"
	keyKindSubject = "subject"
	// keyKindUnknown is the kind of the count of a sign-in whose search found
	// no person.
	keyKindUnknown = "unknown-subject"
)

// lockKeyLength is the length of the key of the digests of the counts.
const lockKeyLength = 32

// unknownSource is the sub-key of a request whose source address does not
// parse. Such requests share one count, which is the safe direction.
const unknownSource = "unknown"

// ipv6Prefix is the length of the source prefix of a count of an IPv6
// address, in bits. An IPv4 address counts whole. They are the lengths of the
// local password.
const ipv6Prefix = 64

// NameKeys reads the key of the digests of the names in the lock counts. Every
// instance on one database reads the same key. Both SQL adapters meet it.
type NameKeys interface {
	// NameLockKey returns the key: 32 bytes.
	NameLockKey(ctx context.Context) ([]byte, error)
}

// PasswordRequest is a sign-in of a password method: a user name and a
// password that a directory checks. The adapter copies the fields. It never
// passes the HTTP request.
type PasswordRequest struct {
	// Entry is as in Request.
	Entry audit.Entry
	// Provider is the provider of the route.
	Provider Provider
	// Method is the method of the provider.
	Method session.PasswordMethod
	// UserName is the user name that the person typed.
	UserName string
	// Password is the password that the person typed.
	Password session.Password
}

// lockKeys are the keys of the counts of one key of an attempt: the digest
// and the source prefix.
type lockKeys struct {
	name string
	sub  string
}

// CompletePassword finishes a sign-in of a password method. It counts each
// attempt on two keys, as the lockout of the local password does, in the
// scopes authgate.ScopePassword (with the source prefix) and
// authgate.ScopePasswordName:
//
//   - the typed name, folded, before it calls Verify, so that a guess against
//     many names counts, also against names that do not exist;
//   - the subject that the search of the method found, in the bind gate,
//     before the method sends the password, so that each form of one user name
//     counts on one person.
//
// A lock on either key stops the sign-in before a password reaches the
// directory. The name of a count is the keyed digest of the provider ID, the
// kind of the key and the value. The service clears the counts of both keys
// only after the session started. It then does the steps of Complete.
//
// It returns a *RefusedError for a sign-in that it refuses, after it wrote the
// audit entry. A locked name, a locked person, an unknown user and a wrong
// password all get AnswerFailed.
func (s *Service) CompletePassword(ctx context.Context, req *PasswordRequest) (Result, error) {
	var none Result

	var noAssertion session.Assertion

	ref := &Request{MethodErr: nil, Entry: req.Entry, Provider: req.Provider, Assertion: noAssertion}

	if reason := s.checkPasswordRequest(req); reason != "" {
		return none, s.refuse(ctx, ref, &refusal{reason: reason, answer: AnswerFailed, idpSubject: "", cause: nil})
	}

	name, err := s.countKey(ctx, req, keyKindName, localuser.FoldUserName(req.UserName))
	if err != nil {
		return none, s.countRefusal(ctx, ref, ReasonNameLocked, err)
	}

	gate := &bindGate{
		svc: s, req: req, mu: sync.Mutex{}, subject: "", keys: lockKeys{name: "", sub: ""}, err: nil, called: false,
	}

	a, methodErr := req.Method.Verify(ctx, session.Credentials{
		ProviderID: req.Provider.ID, UserName: req.UserName, Password: req.Password,
	}, gate)

	if gateErr := s.checkGate(ctx, ref, gate, methodErr); gateErr != nil {
		return none, gateErr
	}

	if countErr := s.countUnknownPerson(ctx, req, gate, methodErr); countErr != nil {
		return none, s.countRefusal(ctx, ref, ReasonSubjectLocked, countErr)
	}

	if methodErr == nil {
		if reason := gate.checkAssertion(&a); reason != "" {
			return none, s.refuse(ctx, ref, &refusal{reason: reason, answer: AnswerFailed, idpSubject: "", cause: nil})
		}
	}

	res, err := s.Complete(ctx, &Request{MethodErr: methodErr, Entry: req.Entry, Provider: req.Provider, Assertion: a})
	if err != nil {
		return none, err
	}

	s.clearCounts(ctx, req.Provider.ID, name, gate.lockKeys())

	return res, nil
}

// checkGate refuses the sign-in when the bind gate refused the bind, whatever
// the method returned. A locked person gets the answer of a wrong password.
// Defense in depth: a method stops at the refusal of the gate and returns its
// error, and the service also checks that error. The check of the gate stays
// so that a method that ignores the refusal and binds anyway never signs a
// locked person in.
func (s *Service) checkGate(ctx context.Context, ref *Request, gate *bindGate, methodErr error) error {
	gateErr := gate.failed()

	switch {
	case errors.Is(gateErr, ErrSubjectLocked), errors.Is(methodErr, ErrSubjectLocked),
		errors.Is(methodErr, authgate.ErrNameLocked):
		return s.refuse(ctx, ref, &refusal{
			reason: ReasonSubjectLocked, answer: AnswerFailed, idpSubject: "", cause: session.ErrMethodRejected,
		})
	case errors.Is(gateErr, errGateTwice):
		return s.refuse(ctx, ref, &refusal{reason: ReasonGateSubject, answer: AnswerFailed, idpSubject: "", cause: nil})
	case gateErr != nil:
		return s.countRefusal(ctx, ref, ReasonSubjectLocked, gateErr)
	default:
		return nil
	}
}

// countUnknownPerson adds one failure to the counts of a random value when the
// method rejected the sign-in and never called the gate: the search found no
// person. So a sign-in of an unknown user writes the counts that a sign-in of
// a known person writes in the gate, and the cost in the store does not tell
// that a user exists. The value is random, so the count never locks and
// never writes the records of a lock. The scheduled prune of the counts
// deletes it after the prune age. It returns the error of a count that cannot
// be written.
func (s *Service) countUnknownPerson(
	ctx context.Context, req *PasswordRequest, gate *bindGate, methodErr error,
) error {
	if gate.wasCalled() || !errors.Is(methodErr, session.ErrMethodRejected) {
		return nil
	}

	_, err := s.countKey(ctx, req, keyKindUnknown, rand.Text())
	if err != nil && !errors.Is(err, authgate.ErrNameLocked) {
		return err
	}

	return nil
}

// checkPasswordRequest returns the reason for which req cannot reach a
// method, or "".
func (s *Service) checkPasswordRequest(req *PasswordRequest) string {
	switch {
	case s.cfg.Locks == nil || s.cfg.NameKeys == nil:
		// Fail closed: without the counts a password method has no lockout,
		// so a guess against a directory would have no limit.
		return ReasonLockoutOff
	case checkProviderKind(&req.Provider) != "" || req.Provider.Kind != session.MethodLDAP:
		return ReasonProviderKind
	case req.Method == nil || req.Method.Kind() != req.Provider.Kind:
		return ReasonPasswordMismatch
	case !validTypedName(req.UserName) || req.Password.Reveal() == "":
		// Defense in depth: the method checks the user name and refuses an
		// empty password before any bind. This check stays so that no count
		// and no method call runs for input that no person can type.
		return ReasonInput
	default:
		return ""
	}
}

// validTypedName reports whether name has 1 to session.MaxUserName bytes of
// UTF-8 and no control character.
func validTypedName(name string) bool {
	return name != "" && len(name) <= session.MaxUserName && utf8.ValidString(name) &&
		!strings.ContainsFunc(name, unicode.IsControl)
}

// countRefusal refuses the sign-in for a count that locked or that could not
// be written. A lock gets the answer of a wrong password.
func (s *Service) countRefusal(ctx context.Context, ref *Request, lockedReason string, err error) error {
	if errors.Is(err, authgate.ErrNameLocked) {
		return s.refuse(ctx, ref, &refusal{
			reason: lockedReason, answer: AnswerFailed, idpSubject: "", cause: session.ErrMethodRejected,
		})
	}

	return s.refuse(ctx, ref, &refusal{
		reason: ReasonCountFailed, answer: AnswerFailed, idpSubject: "",
		cause: fmt.Errorf("%w: %w", session.ErrMethodUnavailable, err),
	})
}

// countKey adds one failure to the counts of the key kind and value of req,
// and returns the keys of the counts. It returns an error that wraps
// authgate.ErrNameLocked while a lock holds.
func (s *Service) countKey(ctx context.Context, req *PasswordRequest, kind, value string) (lockKeys, error) {
	digest, err := s.lockDigest(ctx, req.Provider.ID, kind, value)
	if err != nil {
		return lockKeys{name: "", sub: ""}, err
	}

	keys := lockKeys{name: digest, sub: sourcePrefix(req.Entry.SourceIP)}
	rules := []struct {
		rule  authgate.LockRule
		scope string
		sub   string
	}{
		{scope: authgate.ScopePassword, sub: keys.sub, rule: authgate.PasswordRule()},
		{scope: authgate.ScopePasswordName, sub: "", rule: authgate.PasswordNameRule()},
	}

	for _, r := range rules {
		_, addErr := s.cfg.Locks.AddNameFailure(ctx, authgate.NameFailure{
			Events: s.lockEvents(req), Scope: r.scope, Name: keys.name, Sub: r.sub, Rule: r.rule,
		})
		if addErr != nil {
			return keys, fmt.Errorf("count the %s of the attempt: %w", kind, addErr)
		}
	}

	return keys, nil
}

// lockDigest returns the name of a count: an HMAC-SHA-256 under the key of
// the installation of the provider ID, the kind of the key and the value, in
// hexadecimal. So the counts of one user name at two providers stay apart, the
// table of the counts holds no user name, and an unknown name counts as a
// known one does.
func (s *Service) lockDigest(ctx context.Context, providerID, kind, value string) (string, error) {
	key, err := s.cfg.NameKeys.NameLockKey(ctx)
	if err != nil {
		return "", fmt.Errorf("read the key of the counts: %w", err)
	}

	// Defense in depth: the store checks the length of the key too. This
	// check stays so that a store of another version never gives a short key.
	if len(key) != lockKeyLength {
		return "", fmt.Errorf("%w: %d bytes, want %d", errShortKey, len(key), lockKeyLength)
	}

	mac := hmac.New(sha256.New, key)

	for _, part := range []string{providerID, kind, value} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}

	return hex.EncodeToString(mac.Sum(nil)), nil
}

// lockEvents returns the records of one add: the entries of a new lock and of
// the end of a lock, and the alert of a new lock to the administrators.
func (s *Service) lockEvents(req *PasswordRequest) *authgate.LockEvents {
	at := s.cfg.Sessions.Now()

	base := req.Entry
	base.Subject = "sign-in:" + req.Provider.ID
	base.At = at
	base.Namespace = audit.ServerScope
	base.Kind = audit.KindChange
	base.Outcome = audit.OutcomeSuccess
	base.Object = "sign-in-provider:" + req.Provider.ID

	locked, ended := base, base
	locked.Action, ended.Action = authgate.ActionNameLocked, authgate.ActionNameLockEnded

	return &authgate.LockEvents{
		Locked: locked,
		Ended:  ended,
		Alert: notify.Notification{
			ID: ident.NewID(notify.IDPrefix), Type: notify.SignInLocked, Scope: audit.ServerScope,
			Object: notify.NotificationsObject(audit.ServerScope), ObjectID: "", Subject: history.ServerSubject,
			Agent: "", Severity: notify.SeverityWarning, Summary: "", Links: nil, Attributes: nil,
			Recipients: []notify.Recipient{{Principal: "", Group: "", Admins: true}},
			DedupeKey:  "", At: at, Sequence: 0,
		},
	}
}

// clearCounts clears the counts of both keys of a sign-in that started a
// session. A failed clear leaves the counts, which is the safe direction, and
// is logged.
func (s *Service) clearCounts(ctx context.Context, providerID string, keys ...lockKeys) {
	for _, k := range keys {
		if k.name == "" {
			continue
		}

		for _, c := range []struct{ scope, sub string }{
			{scope: authgate.ScopePassword, sub: k.sub}, {scope: authgate.ScopePasswordName, sub: ""},
		} {
			if err := s.cfg.Locks.ClearNameFailures(ctx, c.scope, k.name, c.sub); err != nil {
				s.cfg.Logger.WarnContext(ctx, "the counts of a sign-in were not cleared",
					slog.String("provider", providerID), slog.String("scope", c.scope),
					slog.String("error", err.Error()))
			}
		}
	}
}

// sourcePrefix returns the sub-key of a count for the source address addr:
// the IPv4 address, or the /64 of an IPv6 address. An address that does not
// parse returns one shared sub-key.
func sourcePrefix(addr string) string {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return unknownSource
	}

	ip = ip.Unmap()
	if ip.Is4() {
		return ip.String()
	}

	prefix, err := ip.WithZone("").Prefix(ipv6Prefix)
	if err != nil {
		return unknownSource
	}

	return prefix.String()
}

// bindGate is the session.BindGate of one password sign-in. It counts the
// subject that the search found, and stops the bind while the person is
// locked. It keeps the subject, so the service can check that the Assertion
// names the person whom the gate counted.
type bindGate struct {
	svc     *Service
	req     *PasswordRequest
	mu      sync.Mutex
	subject string
	keys    lockKeys
	err     error
	called  bool
}

// BeforeBind implements session.BindGate.
func (g *bindGate) BeforeBind(ctx context.Context, subject string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.called {
		// Fail closed: one sign-in checks one person. A second call with
		// another subject could bind a person whom the gate did not count.
		if subject != g.subject {
			g.err = errGateTwice

			return errGateTwice
		}

		return g.err
	}

	g.called, g.subject = true, subject

	keys, err := g.svc.countKey(ctx, g.req, keyKindSubject, subject)
	g.keys = keys

	if errors.Is(err, authgate.ErrNameLocked) {
		// Keep the refusal: the service refuses the sign-in also when the
		// method ignores this error and binds.
		g.err = fmt.Errorf("%w: %w", ErrSubjectLocked, err)

		return g.err
	}

	if err != nil {
		g.err = err

		return err
	}

	return nil
}

// wasCalled reports whether the method called the gate.
func (g *bindGate) wasCalled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.called
}

// failed returns the refusal that the gate gave: a locked person, a count
// that the gate could not write, or a second call with another subject. It
// returns nil when the gate refused nothing.
func (g *bindGate) failed() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.err
}

// lockKeys returns the keys of the count of the subject, or empty keys when
// the gate counted nothing.
func (g *bindGate) lockKeys() lockKeys {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.keys
}

// checkAssertion returns the reason for which the Assertion a of a method
// that returned no error does not belong to the person whom the gate counted,
// or "". Defense in depth: the conformance suite of the port checks that a
// method calls the gate with the subject of its Assertion. This check stays so
// that a method that skips the gate can never sign a person in past the
// lockout.
func (g *bindGate) checkAssertion(a *session.Assertion) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch {
	case !g.called:
		return ReasonGateSkipped
	case a.Subject != g.subject:
		return ReasonGateSubject
	default:
		return ""
	}
}
