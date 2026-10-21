// Package sinks implements the use cases of notification sinks for the HTTP
// adapter: create, replace, delete, read and test. It adds the fence, the
// time, the IDs, the sealing of the secrets and the audit entry to the calls
// of notify.SinkStore. It needs neither the outbox nor the dispatcher: the
// test of a sink sends one fixed message through the notify.Sender of the
// sink kind.
package sinks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// ErrNoSender reports a test of a sink whose kind has no sender on this
// server.
var ErrNoSender = errors.New("this server cannot send to this kind of notification destination")

// SendTimeout bounds the send of a test message.
const SendTimeout = 10 * time.Second

// signingKeyBytes is the size of the signing key of a webhook sink, before
// its hex form.
const signingKeyBytes = 32

// Namespaces reads the namespace that a write fences. namespace.Service
// implements it. WritableHere returns namespace.ErrNotHome for a namespace
// that another region owns.
type Namespaces interface {
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
	WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Config holds the dependencies of a Service.
type Config struct {
	Store      notify.SinkStore
	Sealer     notify.SinkSealer
	Namespaces Namespaces
	// Senders send the test message, one for each sink kind that this
	// server can send. A kind with no sender cannot be tested.
	Senders []notify.Sender
	// Ports are the ports that a sink may send to. The zero value allows the
	// port 443 only.
	Ports egress.Ports
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Service implements the use cases of sinks.
type Service struct {
	store      notify.SinkStore
	sealer     notify.SinkSealer
	namespaces Namespaces
	senders    map[notify.SinkKind]notify.Sender
	ports      egress.Ports
	limiter    *testLimiter
	now        func() time.Time
}

// NewService returns a Service. It returns ErrMissingDependency when the
// store, the sealer or the namespace port is missing.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Namespaces == nil || cfg.Sealer.Namespaces == nil {
		return nil, fmt.Errorf("%w: store, sealer and namespaces", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	senders := make(map[notify.SinkKind]notify.Sender, len(cfg.Senders))
	for _, s := range cfg.Senders {
		senders[s.Kind()] = s
	}

	return &Service{
		store: cfg.Store, sealer: cfg.Sealer, namespaces: cfg.Namespaces, senders: senders, ports: cfg.Ports,
		limiter: newTestLimiter(), now: now,
	}, nil
}

// Caller is the principal who calls a use case. By is the template of the
// audit entry: the caller fills where the request came from, and the service
// fills the action, the object and the time.
type Caller struct {
	Principal identity.Principal
	By        audit.Entry
}

// New describes a sink to make. For a chat sink, URL is the incoming webhook
// URL, Platform names the chat platform, and Target stays empty: the service
// derives it from the URL. For a webhook sink, Target is the https URL of the
// receiver: the service seals it as a secret item and keeps only its host as
// the target of the sink. For an email sink, Target is the address.
type New struct {
	Kind     notify.SinkKind
	Platform string
	Name     string
	Target   string
	URL      string
}

// Created is a sink that Create made. SigningKey is the signing key of a
// webhook sink. It is set once, here, and no other call answers it.
type Created struct {
	Sink       notify.Sink
	SigningKey string
}

// Create makes a sink in ns. It returns notify.ErrInvalid, namespace.ErrNotFound
// or namespace.ErrFenced.
func (s *Service) Create(ctx context.Context, c *Caller, ns namespace.Name, in New) (Created, error) {
	sink := notify.Sink{
		ID: ident.NewID(notify.SinkIDPrefix), Scope: ns, Kind: in.Kind, Name: in.Name, Platform: in.Platform,
		Target: in.Target, SecretRefs: nil, CreatedBy: c.Principal.Subject, Version: 0,
	}

	secrets, err := s.newSecrets(&sink, in)
	if err != nil {
		return Created{}, err
	}

	if portErr := s.checkPort(&sink, secrets); portErr != nil {
		return Created{}, portErr
	}

	// Defense in depth: the store validates the sink again, and the handler checks
	// the body. This check stays so that a refused sink costs no read of the
	// namespace and no sealing, and so that a store that skips its check cannot
	// take an invalid sink from this service.
	if err = sink.ValidateWrite(); err != nil {
		return Created{}, fmt.Errorf("create sink: %w", err)
	}

	sealed, err := s.sealer.Seal(ctx, sink, secrets)
	if err != nil {
		return Created{}, fmt.Errorf("create sink: %w", err)
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return Created{}, err
	}

	if err = s.store.PutSink(ctx, sink, sealed, fence, s.entry(c, ns, sink.ID, "create")); err != nil {
		return Created{}, fmt.Errorf("create sink: %w", err)
	}

	out := Created{Sink: sink, SigningKey: ""}
	out.Sink.Version = 1

	// A webhook sink has its URL first and its signing key second.
	if sink.Kind == notify.SinkWebhook && len(secrets) == 2 {
		out.SigningKey = string(secrets[1].Value)
	}

	return out, nil
}

// NewSigningKey returns a new signing key for a webhook sink: 32 bytes from
// the system random source, as 64 lowercase hex digits. A receiver uses the
// text as the key of notify.Verify.
func NewSigningKey() ([]byte, error) {
	var b [signingKeyBytes]byte

	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("make a signing key: %w", err)
	}

	return []byte(hex.EncodeToString(b[:])), nil
}

// Change describes a replace. Version is the version that the caller read.
// Name is the new name. Target, when set, is the new address of an email
// sink, or the new https URL of a webhook sink: the service stores that URL
// as a new secret item under a new ref, and the signing keys stay. URL, when
// set, is the new incoming webhook URL of a chat sink: the service stores it
// as a new secret item under a new ref.
type Change struct {
	Version int64
	Name    string
	Target  *string
	URL     *string
}

// Replace changes the sink id in ns. It returns notify.ErrNotFound,
// notify.ErrInvalid, notify.ErrConflict, namespace.ErrNotFound or
// namespace.ErrFenced.
func (s *Service) Replace(
	ctx context.Context,
	c *Caller,
	ns namespace.Name,
	id string,
	ch Change,
) (notify.Sink, error) {
	old, err := s.Get(ctx, ns, id)
	if err != nil {
		return notify.Sink{}, err
	}

	next, secrets, err := s.changed(old, ch)
	if err != nil {
		return notify.Sink{}, err
	}

	// Defense in depth: the store validates the sink again. This check stays so
	// that a refused sink costs no sealing, and so that a store that skips its
	// check cannot take an invalid sink from this service.
	if err = next.ValidateWrite(); err != nil {
		return notify.Sink{}, fmt.Errorf("replace sink: %w", err)
	}

	sealed, err := s.sealer.Seal(ctx, next, secrets)
	if err != nil {
		return notify.Sink{}, fmt.Errorf("replace sink: %w", err)
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return notify.Sink{}, err
	}

	if err = s.store.PutSink(ctx, next, sealed, fence, s.entry(c, ns, id, "update")); err != nil {
		return notify.Sink{}, fmt.Errorf("replace sink: %w", err)
	}

	next.Version++

	return next, nil
}

// Delete removes the sink id in ns. It returns notify.ErrNotFound.
func (s *Service) Delete(ctx context.Context, c *Caller, ns namespace.Name, id string) error {
	fence, err := s.fence(ctx, ns)
	if err != nil {
		return err
	}

	if err = s.store.DeleteSink(ctx, id, fence, s.entry(c, ns, id, "delete")); err != nil {
		return fmt.Errorf("delete sink: %w", err)
	}

	return nil
}

// List returns the sinks of ns, ordered by ID. A sink holds no secret.
func (s *Service) List(ctx context.Context, ns namespace.Name) ([]notify.Sink, error) {
	if _, err := s.namespaces.Get(ctx, ns); err != nil {
		return nil, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	sinks, err := s.store.Sinks(ctx, ns)
	if err != nil {
		return nil, fmt.Errorf("list sinks: %w", err)
	}

	return sinks, nil
}

// Get returns the sink id of ns, or notify.ErrNotFound.
func (s *Service) Get(ctx context.Context, ns namespace.Name, id string) (notify.Sink, error) {
	sinks, err := s.List(ctx, ns)
	if err != nil {
		return notify.Sink{}, err
	}

	for i := range sinks {
		if sinks[i].ID == id {
			return sinks[i], nil
		}
	}

	return notify.Sink{}, fmt.Errorf("get sink %s: %w", id, notify.ErrNotFound)
}

// changed returns the sink that applies ch to old, and the new secret items
// that the change makes. It returns notify.ErrInvalid for a field that the
// kind of the sink does not take, and for an address on a port that the
// operator did not allow.
func (s *Service) changed(old notify.Sink, ch Change) (notify.Sink, []notify.SinkSecret, error) {
	next := old
	next.Version, next.Name = ch.Version, ch.Name

	next.SecretRefs = slices.Clone(old.SecretRefs)

	switch {
	case ch.Target != nil && old.Kind == notify.SinkChat:
		return notify.Sink{}, nil, fmt.Errorf(
			"%w: the target of a chat sink follows its URL: send the url",
			notify.ErrInvalid,
		)
	case ch.URL != nil && old.Kind != notify.SinkChat:
		return notify.Sink{}, nil, fmt.Errorf("%w: only a chat sink takes a url", notify.ErrInvalid)
	case ch.Target != nil && old.Kind == notify.SinkWebhook:
		return s.withNewURL(next, *ch.Target, notify.WebhookHost, old.SecretRefs[min(1, len(old.SecretRefs)):])
	case ch.Target != nil:
		next.Target = *ch.Target
	case ch.URL != nil:
		return s.withNewURL(next, *ch.URL, notify.ChatHost, nil)
	default:
		// A change of the name has no new URL.
	}

	// A change of the name or of an email address has no new URL, so no new
	// item meets the port rule: the address that the owner approved stays.
	return next, nil, nil
}

// withNewURL returns next with the URL raw as its new first secret item, then
// the refs keys of the items that stay, and the host of the URL as the target.
// hostOf reads the host. A webhook sink keeps its signing keys, so a receiver
// keeps its key when the URL changes. A chat sink keeps no other item. It
// returns the new item, and notify.ErrInvalid when the port rule or hostOf
// refuses the URL.
func (s *Service) withNewURL(
	next notify.Sink, raw string, hostOf func(string) (string, error), keys []string,
) (notify.Sink, []notify.SinkSecret, error) {
	host, err := hostOf(raw)
	if err != nil {
		return notify.Sink{}, nil, fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	ref := ident.NewID("itm-")
	next.Target, next.SecretRefs = host, slices.Concat([]string{ref}, keys)

	secrets := []notify.SinkSecret{{Ref: ref, Value: []byte(raw)}}

	if portErr := s.checkPort(&next, secrets); portErr != nil {
		return notify.Sink{}, nil, portErr
	}

	return next, secrets, nil
}

// newSecrets returns the new secret items of a new sink, and sets its target
// and its refs. A chat sink has its URL. A webhook sink has its URL, then a
// new signing key. An email sink has none. The caller owns the bytes of the
// values.
func (s *Service) newSecrets(sink *notify.Sink, in New) ([]notify.SinkSecret, error) {
	// Each kind takes the fields it needs and refuses the others, so a field
	// that a kind ignores never hides a mistake of the caller.
	if sink.Kind != notify.SinkChat && in.URL != "" {
		return nil, fmt.Errorf("%w: only a chat sink takes a url", notify.ErrInvalid)
	}

	if sink.Kind == notify.SinkChat && in.Target != "" {
		return nil, fmt.Errorf("%w: a chat sink takes a url, not a target", notify.ErrInvalid)
	}

	switch sink.Kind {
	case notify.SinkWebhook:
		return webhookSecrets(sink, in.Target)
	case notify.SinkChat:
		return s.chatSecrets(sink, in.URL)
	case notify.SinkEmail:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: unknown sink kind %q", notify.ErrInvalid, sink.Kind)
	}
}

// webhookSecrets sets the target and the refs of a new webhook sink and
// returns its URL item and a new signing key.
func webhookSecrets(sink *notify.Sink, raw string) ([]notify.SinkSecret, error) {
	host, err := notify.WebhookHost(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	key, err := NewSigningKey()
	if err != nil {
		return nil, err
	}

	sink.Target = host
	sink.SecretRefs = []string{ident.NewID("itm-"), ident.NewID("itm-")}

	return []notify.SinkSecret{
		{Ref: sink.SecretRefs[0], Value: []byte(raw)},
		{Ref: sink.SecretRefs[1], Value: key},
	}, nil
}

// chatSecrets sets the target and the ref of a new chat sink and returns its
// URL item.
func (s *Service) chatSecrets(sink *notify.Sink, raw string) ([]notify.SinkSecret, error) {
	host, err := notify.ChatHost(raw)
	if err != nil {
		if !errors.Is(err, notify.ErrInvalid) {
			err = fmt.Errorf("%w: %w", notify.ErrInvalid, err)
		}

		return nil, err
	}

	if hostErr := s.checkPlatformHost(*sink, host); hostErr != nil {
		return nil, hostErr
	}

	sink.Target = host
	sink.SecretRefs = []string{ident.NewID("itm-")}

	return []notify.SinkSecret{{Ref: sink.SecretRefs[0], Value: []byte(raw)}}, nil
}

// ErrPlatformHost reports a chat URL whose host the platform of the sink does
// not use for its webhooks. It also matches notify.ErrInvalid.
var ErrPlatformHost = errors.New("the URL host is not a webhook host of the platform")

// platformHostError is the error of a refused host. It matches
// notify.ErrInvalid and ErrPlatformHost through Is, so that ErrInvalid is in
// the chain and in the text only once.
type platformHostError struct{ rule string }

func (e platformHostError) Error() string {
	return notify.ErrInvalid.Error() + ": " + ErrPlatformHost.Error()
}

func (platformHostError) Is(target error) bool {
	return target == ErrPlatformHost || target == notify.ErrInvalid
}

// PlatformHostRule returns the host rule that err carries when err matches
// ErrPlatformHost, and an empty string otherwise. The rule is a fixed
// sentence of the platform and never holds the host of the URL.
func PlatformHostRule(err error) string {
	if pe, ok := errors.AsType[platformHostError](err); ok {
		return pe.rule
	}

	return ""
}

// checkPlatformHost returns ErrPlatformHost, which matches notify.ErrInvalid, when the sender of chat sinks
// says that host is not a webhook host of the platform of sink, so a person
// who pastes the wrong URL learns it when the sink is saved and not at the
// first message. A sender that has no such check, or no adapter for the
// platform, accepts the host. Defense in depth: the sender checks the host
// again before each send.
func (s *Service) checkPlatformHost(sink notify.Sink, host string) error {
	checker, ok := s.senders[notify.SinkChat].(interface {
		CheckHost(platform, host string) error
	})
	if !ok {
		return nil
	}

	if err := checker.CheckHost(sink.Platform, host); err != nil {
		var rule interface{ HostRule() string }
		if !errors.As(err, &rule) {
			return platformHostError{rule: ""}
		}

		return platformHostError{rule: rule.HostRule()}
	}

	return nil
}

// checkPort returns notify.ErrInvalid when the sink would send to a port that
// the operator did not allow. secrets are the new secret items of the write:
// the first one of a chat sink or of a webhook sink is its URL. An email sink
// has no port, and a sink with no new URL keeps the address that the owner
// approved.
//
// Defense in depth: the sender checks the port again before it sends, and
// Service.Test checks it before the test. This check stays so that a refused
// port is a refused write and not a sink that fails at the first send. The
// store cannot hold this check: the ports are a setting of the operator.
func (s *Service) checkPort(sink *notify.Sink, secrets []notify.SinkSecret) error {
	if sink.Kind == notify.SinkEmail || len(secrets) == 0 || len(sink.SecretRefs) == 0 ||
		secrets[0].Ref != sink.SecretRefs[0] {
		return nil
	}

	if err := egress.CheckPort(string(secrets[0].Value), s.ports); err != nil {
		return fmt.Errorf("%w: %w", notify.ErrInvalid, err)
	}

	return nil
}

// fence returns the fence of ns. It returns namespace.ErrNotHome when the
// namespace is homed in another region, so that no write and no test of a
// sink runs in a region that does not own the namespace.
func (s *Service) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return n.Fence(), nil
}

// entry makes the audit entry of one write. It names the sink by ID and
// holds no name, URL or secret.
func (s *Service) entry(c *Caller, ns namespace.Name, id, verb string) audit.Entry {
	return s.entryWith(c, ns, id, verb, audit.OutcomeSuccess)
}

func (s *Service) entryWith(c *Caller, ns namespace.Name, id, verb string, outcome audit.Outcome) audit.Entry {
	e := c.By
	e.At = s.now().UTC()
	e.Namespace = ns
	e.Subject = c.Principal.Subject
	e.TokenID = c.Principal.TokenID
	e.Action = "sink:" + verb
	e.Object = identity.NamespaceObject(ns).String()
	e.ObjectID = id
	e.Kind = audit.KindChange
	e.Outcome = outcome
	e.PrincipalKind = auditKind(c.Principal)

	return e
}

// auditKind returns the audit principal kind of p. The audit log refuses a
// kind that it does not know, so an unknown kind is recorded as no kind.
func auditKind(p identity.Principal) string {
	switch p.Kind {
	case identity.KindPerson:
		return audit.PrincipalKindPerson
	case identity.KindService:
		return audit.PrincipalKindService
	case identity.KindWorkload:
		return audit.PrincipalKindWorkload
	default:
		return ""
	}
}
