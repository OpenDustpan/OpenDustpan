// Package localusersvc is the service of the local users of an installation:
// the persons who sign in to the console with a user name and a password.
//
// The service checks each input from outside the process, hashes and checks
// passwords through the ports of package localuser, and writes each change and
// each refusal to the audit log. It never puts a password, a hash or a code in
// an error, a log record or an audit entry.
//
// A password write, a reset and a removal add 1 to the session generation of
// the person in the transaction of the store, so each console session of the
// person ends at its next request. A sign-in reads the session generation in
// the same read as the hash, and the session that it starts carries that
// generation, so a reset that races the sign-in ends that session too.
package localusersvc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/setting"
)

// Audit actions of the local users. They are not authorization actions.
const (
	ActionUserCreated         = "local-user.created"
	ActionPasswordChanged     = "password.changed"
	ActionPasswordRemoved     = "password.removed"
	ActionPasswordRehashed    = "password.rehashed"
	ActionResetCreated        = "password.reset-created"
	ActionResetUsed           = "password.reset-used"
	ActionResetRefused        = "password.reset-refused"
	ActionPasswordRefused     = "password.change-refused"
	ActionSignInPasswordFails = "sign-in.password-failed"
)

// Reasons of a refused password sign-in, in the audit entry. The answer to the
// client is always localuser.ErrSignInFailed. No reason holds a password, a
// hash or a user name.
const (
	ReasonUnknownName    = "unknown user name"
	ReasonNameForm       = "user name not valid"
	ReasonPasswordForm   = "password not valid"
	ReasonNoPassword     = "user has no password"
	ReasonWrongPassword  = "wrong password"
	ReasonStoredHash     = "stored hash not valid"
	ReasonDisabled       = "principal is disabled"
	ReasonNotPerson      = "principal is not a person"
	ReasonBreakGlass     = "principal is a break-glass account"
	ReasonDevSubject     = "dev user on an installation that is not of the kind dev"
	ReasonSecondFactor   = "second factor required"
	ReasonPrincipalError = "principal cannot be read"
)

// codeBytes is the count of random bytes of a reset code, and codeLength the
// length of its base64url text without padding.
const (
	codeBytes  = localuser.ResetSecretBytes
	codeLength = 43
)

// userIDBytes is the count of random bytes in the ID of a local user record.
const userIDBytes = 16

// Stores is every persistence port that the service uses.
type Stores interface {
	localuser.Store
	localuser.PasswordStore
	localuser.ResetStore
}

// Principals makes and reads the person principals of local users.
// *principal.Service meets it.
type Principals interface {
	// CreateLocalPerson makes the person of the local user userID.
	CreateLocalPerson(ctx context.Context, e audit.Entry, userID, displayName string) (principal.Principal, error)
	// GetPrincipal returns the principal id.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}

// Settings answers the effective value of an installation setting.
// *setting.Service meets it.
type Settings interface {
	// Effective returns the setting key of scope with its effective value
	// and its source.
	Effective(ctx context.Context, scope setting.Scope, key string) (setting.Item, error)
}

// Auditor appends an audit entry in the scope of the installation, for a
// refusal that changes nothing.
type Auditor interface {
	// AppendServer appends e in audit.ServerScope.
	AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error)
}

// Config holds the dependencies of a Service. Every field but Now and SSO is
// required. A nil Settings applies the defaults of each setting. A nil SSO
// means that the installation has no identity provider.
type Config struct {
	Store      Stores
	Principals Principals
	Hasher     localuser.PasswordHasher
	Blocklist  localuser.Blocklist
	Audit      Auditor
	// Locks counts the failures of each user name, so every instance on one
	// database shares one lockout by name.
	Locks authgate.NameLockCounter
	// Keys reads the key of the digests of the user names in Locks.
	Keys     NameKeys
	Settings Settings
	// SSO reports whether the installation has an identity provider: a
	// provider of the configuration or an enabled stored provider.
	SSO func(ctx context.Context) (bool, error)
	// Reach reads the reach of a principal for the check of a reset. A
	// config with a nil field makes each reset fail closed.
	Reach access.ReachConfig
	// Kind is the installation kind of the server.
	Kind installation.Kind
	Now  func() time.Time
}

// ErrMissingDependency reports a Config with a required field that is nil.
var ErrMissingDependency = errors.New("localusersvc: missing dependency")

// Service is the service of the local users.
type Service struct {
	store      Stores
	principals Principals
	hasher     localuser.PasswordHasher
	blocklist  localuser.Blocklist
	audit      Auditor
	locks      authgate.NameLockCounter
	keys       NameKeys
	settings   Settings
	sso        func(ctx context.Context) (bool, error)
	reach      access.ReachConfig
	now        func() time.Time
	kind       installation.Kind

	dummyMu sync.Mutex
	dummy   localuser.PasswordHash
}

// New returns a Service. It returns ErrMissingDependency for a required
// dependency that is nil.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: store", ErrMissingDependency)
	case cfg.Principals == nil:
		return nil, fmt.Errorf("%w: principals", ErrMissingDependency)
	case cfg.Hasher == nil:
		return nil, fmt.Errorf("%w: hasher", ErrMissingDependency)
	case cfg.Blocklist == nil:
		return nil, fmt.Errorf("%w: blocklist", ErrMissingDependency)
	case cfg.Audit == nil:
		return nil, fmt.Errorf("%w: audit", ErrMissingDependency)
	case cfg.Locks == nil:
		return nil, fmt.Errorf("%w: name locks", ErrMissingDependency)
	case cfg.Keys == nil:
		return nil, fmt.Errorf("%w: name lock key", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		store:      cfg.Store,
		principals: cfg.Principals,
		hasher:     cfg.Hasher,
		blocklist:  cfg.Blocklist,
		audit:      cfg.Audit,
		locks:      cfg.Locks,
		keys:       cfg.Keys,
		settings:   cfg.Settings,
		sso:        cfg.SSO, reach: cfg.Reach,
		now:     now,
		kind:    cfg.Kind,
		dummyMu: sync.Mutex{},
		dummy:   "",
	}, nil
}

// Now returns the time of the clock of the service, in UTC, to the
// millisecond.
func (s *Service) Now() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Source names where the effective value of the switch of the password
// sign-in comes from.
type Source string

// Sources of the switch of the password sign-in.
const (
	// SourceDefault means that no one set the switch: the presence of an
	// identity provider decides.
	SourceDefault Source = "default"
	// SourceSet means that a setting gives the value.
	SourceSet Source = "set"
)

// Policy is the effective password policy of the installation.
type Policy struct {
	// EnabledSource says where Enabled comes from.
	EnabledSource Source
	// ResetLifetime is the lifetime of a new reset code or set-up code.
	ResetLifetime time.Duration
	// MinLength is the minimum length of a new password, in code points.
	MinLength int
	// Enabled reports whether the password sign-in is on.
	Enabled bool
	// SSO reports whether the installation has an identity provider.
	SSO bool
	// RequireLettersAndDigits makes each new password hold a letter and a
	// digit.
	RequireLettersAndDigits bool
}

// Policy returns the effective password policy. A setting that cannot be read,
// or whose value is not valid, fails the call with ErrUnavailable: the
// service never applies a weaker value than the one that the operator set.
func (s *Service) Policy(ctx context.Context) (Policy, error) {
	sso := false

	if s.sso != nil {
		has, err := s.sso(ctx)
		if err != nil {
			return Policy{}, fmt.Errorf("%w: read the identity providers: %w", localuser.ErrUnavailable, err)
		}

		sso = has
	}

	enabled, err := s.value(ctx, localuser.SettingEnabled)
	if err != nil {
		return Policy{}, err
	}

	minText, err := s.value(ctx, localuser.SettingMinLength)
	if err != nil {
		return Policy{}, err
	}

	lettersText, err := s.value(ctx, localuser.SettingRequireLettersAndDigits)
	if err != nil {
		return Policy{}, err
	}

	lifetimeText, err := s.value(ctx, localuser.SettingResetLifetime)
	if err != nil {
		return Policy{}, err
	}

	return policyOf(sso, enabled, minText, lettersText, lifetimeText)
}

// policyOf builds the policy from the text of each setting. A value that is
// not valid returns ErrUnavailable: a changed row in the database or a static
// value that skipped its check never weakens a rule.
func policyOf(sso bool, enabled, minText, lettersText, lifetimeText string) (Policy, error) {
	p := Policy{
		EnabledSource: SourceSet, ResetLifetime: 0, MinLength: 0, Enabled: false, SSO: sso,
		RequireLettersAndDigits: false,
	}

	switch enabled {
	case "":
		p.Enabled, p.EnabledSource = !sso, SourceDefault
	case "true":
		p.Enabled = true
	case "false":
		p.Enabled = false
	default:
		return Policy{}, fmt.Errorf("%w: the setting %s holds a value that is not valid",
			localuser.ErrUnavailable, localuser.SettingEnabled)
	}

	minLength, err := localuser.ParseMinLength(minText)
	if err != nil {
		return Policy{}, fmt.Errorf("%w: the setting %s holds a value that is not valid",
			localuser.ErrUnavailable, localuser.SettingMinLength)
	}

	letters, err := strconv.ParseBool(lettersText)
	if err != nil || (lettersText != "true" && lettersText != "false") {
		return Policy{}, fmt.Errorf("%w: the setting %s holds a value that is not valid",
			localuser.ErrUnavailable, localuser.SettingRequireLettersAndDigits)
	}

	lifetime, err := localuser.ParseResetLifetime(lifetimeText)
	if err != nil {
		return Policy{}, fmt.Errorf("%w: the setting %s holds a value that is not valid",
			localuser.ErrUnavailable, localuser.SettingResetLifetime)
	}

	p.MinLength, p.RequireLettersAndDigits, p.ResetLifetime = minLength, letters, lifetime

	return p, nil
}

// value returns the effective text of the setting key, or its default when
// the service has no settings.
func (s *Service) value(ctx context.Context, key string) (string, error) {
	if s.settings == nil {
		for _, d := range localuser.SettingDeclarations() {
			if d.Key == key {
				return d.Default, nil
			}
		}

		return "", fmt.Errorf("%w: the setting %s is not declared", localuser.ErrUnavailable, key)
	}

	item, err := s.settings.Effective(ctx, setting.InstallationScope(), key)
	if err != nil {
		return "", fmt.Errorf("%w: read the setting %s: %w", localuser.ErrUnavailable, key, err)
	}

	return item.Value, nil
}

// requireEnabled returns ErrPasswordSignInOff while the password sign-in is
// off, and the policy otherwise.
func (s *Service) requireEnabled(ctx context.Context) (Policy, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return Policy{}, err
	}

	if !p.Enabled {
		return Policy{}, localuser.ErrPasswordSignInOff
	}

	return p, nil
}

// Actor is the caller of a change: the audit entry that the HTTP adapter
// fills (subject, source address, user agent, channel, request ID) and the
// principal ID of the caller.
type Actor struct {
	Entry       audit.Entry
	PrincipalID string
}

// entry returns the entry of a of the action on object at the time of the
// service, with the outcome and the reason.
func (s *Service) entry(base audit.Entry, action, object string, outcome audit.Outcome, reason string) audit.Entry {
	e := base
	e.At = s.Now()
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = outcome
	e.Action = action
	e.Object = object
	e.Reason = reason

	return e
}

// target is the action and the object of an audit entry.
type target struct {
	action string
	object string
}

// on returns the target of action on object.
func on(action, object string) target {
	return target{action: action, object: object}
}

// refuse appends the entry of a refusal of the target with the cause, and
// returns err. A refused append joins its error to err: the refusal changed
// nothing, and the caller still refuses.
func (s *Service) refuse(ctx context.Context, base audit.Entry, tg target, cause string, err error) error {
	outcome := audit.OutcomeDenied
	if errors.Is(err, localuser.ErrUnavailable) || errors.Is(err, localuser.ErrHashBusy) {
		outcome = audit.OutcomeFailure
	}

	e := s.entry(base, tg.action, tg.object, outcome, "")
	e.Changes = []audit.Change{{Key: "cause", Old: nil, New: cause, Layer: "", TakesEffect: ""}}

	if _, appendErr := s.audit.AppendServer(ctx, e); appendErr != nil {
		return errors.Join(err, fmt.Errorf("append the audit entry of a refusal: %w", appendErr))
	}

	return err
}

// UserObject returns the audit object of the local user id.
func UserObject(id string) string {
	return "local-user:" + id
}

// unknownObject is the audit object of a sign-in or a redeem that names no
// local user. The entry never holds the name or the code that the client sent.
const unknownObject = "local-user:unknown"

// newCode returns a new reset code: codeBytes from crypto/rand in base64url
// with no padding.
func newCode() (string, error) {
	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: random bytes: %w", localuser.ErrUnavailable, err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newUserID returns a new ID of a local user record.
func newUserID() (string, error) {
	b := make([]byte, userIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: random bytes: %w", localuser.ErrUnavailable, err)
	}

	return localuser.UserIDPrefix + hex.EncodeToString(b), nil
}

// newResetID returns a new ID of a reset code record.
func newResetID() (string, error) {
	b := make([]byte, userIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("%w: random bytes: %w", localuser.ErrUnavailable, err)
	}

	return localuser.ResetIDPrefix + hex.EncodeToString(b), nil
}

// validCode reports whether code has the form of a reset code: codeLength
// characters of the base64url alphabet.
func validCode(code string) bool {
	if len(code) != codeLength {
		return false
	}

	for i := range len(code) {
		c := code[i]
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'

		if !ok {
			return false
		}
	}

	return true
}

// validReason checks the reason of an administrator change: 1 to
// MaxReasonLength bytes of valid text with no control character.
func validReason(reason string) error {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" || len(reason) > localuser.MaxReasonLength ||
		strings.ContainsFunc(reason, func(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) }) {
		return localuser.ErrReasonRequired
	}

	return nil
}

// contextWords returns the words of the person that no password can hold:
// the user name and each word of the display name.
func contextWords(userName, displayName string) []string {
	parts := strings.FieldsFunc(displayName, func(r rune) bool {
		return r == ' ' || r == '.' || r == '_' || r == '-' || r == '@'
	})

	words := make([]string, 0, 1+len(parts))
	words = append(words, userName)

	return append(words, parts...)
}
