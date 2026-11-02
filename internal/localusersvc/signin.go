package localusersvc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
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
	locks             lockKeys
	SessionGeneration uint64
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
		return SignedIn{}, s.refuse(
			ctx,
			base,
			on(ActionSignInPasswordFails, unknownObject),
			"password sign-in is off",
			err,
		)
	}

	in := &attempt{
		base: base, object: unknownObject, reason: "", name: "", hash: "",
		cred: localuser.Credential{User: localuser.User{
			CreatedAt: time.Time{}, PasswordSetAt: time.Time{}, ID: "", PrincipalID: "", UserName: "", CreatedBy: "",
			ChangeRequired: false, Demo: false,
		}, Hash: "", SessionGeneration: 0},
	}

	nfc, formErr := checkForm(password)
	name := localuser.FoldUserName(userName)
	in.name = name

	switch {
	case formErr != nil:
		// The hasher refuses such a password too, so no hash runs. The
		// timing of this answer tells nothing about a user.
		return SignedIn{}, s.refuse(ctx, base, on(ActionSignInPasswordFails, unknownObject), ReasonPasswordForm,
			localuser.ErrSignInFailed)
	case localuser.ValidateUserName(name) != nil:
		in.reason = ReasonNameForm
	default:
		if readErr := s.readCredential(ctx, in, name); readErr != nil {
			return SignedIn{}, readErr
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
	// name is the folded user name that the client sent. Its digest names
	// the counts of the lockout by name.
	name string
	hash localuser.PasswordHash
	cred localuser.Credential
}

// readCredential reads the credential of the folded user name into in.
// An unknown name or a user with no password sets the reason of the failure,
// and the check of the password then runs against the fixed hash.
func (s *Service) readCredential(ctx context.Context, in *attempt, name string) error {
	cred, err := s.store.UserByName(ctx, name)

	switch {
	case errors.Is(err, localuser.ErrNotFound):
		in.reason = ReasonUnknownName
	case err != nil:
		return s.refuse(ctx, in.base, on(ActionSignInPasswordFails, unknownObject), "store cannot answer",
			fmt.Errorf("%w: read the local user", localuser.ErrUnavailable))
	case cred.Hash == "":
		in.cred, in.object, in.reason = cred, UserObject(cred.User.ID), ReasonNoPassword
	default:
		in.cred, in.object, in.hash = cred, UserObject(cred.User.ID), cred.Hash
	}

	return nil
}

// verify checks the password nfc against the hash of in, or against the fixed
// hash when in has a reason already, so that the time of the answer is the
// same. It checks the state of the principal only after a full check of the
// password.
func (s *Service) verify(ctx context.Context, in *attempt, nfc string, p Policy) (SignedIn, error) {
	res, err := s.reserve(ctx, in)
	if err != nil {
		return SignedIn{}, err
	}

	// The failure counts after the hash has its budget and before the
	// compare: a request that finds the budget full counts nothing, and of
	// attempts at the same time no more than the limit reach the compare.
	keys, err := s.countAttempt(ctx, in)
	locked := errors.Is(err, authgate.ErrNameLocked)

	if err != nil && !locked {
		res.Release()

		return SignedIn{}, err
	}

	// A locked name runs the compare too, and the result is discarded, so that
	// the time of the answer does not tell that the name is locked.
	verdict, err := res.Verify(nfc, localuser.DefaultHashParams())
	res.Release()

	if locked {
		return SignedIn{}, s.refuse(ctx, in.base, on(ActionSignInPasswordFails, in.object), ReasonNameLocked,
			localuser.ErrSignInFailed)
	}

	if refusal := s.compareRefusal(ctx, in, err); refusal != nil {
		return SignedIn{}, refusal
	}

	if reason := s.principalRefusal(ctx, in.cred, p); reason != "" {
		return SignedIn{}, s.refuse(ctx, in.base, on(ActionSignInPasswordFails, in.object), reason,
			signInErrorFor(reason))
	}

	if verdict.NeedsRehash {
		s.rehash(ctx, in, nfc)
	}

	return SignedIn{
		PrincipalID: in.cred.User.PrincipalID, UserID: in.cred.User.ID, locks: keys,
		SessionGeneration: in.cred.SessionGeneration, ChangeRequired: in.cred.User.ChangeRequired,
	}, nil
}

// countAttempt adds the failure of the attempt in to the counts of its name
// before the compare, and returns the keys of the counts. Only the demo person
// of an installation of the kind dev has no lockout by name, so one visitor cannot lock out
// the others. While a lock holds, it returns an error that wraps
// authgate.ErrNameLocked, and verify runs the compare and then writes the one
// refusal entry. A count that cannot be written refuses the attempt with
// ErrUnavailable.
func (s *Service) countAttempt(ctx context.Context, in *attempt) (lockKeys, error) {
	tg := on(ActionSignInPasswordFails, in.object)

	// Defense in depth: the demo person exists only on a dev installation,
	// and principalRefusal refuses it after the compare on any other kind.
	// This check stays so that a demo flag on a row of another installation
	// kind, from a restore or a bug, never turns the lockout off.
	if in.cred.User.Demo && s.kind == installation.KindDev {
		return lockKeys{name: "", sub: ""}, nil
	}

	name, err := s.nameDigest(ctx, in.name)
	if err != nil {
		return lockKeys{name: "", sub: ""}, s.refuse(ctx, in.base, tg, "key of the names cannot be read", err)
	}

	keys := lockKeys{name: name, sub: SourcePrefix(in.base.SourceIP)}

	err = s.countFailure(ctx, in.base, in.object, keys)

	switch {
	case errors.Is(err, authgate.ErrNameLocked):
		return keys, err
	case err != nil:
		return keys, s.refuse(ctx, in.base, tg, "count of the name cannot be written", err)
	default:
		return keys, nil
	}
}

// reserve takes the memory of the hash of in, or of the fixed hash when in
// has a reason already, from the budget of the instance.
func (s *Service) reserve(ctx context.Context, in *attempt) (*localuser.Reservation, error) {
	tg := on(ActionSignInPasswordFails, in.object)

	h := in.hash
	if in.reason != "" {
		dummy, err := s.dummyHash(ctx)
		if err != nil {
			return nil, s.refuse(ctx, in.base, tg, "hasher cannot answer", err)
		}

		h = dummy
	}

	res, err := s.hasher.Reserve(ctx, h)

	switch {
	case errors.Is(err, localuser.ErrHashBusy):
		// A full budget counts no failure: the sign-in never reached the
		// compare, so an attacker who fills the budget adds no failure to the
		// name of a person.
		return nil, s.refuse(ctx, in.base, tg, "hash budget full", localuser.ErrHashBusy)
	case errors.Is(err, localuser.ErrInvalid):
		// Fail closed: a stored hash that does not parse, or whose parameters
		// are outside the bounds, never signs a person in.
		return nil, s.refuse(ctx, in.base, tg, ReasonStoredHash, localuser.ErrSignInFailed)
	case err != nil || res == nil:
		return nil, s.refuse(ctx, in.base, tg, "hasher cannot answer",
			fmt.Errorf("%w: reserve the hash", localuser.ErrUnavailable))
	default:
		return res, nil
	}
}

// compareRefusal returns the refusal of a compare that failed with err, or of
// an attempt that had a reason before the compare, or nil for a good
// password.
func (s *Service) compareRefusal(ctx context.Context, in *attempt, err error) error {
	tg := on(ActionSignInPasswordFails, in.object)

	switch {
	case in.reason != "":
		return s.refuse(ctx, in.base, tg, in.reason, localuser.ErrSignInFailed)
	case errors.Is(err, localuser.ErrWrongPassword):
		return s.refuse(ctx, in.base, tg, ReasonWrongPassword, localuser.ErrSignInFailed)
	case errors.Is(err, localuser.ErrInvalid):
		return s.refuse(ctx, in.base, tg, ReasonStoredHash, localuser.ErrSignInFailed)
	case err != nil:
		return s.refuse(ctx, in.base, tg, "hasher cannot answer",
			fmt.Errorf("%w: check the password", localuser.ErrUnavailable))
	default:
		return nil
	}
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
func (s *Service) rehash(ctx context.Context, in *attempt, nfc string) {
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
