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
	// CreateHostRecovery persists the person, the grant, the record and
	// the token of r, and appends their four entries. It returns
	// principal.ErrAlreadyExists when a principal holds the subject or the
	// ID of the person. It inserts the token only when all of these hold
	// at the time of the token: the principal is a live, enabled person of
	// the installation, made at most HostFirstTokenWindow before the token
	// and not after it; the record names the principal and works past the
	// time and at least to the expiry of the token; and the principal holds
	// no token of any status. Else it returns ErrNotOwnToken and changes
	// nothing. Of two concurrent calls for one subject, at most one
	// succeeds.
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

	rec := hostRecord(in, p.ID, at, lifetime)

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

// hostRecord returns the temporary administrator record of the principal id,
// made at the time at. The record ends with the token policy maximum when
// that is below tempadmin.Lifetime.
func hostRecord(in HostInput, id string, at time.Time, lifetime time.Duration) tempadmin.TempAdmin {
	return tempadmin.TempAdmin{
		CreatedAt:      at,
		ExpiresAt:      at.Add(min(tempadmin.Lifetime, lifetime)),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    id,
		Subject:        in.Subject,
		CreatedBy:      in.CreatedBy,
		Reason:         in.Reason,
	}
}
