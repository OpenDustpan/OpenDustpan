package localusersvc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
)

// SignedIn is the result of a good password sign-in: the person, the local
// user, and the session generation that the read of the hash returned. The
// session of the sign-in must carry this generation, never one that a later
// read returns.
type SignedIn struct {
	PrincipalID       string
	UserID            string
	SessionGeneration int64
	ChangeRequired    bool
}

// SignIn checks the user name and the password. It returns
// localuser.ErrSignInFailed for an unknown user name, a wrong password, a
// disabled principal and a user with no password, after the same work, and
// writes the real reason to the audit log. It returns
// localuser.ErrPasswordSignInOff while the password sign-in is off,
// localuser.ErrHashBusy when the hash budget of the instance is full, and
// localuser.ErrUnavailable when a store or the hasher cannot answer.
//
// base is the entry of the caller: the source address, the user agent and
// the request ID.
func (s *Service) SignIn(ctx context.Context, base audit.Entry, userName, password string) (SignedIn, error) {
	base.Subject = "sign-in:" + localuser.ProviderID

	p, err := s.requireEnabled(ctx)
	if err != nil {
		return SignedIn{}, s.refuse(ctx, base, ActionSignInPasswordFails, unknownObject, "password sign-in is off", err)
	}

	in := attempt{base: base, object: unknownObject, reason: "", hash: "", cred: localuser.Credential{}}

	nfc, formErr := checkForm(password)
	name := localuser.FoldUserName(userName)

	switch {
	case formErr != nil:
		// The hasher refuses such a password too, so no hash runs. The
		// timing of this answer tells nothing about a user.
		return SignedIn{}, s.refuse(ctx, base, ActionSignInPasswordFails, unknownObject, ReasonPasswordForm,
			localuser.ErrSignInFailed)
	case localuser.ValidateUserName(name) != nil:
		in.reason = ReasonNameForm
	default:
		in, err = s.readCredential(ctx, in, name)
		if err != nil {
			return SignedIn{}, err
		}
	}

	return s.verify(ctx, in, nfc, p)
}

// attempt is the state of one password sign-in between its steps.
type attempt struct {
	base   audit.Entry
	object string
	// reason is the reason of a failure that the check of the password must
	// not tell: an unknown name or a user with no password. Empty for a user
	// whose hash the sign-in checks.
	reason string
	hash   localuser.PasswordHash
	cred   localuser.Credential
}

// readCredential reads the credential of the folded user name name. An
// unknown name or a user with no password sets the reason of the failure, and
// the check of the password then runs against the fixed hash.
func (s *Service) readCredential(ctx context.Context, in attempt, name string) (attempt, error) {
	cred, err := s.store.UserByName(ctx, name)

	switch {
	case errors.Is(err, localuser.ErrNotFound):
		in.reason = ReasonUnknownName
	case err != nil:
		return in, s.refuse(ctx, in.base, ActionSignInPasswordFails, unknownObject, "store cannot answer",
			fmt.Errorf("%w: read the local user", localuser.ErrUnavailable))
	case cred.Hash == "":
		in.cred, in.object, in.reason = cred, UserObject(cred.User.ID), ReasonNoPassword
	default:
		in.cred, in.object, in.hash = cred, UserObject(cred.User.ID), cred.Hash
	}

	return in, nil
}

// verify checks the password nfc against the hash of in, or against the fixed
// hash when in has a reason already, so that the time of the answer is the
// same. It checks the state of the principal only after a full check of the
// password.
func (s *Service) verify(ctx context.Context, in attempt, nfc string, p Policy) (SignedIn, error) {
	h := in.hash
	if in.reason != "" {
		dummy, err := s.dummyHash(ctx)
		if err != nil {
			return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, "hasher cannot answer", err)
		}

		h = dummy
	}

	res, err := s.hasher.Reserve(ctx, h)

	switch {
	case errors.Is(err, localuser.ErrHashBusy):
		// A full budget counts no failure: the sign-in never reached the
		// compare, so an attacker who fills the budget adds no failure to the
		// name of a person.
		return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, "hash budget full",
			localuser.ErrHashBusy)
	case errors.Is(err, localuser.ErrInvalid):
		// Fail closed: a stored hash that does not parse, or whose parameters
		// are outside the bounds, never signs a person in.
		return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, ReasonStoredHash,
			localuser.ErrSignInFailed)
	case err != nil || res == nil:
		return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, "hasher cannot answer",
			fmt.Errorf("%w: reserve the hash", localuser.ErrUnavailable))
	}

	verdict, err := res.Verify(nfc, localuser.DefaultHashParams())
	res.Release()

	switch {
	case in.reason != "":
		return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, in.reason,
			localuser.ErrSignInFailed)
	case errors.Is(err, localuser.ErrWrongPassword):
		return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, ReasonWrongPassword,
			localuser.ErrSignInFailed)
	case errors.Is(err, localuser.ErrInvalid):
		return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, ReasonStoredHash,
			localuser.ErrSignInFailed)
	case err != nil:
		return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, "hasher cannot answer",
			fmt.Errorf("%w: check the password", localuser.ErrUnavailable))
	}

	if reason := s.principalRefusal(ctx, in.cred, p); reason != "" {
		return SignedIn{}, s.refuse(ctx, in.base, ActionSignInPasswordFails, in.object, reason,
			signInErrorFor(reason))
	}

	if verdict.NeedsRehash {
		s.rehash(ctx, in, nfc)
	}

	return SignedIn{
		PrincipalID: in.cred.User.PrincipalID, UserID: in.cred.User.ID,
		SessionGeneration: in.cred.SessionGeneration, ChangeRequired: in.cred.User.ChangeRequired,
	}, nil
}

// signInErrorFor returns the answer of a refusal of the state of the
// principal: ErrSecondFactorRequired, which a person can act on and which
// tells nothing that the good password did not, or ErrSignInFailed.
func signInErrorFor(reason string) error {
	if reason == ReasonSecondFactor {
		return localuser.ErrSecondFactorRequired
	}

	return localuser.ErrSignInFailed
}

// principalRefusal returns the reason for which the person of cred cannot
// sign in after a good password, or "" when it can.
func (s *Service) principalRefusal(ctx context.Context, cred localuser.Credential, p Policy) string {
	pr, err := s.principals.GetPrincipal(ctx, cred.User.PrincipalID)

	switch {
	case err != nil:
		return ReasonPrincipalError
	case pr.Kind != principal.KindPerson:
		return ReasonNotPerson
	case !pr.DisabledAt.IsZero():
		return ReasonDisabled
	case pr.BreakGlass:
		// Defense in depth: the store refuses a password for a break-glass
		// person in its own transaction. This check stays so that a row from
		// another version never signs a break-glass person in with a password.
		return ReasonBreakGlass
	case s.kind != installation.KindDev && (cred.User.Demo || principal.IsDevSubject(pr.Subject)):
		return ReasonDevSubject
	case p.SSO && p.EnabledSource == SourceSet:
		// Next to an identity provider, a password sign-in needs a second
		// factor. The server cannot ask for one yet, so the sign-in fails
		// closed.
		return ReasonSecondFactor
	default:
		return ""
	}
}

// rehash stores a new hash of the password with the current parameters, with
// a compare-and-swap on the old hash. A failure leaves the old hash, which
// still works: the next good sign-in tries again.
func (s *Service) rehash(ctx context.Context, in attempt, nfc string) {
	h, err := s.hasher.Hash(ctx, nfc, localuser.DefaultHashParams())
	if err != nil {
		return
	}

	e := s.entry(in.base, ActionPasswordRehashed, in.object, audit.OutcomeSuccess, "")
	e.Subject = principal.LocalSubjectPrefix + in.cred.User.ID

	if err := s.store.RehashPassword(ctx, in.cred.User.ID, in.hash, h, e); err != nil {
		// A failed rehash keeps the old hash, which still works, and the
		// sign-in goes on: the next good sign-in tries again.
		return
	}
}

// dummyHash returns the fixed hash of the current parameters, against which
// a sign-in checks the password of an unknown name or of a user with no
// password. The service makes it once, from a random password.
func (s *Service) dummyHash(ctx context.Context) (localuser.PasswordHash, error) {
	s.dummyMu.Lock()
	defer s.dummyMu.Unlock()

	if s.dummy != "" {
		return s.dummy, nil
	}

	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: random bytes: %w", localuser.ErrUnavailable, err)
	}

	h, err := s.hasher.Hash(ctx, base64.RawURLEncoding.EncodeToString(b), localuser.DefaultHashParams())
	if err != nil {
		return "", fmt.Errorf("make the fixed hash: %w", err)
	}

	s.dummy = h

	return h, nil
}
