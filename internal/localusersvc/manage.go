package localusersvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
)

// CreateInput names a new local user: the user name, the display name of the
// person, and the reason of the administrator.
type CreateInput struct {
	UserName    string
	DisplayName string
	Reason      string
}

// Code is a reset code or a set-up code, shown once. Text is the code, and
// ExpiresAt the end of its life. The server keeps only the digest of Text.
type Code struct {
	ExpiresAt time.Time
	Text      string
}

// Created is a new local user with its person and its set-up code.
type Created struct {
	User      localuser.User
	Principal principal.Principal
	SetUp     Code
}

// CreateUser makes a person principal, its local user record with no
// password, and a set-up code. The administrator gives the code to the
// person, who chooses the password with Redeem. So no administrator chooses
// or sees the password of a person.
//
// It returns localuser.ErrInvalid for a user name that does not match
// localuser.UserNamePattern, localuser.ErrReasonRequired for no reason, and
// localuser.ErrAlreadyExists for a user name that a local user holds. When the
// record or the code fails after the person was made, the person stays with
// no credential: it cannot sign in, and an administrator can delete it.
func (s *Service) CreateUser(ctx context.Context, a Actor, in CreateInput) (Created, error) {
	// The user name is checked as it came: the server never changes a name
	// to make it valid.
	if err := localuser.ValidateUserName(in.UserName); err != nil {
		return Created{}, s.refuse(ctx, a.Entry, on(ActionUserCreated, unknownObject), "user name not valid", err)
	}

	if err := validReason(in.Reason); err != nil {
		return Created{}, s.refuse(ctx, a.Entry, on(ActionUserCreated, unknownObject), "no reason", err)
	}

	p, err := s.Policy(ctx)
	if err != nil {
		return Created{}, err
	}

	userID, err := newUserID()
	if err != nil {
		return Created{}, err
	}

	object := UserObject(userID)
	pe := s.entry(a.Entry, ActionUserCreated, "", audit.OutcomeSuccess, in.Reason)

	person, err := s.principals.CreateLocalPerson(ctx, pe, userID, in.DisplayName)
	if err != nil {
		return Created{}, fmt.Errorf("create the person of the local user: %w", err)
	}

	u := localuser.User{
		CreatedAt: s.Now(), PasswordSetAt: time.Time{}, ID: userID, PrincipalID: person.ID, UserName: in.UserName,
		CreatedBy: a.Entry.Subject, ChangeRequired: false, Demo: false,
	}

	ue := s.entry(a.Entry, ActionUserCreated, object, audit.OutcomeSuccess, in.Reason)
	ue.Changes = []audit.Change{
		{Key: "userName", Old: nil, New: u.UserName, Layer: "", TakesEffect: ""},
		{Key: "principal", Old: nil, New: person.ID, Layer: "", TakesEffect: ""},
	}

	if createErr := s.store.CreateUser(ctx, u, ue); createErr != nil {
		return Created{}, fmt.Errorf("create the local user: %w", createErr)
	}

	code, err := s.createCode(ctx, a, codeRequest{
		user: u, purpose: localuser.PurposeSetUp, reason: in.Reason, lifetime: p.ResetLifetime, revoke: false,
	})
	if err != nil {
		return Created{}, err
	}

	return Created{User: u, Principal: person, SetUp: code}, nil
}

// ResetInput names a reset of the password of a person: the reason of the
// administrator, and whether the reset revokes each access token of the
// person too.
type ResetInput struct {
	Reason             string
	RevokeAccessTokens bool
}

// StartReset makes a reset code for the local user of the person
// principalID. It ends every session of the person, and revokes each access
// token of the person when in.RevokeAccessTokens is set, in the transaction
// of the code. The administrator gives the code to the person on another
// channel.
//
// It returns localuser.ErrPasswordSignInOff while the password sign-in is
// off, localuser.ErrOwnAccount for the own account,
// localuser.ErrReasonRequired for no reason, localuser.ErrNotFound for a
// person with no local user, localuser.ErrBreakGlass for a break-glass
// person, and localuser.ErrDemoFixed for the demo person.
func (s *Service) StartReset(ctx context.Context, a Actor, principalID string, in ResetInput) (Code, error) {
	object := principal.Ref(principalID)

	p, err := s.requireEnabled(ctx)
	if err != nil {
		return Code{}, s.refuse(ctx, a.Entry, on(ActionResetCreated, object), "password sign-in is off", err)
	}

	// Defense in depth: the route refuses a reset of the own account too.
	// This check stays so that a new caller of the service cannot skip it.
	if a.PrincipalID == "" || a.PrincipalID == principalID {
		return Code{}, s.refuse(ctx, a.Entry, on(ActionResetCreated, object), "own account", localuser.ErrOwnAccount)
	}

	if reasonErr := validReason(in.Reason); reasonErr != nil {
		return Code{}, s.refuse(ctx, a.Entry, on(ActionResetCreated, object), "no reason", reasonErr)
	}

	u, err := s.store.UserByPrincipal(ctx, principalID)
	if err != nil {
		return Code{}, s.refuse(ctx, a.Entry, on(ActionResetCreated, object), "no local user", err)
	}

	return s.createCode(ctx, a, codeRequest{
		user: u, purpose: localuser.PurposeReset, reason: in.Reason, lifetime: p.ResetLifetime,
		revoke: in.RevokeAccessTokens,
	})
}

// codeRequest names a new code: its user, its purpose, the reason of the
// administrator, whether a reset revokes the access tokens, and its
// lifetime.
type codeRequest struct {
	user     localuser.User
	purpose  localuser.Purpose
	reason   string
	lifetime time.Duration
	revoke   bool
}

// createCode stores a new code of in for its user and returns it.
func (s *Service) createCode(ctx context.Context, a Actor, in codeRequest) (Code, error) {
	u, purpose, reason, revoke, lifetime := in.user, in.purpose, in.reason, in.revoke, in.lifetime

	text, err := newCode()
	if err != nil {
		return Code{}, err
	}

	id, err := newResetID()
	if err != nil {
		return Code{}, err
	}

	at := s.Now()
	r := localuser.Reset{
		CreatedAt: at, ExpiresAt: at.Add(lifetime), ID: id, UserID: u.ID, CreatedBy: a.Entry.Subject,
		Reason: reason, Purpose: purpose, RevokeAccessTokens: revoke,
	}

	object := UserObject(u.ID)
	e := s.entry(a.Entry, ActionResetCreated, object, audit.OutcomeSuccess, reason)

	if createErr := s.store.CreateReset(ctx, r, localuser.DigestOf(text), e); createErr != nil {
		if errors.Is(createErr, localuser.ErrBreakGlass) || errors.Is(createErr, localuser.ErrDemoFixed) {
			return Code{}, s.refuse(ctx, a.Entry, on(ActionResetCreated, object), "account cannot have a password",
				createErr)
		}

		return Code{}, fmt.Errorf("create the reset code: %w", createErr)
	}

	return Code{ExpiresAt: r.ExpiresAt, Text: text}, nil
}

// Redeem uses a set-up code or a reset code: it checks the new password
// against the rules, and then stores it, deletes the code and ends every
// session of the person in one transaction. A password that a rule refuses
// never uses up the code.
//
// It returns localuser.ErrPasswordSignInOff while the password sign-in is
// off, localuser.ErrNotFound for a code that is not valid, unknown, used or
// expired, the error of the rule that refuses the password, and
// localuser.ErrBreakGlass for a break-glass person. base is the entry of the
// caller.
func (s *Service) Redeem(ctx context.Context, base audit.Entry, code, password string) error {
	base.Subject = "sign-in:" + localuser.ProviderID

	p, err := s.requireEnabled(ctx)
	if err != nil {
		return s.refuse(ctx, base, on(ActionResetRefused, unknownObject), "password sign-in is off", err)
	}

	if !validCode(code) {
		return s.refuse(ctx, base, on(ActionResetRefused, unknownObject), "code not valid", localuser.ErrNotFound)
	}

	d := localuser.DigestOf(code)

	u, err := s.store.UserOfReset(ctx, d, s.Now())
	if err != nil {
		return s.refuse(ctx, base, on(ActionResetRefused, unknownObject), "code unknown, used or expired", err)
	}

	object := UserObject(u.ID)
	base.Subject = principal.LocalSubjectPrefix + u.ID

	words := []string{u.UserName}
	if pr, prErr := s.principals.GetPrincipal(ctx, u.PrincipalID); prErr == nil {
		words = contextWords(u.UserName, pr.DisplayName)
	}

	nfc, err := s.checkNew(ctx, password, p, words)
	if err != nil {
		return s.refuse(ctx, base, on(ActionResetRefused, object), "password refused by a rule", err)
	}

	h, err := s.hasher.Hash(ctx, nfc, localuser.DefaultHashParams())
	if err != nil {
		return s.refuse(ctx, base, on(ActionResetRefused, object), "hasher cannot answer", hashError(err))
	}

	e := s.entry(base, ActionResetUsed, object, audit.OutcomeSuccess, "")
	rd := localuser.Redeem{At: s.Now(), Hash: h, Change: localuser.PasswordChange{ChangeRequired: false}, Digest: d}

	if _, redeemErr := s.store.RedeemReset(ctx, rd, e); redeemErr != nil {
		return s.refuse(ctx, base, on(ActionResetRefused, object), "redeem refused", redeemErr)
	}

	return nil
}

// hashError returns ErrHashBusy as it is, and ErrUnavailable for each other
// error of the hasher.
func hashError(err error) error {
	if errors.Is(err, localuser.ErrHashBusy) {
		return localuser.ErrHashBusy
	}

	return fmt.Errorf("%w: hash the password", localuser.ErrUnavailable)
}

// ChangeInput names a change of the own password: the current password, or
// a session with a fresh authentication, and the new password.
type ChangeInput struct {
	Current string
	New     string
	// Fresh reports that the session of the change has a fresh
	// authentication. With it, the change needs no current password.
	Fresh bool
}

// ChangePassword changes the password of the local user of the caller. It
// ends every session of the person; the HTTP adapter then starts a new
// session for the caller.
//
// It returns localuser.ErrNotFound for a caller with no local user,
// localuser.ErrDemoFixed for the demo person,
// localuser.ErrCurrentPasswordRequired with no current password and no fresh
// authentication, localuser.ErrWrongPassword for a wrong current password,
// and the error of the rule that refuses the new password.
func (s *Service) ChangePassword(ctx context.Context, a Actor, in ChangeInput) error {
	object := principal.Ref(a.PrincipalID)

	u, err := s.store.UserByPrincipal(ctx, a.PrincipalID)
	if err != nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), "no local user", err)
	}

	object = UserObject(u.ID)

	if u.Demo {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), "demo user", localuser.ErrDemoFixed)
	}

	p, err := s.Policy(ctx)
	if err != nil {
		return err
	}

	if currentErr := s.checkCurrent(ctx, a, u, in); currentErr != nil {
		return currentErr
	}

	words := []string{u.UserName}
	if pr, prErr := s.principals.GetPrincipal(ctx, u.PrincipalID); prErr == nil {
		words = contextWords(u.UserName, pr.DisplayName)
	}

	nfc, err := s.checkNew(ctx, in.New, p, words)
	if err != nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), "password refused by a rule", err)
	}

	h, err := s.hasher.Hash(ctx, nfc, localuser.DefaultHashParams())
	if err != nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), "hasher cannot answer", hashError(err))
	}

	e := s.entry(a.Entry, ActionPasswordChanged, object, audit.OutcomeSuccess, "")
	setErr := s.store.SetPassword(ctx, u.ID, h, localuser.PasswordChange{ChangeRequired: false}, e)
	if setErr != nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), "store refused the password", setErr)
	}

	return nil
}

// checkCurrent checks the current password of the change in, unless the
// session of the change has a fresh authentication.
func (s *Service) checkCurrent(ctx context.Context, a Actor, u localuser.User, in ChangeInput) error {
	object := UserObject(u.ID)

	if in.Current == "" {
		if in.Fresh {
			return nil
		}

		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), "no current password",
			localuser.ErrCurrentPasswordRequired)
	}

	nfc, err := checkForm(in.Current)
	if err != nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), ReasonPasswordForm, localuser.ErrWrongPassword)
	}

	cred, err := s.store.UserByName(ctx, u.UserName)
	if err != nil || cred.User.ID != u.ID || cred.Hash == "" {
		// A user with no password has no current password to check: only a
		// fresh authentication can set the first one.
		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), ReasonNoPassword,
			localuser.ErrCurrentPasswordRequired)
	}

	res, err := s.hasher.Reserve(ctx, cred.Hash)
	if err != nil || res == nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRefused, object), "hasher cannot answer", hashError(err))
	}

	_, err = res.Verify(nfc, localuser.DefaultHashParams())
	res.Release()

	if err != nil {
		return s.refuse(
			ctx,
			a.Entry,
			on(ActionPasswordRefused, object),
			ReasonWrongPassword,
			localuser.ErrWrongPassword,
		)
	}

	return nil
}

// RemovePassword removes the password of the local user of the person
// principalID. It returns localuser.ErrLastCredential when the person keeps
// no other credential, localuser.ErrOwnAccount for the own account,
// localuser.ErrReasonRequired for no reason, and localuser.ErrNotFound for a
// person with no local user.
func (s *Service) RemovePassword(ctx context.Context, a Actor, principalID, reason string) error {
	object := principal.Ref(principalID)

	// Defense in depth: the route refuses the own account too. This check
	// stays so that a new caller of the service cannot skip it.
	if a.PrincipalID == "" || a.PrincipalID == principalID {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRemoved, object), "own account", localuser.ErrOwnAccount)
	}

	if err := validReason(reason); err != nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRemoved, object), "no reason", err)
	}

	u, err := s.store.UserByPrincipal(ctx, principalID)
	if err != nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRemoved, object), "no local user", err)
	}

	object = UserObject(u.ID)
	e := s.entry(a.Entry, ActionPasswordRemoved, object, audit.OutcomeSuccess, reason)

	if err := s.store.RemovePassword(ctx, u.ID, e); err != nil {
		return s.refuse(ctx, a.Entry, on(ActionPasswordRemoved, object), "store refused the removal", err)
	}

	return nil
}

// User returns the local user of the person principalID, or
// localuser.ErrNotFound.
func (s *Service) User(ctx context.Context, principalID string) (localuser.User, error) {
	u, err := s.store.UserByPrincipal(ctx, principalID)
	if err != nil {
		return localuser.User{}, fmt.Errorf("read the local user: %w", err)
	}

	return u, nil
}
