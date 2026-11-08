package localusersvc

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Facts of the demo seed. The creator of each grant is DemoCreator, and the
// audit entry of each change carries DemoReason.
const (
	// DemoCreator is the creator of the grants of the demo person.
	DemoCreator = "system"
	// DemoReason is the reason that each audit entry of the demo seed gives.
	DemoReason = "demo user of opendustpan dev"
	// DemoResetReason is the reason of the entry of a grant that the seed
	// revokes because it is not in the fixed set of the demo person.
	DemoResetReason = "demo reset"
	// DemoDisplayName is the display name of the demo person.
	DemoDisplayName = "Demo user"
	// ActionDemoSeeded is the audit action of the password of the demo seed.
	ActionDemoSeeded = "local-user.demo-seeded"
	// demoPasswordAlphabet holds the characters of the demo password: lowercase
	// letters and digits.
	demoPasswordAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	// demoPasswordTries bounds the new passwords that the seed makes when a
	// password rule refuses one. A password of this form is refused rarely.
	demoPasswordTries = 100
	// demoRandomLimit is the first random byte that the seed does not use.
	// It is the largest multiple of the alphabet length that fits in a byte,
	// so each character has the same chance.
	demoRandomLimit = 252
)

// errDemoPassword reports that the seed could not make a password that the
// password rules accept.
var errDemoPassword = errors.New("no demo password passes the password rules")

// DemoPrincipals makes the principal of the demo person. *principal.Service
// meets it.
type DemoPrincipals interface {
	// SeedDemoPerson returns the person with the subject principal.DemoSubject
	// and makes it when it does not exist.
	SeedDemoPerson(ctx context.Context, e audit.Entry, displayName string) (principal.Principal, bool, error)
}

// DemoGrants writes the grants of the demo person. The grant store of the SQL
// adapters meets it.
type DemoGrants interface {
	// CreateGrant persists g, or returns access.ErrAlreadyExists.
	CreateGrant(
		ctx context.Context, g access.Grant, fence namespace.Fence, e audit.Entry, check access.GrantCheck,
	) error
	// GrantsOf returns every current grant of subject.
	GrantsOf(ctx context.Context, subject access.Subject) ([]access.Grant, error)
	// RevokeGrant ends the current grant id.
	RevokeGrant(ctx context.Context, id string, fence namespace.Fence, e audit.Entry, check access.GrantCheck) error
}

// DemoNamespaces reads the fence of a namespace.
type DemoNamespaces interface {
	// GetNamespace returns the namespace name, or namespace.ErrNotFound.
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// DemoConfig holds the dependencies of a DemoSeed. Every field but Now and
// Random is required.
type DemoConfig struct {
	Passwords  localuser.PasswordStore
	Principals DemoPrincipals
	Grants     DemoGrants
	Namespaces DemoNamespaces
	Hasher     localuser.PasswordHasher
	// Blocklist refuses a common password. The seed checks its password
	// against the same rules as a password that a person sets.
	Blocklist localuser.Blocklist
	// Kind is the installation kind of the server. Defense in depth: the
	// store reads the kind from its own database again at each seed.
	Kind installation.Kind
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Random is the source of the password. Nil means crypto/rand.
	Random io.Reader
}

// DemoSeed sets up the demo person of opendustpan dev: a local user with a
// password that the seed makes, an admin grant on each named namespace and a
// viewer grant on the installation.
type DemoSeed struct {
	cfg DemoConfig
	now func() time.Time
	rnd io.Reader
}

// NewDemoSeed returns a DemoSeed. It returns ErrMissingDependency for a
// required field that is nil, and localuser.ErrDemoOnly for a kind that is
// not dev.
func NewDemoSeed(cfg DemoConfig) (*DemoSeed, error) {
	switch {
	case cfg.Passwords == nil:
		return nil, fmt.Errorf("%w: password store", ErrMissingDependency)
	case cfg.Principals == nil:
		return nil, fmt.Errorf("%w: principals", ErrMissingDependency)
	case cfg.Grants == nil:
		return nil, fmt.Errorf("%w: grants", ErrMissingDependency)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespaces", ErrMissingDependency)
	case cfg.Hasher == nil:
		return nil, fmt.Errorf("%w: hasher", ErrMissingDependency)
	case cfg.Blocklist == nil:
		return nil, fmt.Errorf("%w: blocklist", ErrMissingDependency)
	case cfg.Kind != installation.KindDev:
		return nil, fmt.Errorf("demo seed: %w", localuser.ErrDemoOnly)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	rnd := rand.Reader
	if cfg.Random != nil {
		rnd = cfg.Random
	}

	return &DemoSeed{cfg: cfg, now: now, rnd: rnd}, nil
}

// Run sets up the demo person and returns the new password. It makes the
// principal when it does not exist, revokes each grant outside the fixed set,
// sets a new password, and gives the admin role on each namespace in
// namespaces and the viewer role on the installation. The revoke runs before
// the password and the required grants, so a revoke that fails leaves both
// unchanged. A grant that exists stays. A second call sets a new password and
// keeps the one person.
func (d *DemoSeed) Run(ctx context.Context, namespaces []namespace.Name) (string, error) {
	// Defense in depth: the constructor and the store check the kind too. This
	// check stays so that a seed built before a change of the kind never runs.
	if d.cfg.Kind != installation.KindDev {
		return "", fmt.Errorf("demo seed: %w", localuser.ErrDemoOnly)
	}

	person, err := d.person(ctx)
	if err != nil {
		return "", err
	}

	subject := access.Subject{Kind: access.SubjectPrincipal, ID: person.ID}

	fixed := map[string]bool{access.RoleViewer + " " + identity.ServerObject().String(): true}

	for _, name := range namespaces {
		fixed[access.RoleAdmin+" "+identity.NamespaceObject(name).String()] = true
	}

	if revokeErr := d.revokeExtra(ctx, subject, fixed); revokeErr != nil {
		return "", revokeErr
	}

	password, err := d.newPassword(ctx)
	if err != nil {
		return "", err
	}

	hash, err := d.cfg.Hasher.Hash(ctx, password, localuser.DefaultHashParams())
	if err != nil {
		return "", fmt.Errorf("demo seed: hash the password: %w", err)
	}

	seeded := d.entry(audit.ServerScope, ActionDemoSeeded, "local-user:"+localuser.DemoUserName)
	if err := d.cfg.Passwords.SeedDemoPassword(ctx, hash, seeded); err != nil {
		return "", fmt.Errorf("demo seed: set the password: %w", err)
	}

	if err := d.grant(ctx, subject, access.RoleViewer, identity.ServerObject()); err != nil {
		return "", err
	}

	for _, name := range namespaces {
		if err := d.grant(ctx, subject, access.RoleAdmin, identity.NamespaceObject(name)); err != nil {
			return "", err
		}
	}

	return password, nil
}

// revokeExtra revokes each current grant of the demo person that is not in the
// fixed set, for example a site-administrator grant that someone added after
// an earlier start. Each revoke writes an audit entry that names the grant and
// gives the reason DemoResetReason. A revoke that fails stops the seed: the
// demo person must not keep a grant outside the fixed set.
func (d *DemoSeed) revokeExtra(ctx context.Context, subject access.Subject, fixed map[string]bool) error {
	current, err := d.cfg.Grants.GrantsOf(ctx, subject)
	if err != nil {
		return fmt.Errorf("demo seed: read the grants of the demo person: %w", err)
	}

	for i := range current {
		g := current[i]
		if fixed[g.Role+" "+g.Object.String()] {
			continue
		}

		var fence namespace.Fence

		scope := audit.ServerScope

		if name := g.Object.Namespace(); name != "" {
			ns, nsErr := d.cfg.Namespaces.GetNamespace(ctx, name)
			if nsErr != nil {
				return fmt.Errorf("demo seed: read the namespace %s: %w", name, nsErr)
			}

			fence, scope = ns.Fence(), name
		}

		e := d.entry(scope, string(identity.ActionServerManageAccess), access.GrantRef(g))
		e.Reason = DemoResetReason

		if err := d.cfg.Grants.RevokeGrant(ctx, g.ID, fence, e, nil); err != nil {
			return fmt.Errorf("demo seed: revoke the grant %s: %w", g.ID, err)
		}
	}

	return nil
}

// person returns the principal of the demo person and makes it when it does
// not exist.
func (d *DemoSeed) person(ctx context.Context) (principal.Principal, error) {
	person, _, err := d.cfg.Principals.SeedDemoPerson(ctx,
		d.entry(audit.ServerScope, string(identity.ActionServerManagePrincipals), "principal:"+localuser.DemoSubject),
		DemoDisplayName)
	if err != nil {
		return principal.Principal{}, fmt.Errorf("demo seed: the principal: %w", err)
	}

	return person, nil
}

// grant gives role on obj to subject. A grant that exists is not an error.
func (d *DemoSeed) grant(ctx context.Context, subject access.Subject, role string, obj identity.Object) error {
	var fence namespace.Fence

	scope := audit.ServerScope

	if name := obj.Namespace(); name != "" {
		ns, err := d.cfg.Namespaces.GetNamespace(ctx, name)
		if err != nil {
			return fmt.Errorf("demo seed: read the namespace %s: %w", name, err)
		}

		fence, scope = ns.Fence(), name
	}

	g := access.Grant{
		CreatedAt: d.now().UTC().Truncate(time.Millisecond), Condition: nil, Object: obj,
		ID: ident.NewID(access.GrantIDPrefix), Role: role, CreatedBy: DemoCreator, Subject: subject,
	}

	e := d.entry(scope, string(identity.ActionServerManageAccess), access.GrantRef(g))

	err := d.cfg.Grants.CreateGrant(ctx, g, fence, e, nil)
	if err != nil && !errors.Is(err, access.ErrAlreadyExists) {
		return fmt.Errorf("demo seed: grant %s on %s: %w", role, obj.String(), err)
	}

	return nil
}

// entry builds the audit entry of one change of the seed.
func (d *DemoSeed) entry(scope namespace.Name, action, object string) audit.Entry {
	return audit.Entry{
		At: d.now().UTC().Truncate(time.Millisecond), Namespace: scope, Subject: DemoCreator,
		IDPSubject: "", TokenID: "", Action: action, Object: object, Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelStartup, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: DemoReason,
	}
}

// newPassword makes a demo password: DemoPasswordGroups groups of
// DemoPasswordGroupLength lowercase letters and digits, joined by hyphens. It
// checks the password against the rules of a password that a person sets, and
// makes a new one when a rule refuses it.
func (d *DemoSeed) newPassword(ctx context.Context) (string, error) {
	policy := Policy{
		EnabledSource: SourceDefault, ResetLifetime: localuser.DefaultResetLifetime,
		MinLength: localuser.DefaultMinLength, Enabled: true, SSO: false, RequireLettersAndDigits: true,
	}
	words := append(productNames(), localuser.DemoUserName, DemoDisplayName)

	for range demoPasswordTries {
		password, err := d.randomPassword()
		if err != nil {
			return "", err
		}

		_, err = checkNewPassword(ctx, d.cfg.Blocklist, password, policy, words)
		if err == nil {
			return password, nil
		}

		if !errors.Is(err, localuser.ErrPasswordBlocked) && !errors.Is(err, localuser.ErrPasswordLettersAndDigits) {
			return "", fmt.Errorf("demo seed: check the password: %w", err)
		}
	}

	return "", fmt.Errorf("demo seed: %w", errDemoPassword)
}

// randomPassword makes one password from the random source.
func (d *DemoSeed) randomPassword() (string, error) {
	groups := make([]string, localuser.DemoPasswordGroups)
	raw := make([]byte, 1)

	for g := range groups {
		group := make([]byte, 0, localuser.DemoPasswordGroupLength)

		for len(group) < localuser.DemoPasswordGroupLength {
			if _, err := io.ReadFull(d.rnd, raw); err != nil {
				return "", fmt.Errorf("demo seed: make the password: %w", err)
			}

			if raw[0] >= demoRandomLimit {
				continue
			}

			group = append(group, demoPasswordAlphabet[int(raw[0])%len(demoPasswordAlphabet)])
		}

		groups[g] = string(group)
	}

	return strings.Join(groups, "-"), nil
}
