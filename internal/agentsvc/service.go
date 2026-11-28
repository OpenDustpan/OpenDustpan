// Package agentsvc is the service of the model connection, its secret items
// and the egress allowlist of the installation. It checks the action of the
// caller on @server at the point of use, checks each input again, and gives
// each change an audit entry of the kind change on @server. The store appends
// that entry in the transaction of the change. No entry holds a secret value.
package agentsvc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
)

// The objects of the audit entries of the service.
const (
	// ObjectSecretItems is the object of the entry of a new secret item. The
	// store makes the reference in the transaction, so the entry names the
	// list of the items and the name of the new item.
	ObjectSecretItems = "agent-secret-items"
	// ObjectConnection is the object of the entry of a disconnect.
	ObjectConnection = "agent-connection"
	// ObjectEgressAllowlist is the object of the entry of an allowlist change.
	ObjectEgressAllowlist = "egress-allowlist"
)

// maxSecretRefBytes is the largest length of a secret reference that the
// service accepts.
const maxSecretRefBytes = 64

// The wire formats that the server can use for a model connection. The
// format responses parses but is not supported yet.
const (
	formatChatCompletions = "chat-completions"
	formatMessages        = "messages"
)

// ErrUnknownAction reports an action that the service does not check.
var ErrUnknownAction = errors.New("agentsvc: unknown action")

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("agentsvc: missing dependency")

// ErrInvalidRef reports a secret reference that does not have the form
// itm-....
var ErrInvalidRef = errors.New("the secret reference is not valid: use a reference that GET /api/agent/secrets gives")

// Config holds the ports of a Service. Every port is required.
type Config struct {
	// Secrets keeps the sealed secret items of @server.
	Secrets agent.SecretItems
	// Connection keeps the current and the pending connection document.
	Connection agent.ConnectionStore
	// Allowlist keeps the egress allowlist of the installation.
	Allowlist agent.EgressAllowlistStore
	// Authorizer checks the action of the caller at the point of use.
	Authorizer identity.Authorizer
	// Now returns the time of an audit entry. Nil means time.Now.
	Now func() time.Time
}

// Service is the service of the model connection, its secret items and the
// egress allowlist.
type Service struct {
	secrets    agent.SecretItems
	connection agent.ConnectionStore
	allowlist  agent.EgressAllowlistStore
	authz      identity.Authorizer
	now        func() time.Time
}

// Connection is the stored connection document and its version.
type Connection struct {
	// Spec is the document. It holds references to secret items only.
	Spec agent.ConnectionSpec
	// Version is the version of the stored document.
	Version int64
}

// New returns a Service. It returns ErrMissingDependency for a Config
// without a port.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Secrets == nil:
		return nil, fmt.Errorf("%w: secret items", ErrMissingDependency)
	case cfg.Connection == nil:
		return nil, fmt.Errorf("%w: connection store", ErrMissingDependency)
	case cfg.Allowlist == nil:
		return nil, fmt.Errorf("%w: egress allowlist store", ErrMissingDependency)
	case cfg.Authorizer == nil:
		return nil, fmt.Errorf("%w: authorizer", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		secrets: cfg.Secrets, connection: cfg.Connection, allowlist: cfg.Allowlist, authz: cfg.Authorizer, now: now,
	}, nil
}

// SecretItemObject returns the object of the audit entry of the secret item
// ref.
func SecretItemObject(ref agent.SecretRef) string {
	return ObjectSecretItems + "/" + string(ref)
}

// CheckSecretRef returns ErrInvalidRef unless ref has the form itm-... and
// at most 64 bytes.
func CheckSecretRef(ref agent.SecretRef) error {
	if len(ref) > maxSecretRefBytes || !agent.IsSecretRef(string(ref)) {
		return ErrInvalidRef
	}

	return nil
}

// Authorize checks action for p on @server. It returns ErrUnknownAction for
// an action other than server:manage-agent and server:manage-settings, and an
// error that wraps identity.ErrDenied when the authorizer refuses.
func (s *Service) Authorize(ctx context.Context, p identity.Principal, action identity.Action) error {
	if action != identity.ActionServerManageAgent && action != identity.ActionServerManageSettings {
		return fmt.Errorf("%w: %s", ErrUnknownAction, action)
	}

	if err := s.authz.Authorize(ctx, p, action, identity.ServerObject()); err != nil {
		return fmt.Errorf("authorize %s: %w", action, err)
	}

	return nil
}

// CreateSecret stores value under name as a new secret item and returns its
// metadata. base names the caller of the audit entry.
func (s *Service) CreateSecret(
	ctx context.Context, p identity.Principal, base audit.Entry, name string, value []byte,
) (agent.SecretItem, error) {
	if err := s.Authorize(ctx, p, identity.ActionServerManageAgent); err != nil {
		return agent.SecretItem{}, err
	}

	// Defense in depth: the handler and the store check this too; this check
	// stays so that a new caller cannot skip it.
	if err := agent.CheckSecretItem(name, value); err != nil {
		return agent.SecretItem{}, err
	}

	e := s.entry(base, identity.ActionServerManageAgent, ObjectSecretItems)
	e.Changes = append(e.Changes, audit.Change{Key: "name", Old: nil, New: name, Layer: "", TakesEffect: ""})

	item, err := s.secrets.Create(ctx, name, value, e)
	if err != nil {
		return agent.SecretItem{}, fmt.Errorf("create secret item: %w", err)
	}

	return item, nil
}

// ListSecrets returns the metadata of every secret item, without a value.
func (s *Service) ListSecrets(ctx context.Context, p identity.Principal) ([]agent.SecretItem, error) {
	if err := s.Authorize(ctx, p, identity.ActionServerManageAgent); err != nil {
		return nil, err
	}

	items, err := s.secrets.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list secret items: %w", err)
	}

	return items, nil
}

// ReplaceSecret sets the value of the secret item ref and returns its
// metadata.
func (s *Service) ReplaceSecret(
	ctx context.Context, p identity.Principal, base audit.Entry, ref agent.SecretRef, value []byte,
) (agent.SecretItem, error) {
	if err := s.Authorize(ctx, p, identity.ActionServerManageAgent); err != nil {
		return agent.SecretItem{}, err
	}

	// Defense in depth: the handler checks the reference and the value too;
	// this check stays so that a new caller cannot skip it.
	if err := CheckSecretRef(ref); err != nil {
		return agent.SecretItem{}, err
	}

	// The item keeps its name, so only the value rules apply here. Defense in
	// depth: the handler and the store check the value too; this check stays
	// so that a new caller cannot skip it.
	if err := agent.CheckSecretValue(value); err != nil {
		return agent.SecretItem{}, err
	}

	item, err := s.secrets.Replace(
		ctx, ref, value, s.entry(base, identity.ActionServerManageAgent, SecretItemObject(ref)),
	)
	if err != nil {
		return agent.SecretItem{}, fmt.Errorf("replace secret item: %w", err)
	}

	return item, nil
}

// DeleteSecret removes the secret item ref. It returns agent.ErrSecretInUse
// while a stored document refers to it.
func (s *Service) DeleteSecret(ctx context.Context, p identity.Principal, base audit.Entry, ref agent.SecretRef) error {
	if err := s.Authorize(ctx, p, identity.ActionServerManageAgent); err != nil {
		return err
	}

	// Defense in depth: the handler checks the reference too; this check stays
	// so that a new caller cannot skip it.
	if err := CheckSecretRef(ref); err != nil {
		return err
	}

	entry := s.entry(base, identity.ActionServerManageAgent, SecretItemObject(ref))
	if err := s.secrets.Delete(ctx, ref, entry); err != nil {
		return fmt.Errorf("delete secret item: %w", err)
	}

	return nil
}

// Connection returns the current connection document and its version. It
// returns agent.ErrDisabled when no document exists.
func (s *Service) Connection(ctx context.Context, p identity.Principal) (Connection, error) {
	if err := s.Authorize(ctx, p, identity.ActionServerManageAgent); err != nil {
		return Connection{Spec: agent.ConnectionSpec{}, Version: 0}, err
	}

	spec, version, err := s.connection.Current(ctx)
	if err != nil {
		return Connection{Spec: agent.ConnectionSpec{}, Version: 0}, fmt.Errorf("read connection: %w", err)
	}

	return Connection{Spec: spec, Version: version}, nil
}

// PendingAll returns every pending connection document with its probe,
// oldest first.
func (s *Service) PendingAll(ctx context.Context, p identity.Principal) ([]agent.PendingDocument, error) {
	if err := s.Authorize(ctx, p, identity.ActionServerManageAgent); err != nil {
		return nil, err
	}

	list, err := s.connection.PendingAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending connections: %w", err)
	}

	return list, nil
}

// Disconnect removes the current document, each pending document and the
// reference rows in one transaction. It returns agent.ErrDisabled when no
// document exists.
func (s *Service) Disconnect(ctx context.Context, p identity.Principal, base audit.Entry) error {
	if err := s.Authorize(ctx, p, identity.ActionServerManageAgent); err != nil {
		return err
	}

	if err := s.connection.Delete(ctx, s.entry(base, identity.ActionServerManageAgent, ObjectConnection)); err != nil {
		return fmt.Errorf("delete connection: %w", err)
	}

	return nil
}

// Formats returns the wire formats that a connection document can name now.
func (s *Service) Formats(ctx context.Context, p identity.Principal) ([]string, error) {
	if err := s.Authorize(ctx, p, identity.ActionServerManageAgent); err != nil {
		return nil, err
	}

	return []string{formatChatCompletions, formatMessages}, nil
}

// Allowlist returns the egress allowlist of the installation.
func (s *Service) Allowlist(ctx context.Context, p identity.Principal) (egress.Allowlist, error) {
	if err := s.Authorize(ctx, p, identity.ActionServerManageSettings); err != nil {
		return egress.Allowlist{Hosts: nil, CIDRs: nil}, err
	}

	a, err := s.allowlist.Get(ctx)
	if err != nil {
		return egress.Allowlist{Hosts: nil, CIDRs: nil}, fmt.Errorf("read egress allowlist: %w", err)
	}

	return a, nil
}

// SetAllowlist replaces the egress allowlist of the installation. It
// returns agent.ErrInvalid for an allowlist that agent.CheckAllowlist
// refuses, and an error that wraps egress.ErrRangeTooWide for a rule that
// starts in an address that no rule may open.
func (s *Service) SetAllowlist(ctx context.Context, p identity.Principal, base audit.Entry, a egress.Allowlist) error {
	if err := s.Authorize(ctx, p, identity.ActionServerManageSettings); err != nil {
		return err
	}

	// Defense in depth: the handler and the store check the allowlist too;
	// this check stays so that a new caller cannot skip it.
	if err := agent.CheckAllowlist(a); err != nil {
		return err
	}

	// Defense in depth: the handler checks each range too; this check stays so
	// that a new caller cannot store a range that no rule may open.
	for _, r := range a.CIDRs {
		if err := egress.CheckAllowedRange(r.CIDR); err != nil {
			return fmt.Errorf("%w: %w", agent.ErrInvalid, err)
		}
	}

	e := s.entry(base, identity.ActionServerManageSettings, ObjectEgressAllowlist)
	e.Changes = append(e.Changes,
		audit.Change{Key: "hostCount", Old: nil, New: strconv.Itoa(len(a.Hosts)), Layer: "", TakesEffect: ""},
		audit.Change{Key: "ruleCount", Old: nil, New: strconv.Itoa(len(a.CIDRs)), Layer: "", TakesEffect: ""},
	)

	if err := s.allowlist.Set(ctx, a, e); err != nil {
		return fmt.Errorf("set egress allowlist: %w", err)
	}

	return nil
}

// entry returns the audit entry of a change: base with the time, the scope
// @server, the kind change, the outcome success, the action and the object.
// It copies the changes of base, so the caller can add to them.
func (s *Service) entry(base audit.Entry, action identity.Action, object string) audit.Entry {
	e := base
	e.At = s.now()
	e.Namespace = audit.ServerScope
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Action = string(action)
	e.Object = object
	e.Changes = append([]audit.Change(nil), base.Changes...)

	return e
}
