package accesstoken

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// HostFirstTokenWindow is the time after the creation of a person in which
// the host command may issue its first token. The host command makes the
// person and the token in one run, so a person older than this window is
// not the person of the run.
const HostFirstTokenWindow = 5 * time.Minute

// HostRecovery is one recovery of the host command: a new person, its
// site-administrator grant on the installation, its temporary
// administrator record and its first token, each with the audit entry of
// its create.
type HostRecovery struct {
	Person      principal.Principal
	PersonEntry audit.Entry
	Grant       access.Grant
	GrantEntry  audit.Entry
	Record      tempadmin.TempAdmin
	RecordEntry audit.Entry
	Token       Token
	Digest      Digest
	TokenEntry  audit.Entry
}

// HostStore is the persistence port of the host issuer. Each method writes
// in one transaction and appends its audit entries in that transaction:
// all commit, or nothing does. An entry that fails validation gives
// audit.ErrInvalidEntry and changes nothing.
type HostStore interface {
	// CreateFirstHostToken persists the token t with the digest d and
	// appends e. It inserts t only when all of these hold at t.CreatedAt:
	// the principal of t is a live, enabled person of the installation,
	// made at most HostFirstTokenWindow before t.CreatedAt and not after
	// it; a temporary administrator record names the principal and works
	// past t.CreatedAt and at least to t.ExpiresAt; and the principal holds
	// no token of any status. Else it returns ErrNotOwnToken and changes
	// nothing. Of two concurrent calls for one principal, at most one
	// succeeds.
	CreateFirstHostToken(ctx context.Context, t Token, d Digest, e audit.Entry) error
	// CreateHostRecovery persists the person, the grant, the record and
	// the token of r, and appends their four entries. It returns
	// principal.ErrAlreadyExists when a principal holds the subject or the
	// ID of the person, and ErrNotOwnToken when the token fails the checks
	// of CreateFirstHostToken.
	CreateHostRecovery(ctx context.Context, r *HostRecovery) error
}

// HostIssuer issues the first token of a temporary administrator that the
// host command creates. It is a narrow exception to the own-token rule:
// recovery has no sign-in, so the person cannot make its own first token.
//
// Only the host command wiring builds a HostIssuer. No HTTP route builds
// one, and the normal Create path has no flag that reaches it. Every other
// caller keeps ErrNotOwnToken.
type HostIssuer struct {
	svc   *Service
	store HostStore
}

// NewHostIssuer returns the issuer of the host command over svc and store.
// It returns ErrNoTempAdminReader when svc has no temporary administrator
// reader: without the record the issuer cannot prove that the person is a
// temporary administrator, so it fails closed. It returns
// ErrMissingDependency when store is nil.
func NewHostIssuer(svc *Service, store HostStore) (*HostIssuer, error) {
	if svc == nil || svc.tempAdmins == nil {
		return nil, ErrNoTempAdminReader
	}

	if store == nil {
		return nil, fmt.Errorf("%w: host store", ErrMissingDependency)
	}

	return &HostIssuer{svc: svc, store: store}, nil
}

// ErrNoTempAdminReader reports a HostIssuer over a Service that has no
// temporary administrator reader.
var ErrNoTempAdminReader = errors.New("the host issuer needs a temporary administrator reader")

// HostInput names one recovery. Subject is the name of the person, and it
// must name no principal. Reason is the text that the host operator gave.
// CreatedBy names the host operator that runs the host command.
type HostInput struct {
	Subject   string
	Reason    string
	CreatedBy string
}

// HostResult is the result of a recovery: the new person, the ID of its
// grant, its record, and its token with the secret, shown once.
type HostResult struct {
	Person  principal.Principal
	GrantID string
	Record  tempadmin.TempAdmin
	Token   Token
	Secret  Secret
}

// Recover makes the person in.Subject a temporary administrator in one
// store transaction: the person, its site-administrator grant on the
// installation, its record and its first token. The record and the token
// share one expiry: tempadmin.Lifetime after now, or the maximum lifetime
// of the token policy when that is shorter. The entry e names the host as
// the subject. Each create gets its own copy of e with its action and
// object, and the entry of the record holds the reason. A crash leaves
// all four, or none.
func (h *HostIssuer) Recover(ctx context.Context, e audit.Entry, in HostInput) (HostResult, error) {
	r, secret, err := h.recovery(ctx, e, in, h.svc.Now())
	if err != nil {
		return HostResult{}, err
	}

	if err := h.store.CreateHostRecovery(ctx, r); err != nil {
		if errors.Is(err, ErrNotOwnToken) {
			return HostResult{}, hostRefusal("the store refused the first token")
		}

		return HostResult{}, fmt.Errorf("create temporary administrator %q: %w", in.Subject, err)
	}

	return HostResult{Person: r.Person, GrantID: r.Grant.ID, Record: r.Record, Token: r.Token, Secret: secret}, nil
}

// IssueFirstToken issues the first personal token of the principal
// principalID, with the expiry expires and the audit entry e. The store
// checks the person, its record and its tokens in the transaction of the
// insert (see HostStore.CreateFirstHostToken). It refuses with
// identity.ErrDenied wrapping ErrNotOwnToken when a check fails or when
// the expiry does not lie after now and at most tempadmin.Lifetime from
// now.
//
// The token carries the name tempadmin.HostTokenName and the creator
// tempadmin.HostSubject. The audit entry e records the creation of the
// token in the same transaction as the token.
func (h *HostIssuer) IssueFirstToken(
	ctx context.Context, e audit.Entry, principalID string, expires time.Time,
) (Token, Secret, error) {
	s := h.svc

	if err := s.requireFresh(ctx, e.Subject); err != nil {
		return Token{}, Secret{}, err
	}

	at := s.Now()

	if want := at.Add(tempadmin.Lifetime); !expires.After(at) || expires.After(want) {
		return Token{}, Secret{}, hostRefusal(fmt.Sprintf(
			"the expiry must be after now and at most %s from now", tempadmin.Lifetime,
		))
	}

	expiresAt, err := s.expiry(ctx, at, expires)
	if err != nil {
		return Token{}, Secret{}, err
	}

	n, err := s.newToken(tokenSpec{
		at:          at,
		expiresAt:   expiresAt,
		principalID: principalID,
		name:        tempadmin.HostTokenName,
		kind:        KindPersonal,
		createdBy:   tempadmin.HostSubject,
	})
	if err != nil {
		return Token{}, Secret{}, err
	}

	err = h.store.CreateFirstHostToken(ctx, n.token, n.digest, change(e, at, TokenRef(principalID, n.token.ID)))
	if errors.Is(err, ErrNotOwnToken) {
		return Token{}, Secret{}, hostRefusal(
			"the person is not a new, enabled, working temporary administrator with no token",
		)
	}

	if err != nil {
		return Token{}, Secret{}, fmt.Errorf("create host access token: %w", err)
	}

	return n.token, n.secret, nil
}

// recovery builds the person, the grant, the record and the token of a
// recovery at the time at, with their entries, and checks each. It answers
// the secret of the token too.
func (h *HostIssuer) recovery(
	ctx context.Context, e audit.Entry, in HostInput, at time.Time,
) (*HostRecovery, Secret, error) {
	s := h.svc

	if err := principal.ValidateSubject(in.Subject); err != nil {
		return nil, Secret{}, fmt.Errorf("create temporary administrator: %w", err)
	}

	lifetime, err := s.MaxLifetime(ctx)
	if err != nil {
		return nil, Secret{}, fmt.Errorf("read token policy: %w", err)
	}

	if lifetime <= 0 {
		return nil, Secret{}, fmt.Errorf("%w: the maximum lifetime of the token policy is not positive", ErrInvalid)
	}

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{},
		ID: s.newID(principal.IDPrefix), Subject: in.Subject, DisplayName: in.Subject,
		Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	if validErr := p.Validate(); validErr != nil {
		return nil, Secret{}, fmt.Errorf("create temporary administrator: %w", validErr)
	}

	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: s.newID(access.GrantIDPrefix),
		Role: access.RoleSiteAdministrator, CreatedBy: tempadmin.HostSubject,
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: p.ID},
	}

	rec := tempadmin.TempAdmin{
		CreatedAt: at, ExpiresAt: at.Add(min(tempadmin.Lifetime, lifetime)),
		PrincipalID: p.ID, Subject: in.Subject, CreatedBy: in.CreatedBy, Reason: in.Reason,
	}

	if validErr := rec.Validate(); validErr != nil {
		return nil, Secret{}, fmt.Errorf("create temporary administrator: %w", validErr)
	}

	n, err := s.newToken(tokenSpec{
		at: at, expiresAt: rec.ExpiresAt, principalID: p.ID,
		name: tempadmin.HostTokenName, kind: KindPersonal, createdBy: tempadmin.HostSubject,
	})
	if err != nil {
		return nil, Secret{}, err
	}

	recordEntry := change(withAction(e, tempadmin.Action), at, tempadmin.Ref(p.ID))
	recordEntry.Changes = append(slices.Clone(recordEntry.Changes), audit.Change{
		Key: "reason", Old: nil, New: in.Reason, Layer: "", TakesEffect: "",
	})

	return &HostRecovery{
		Person:      p,
		PersonEntry: change(withAction(e, string(identity.ActionServerManagePrincipals)), at, principal.Ref(p.ID)),
		Grant:       g,
		GrantEntry:  change(withAction(e, string(identity.ActionServerManageAccess)), at, access.GrantRef(g)),
		Record:      rec,
		RecordEntry: recordEntry,
		Token:       n.token,
		Digest:      n.digest,
		TokenEntry: change(
			withAction(e, string(identity.ActionServerManageTokens)), at, TokenRef(p.ID, n.token.ID),
		),
	}, n.secret, nil
}

// withAction returns e with the action action.
func withAction(e audit.Entry, action string) audit.Entry {
	e.Action = action

	return e
}

// hostRefusal returns the refusal of the host issuer for the reason why:
// the same denial as a create of the token of another person.
func hostRefusal(why string) error {
	return fmt.Errorf("%w: %w: the host issues only the first token of a new temporary administrator: %s",
		identity.ErrDenied, ErrNotOwnToken, why)
}
